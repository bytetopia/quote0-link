package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/device"
)

var (
	romMAC        = regexp.MustCompile(`(?mi)^(?:BASE MAC|MAC):[ \t]*([0-9a-f]{2}(?::[0-9a-f]{2}){5})[ \t\r]*$`)
	flash4        = regexp.MustCompile(`(?mi)^Detected flash size:[ \t]*4[ \t]*MB[ \t\r]*$`)
	secureOff     = regexp.MustCompile(`(?mi)^Secure Boot:[ \t]*Disabled[ \t\r]*$`)
	encryptionOff = regexp.MustCompile(`(?mi)^Flash Encryption:[ \t]*Disabled[ \t\r]*$`)
	firmwareFiles = []struct {
		address, name string
		max           int
	}{
		{"0x0", "bootloader.bin", 0x8000},
		{"0x8000", "partition-table.bin", 0x1000},
		{"0x10000", "quote0-link.bin", 0x100000},
	}
)

type Request struct {
	Action    string `json:"action"`
	Variant   string `json:"variant"`
	MAC       string `json:"mac"`
	Confirm   bool   `json:"confirm"`
	ManualROM bool   `json:"manual_rom"`
}

type Status struct {
	Available bool      `json:"available"`
	MAC       string    `json:"mac,omitempty"`
	Port      string    `json:"port,omitempty"`
	Busy      bool      `json:"busy"`
	Action    string    `json:"action,omitempty"`
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
	Download  *Download `json:"download,omitempty"`
}

type Download struct {
	ID       uint64 `json:"id"`
	Filename string `json:"filename"`
}

type Manager struct {
	link      *device.Link
	testFrame device.Frame
	assets    string
	version   string
	mu        sync.Mutex
	closing   bool
	status    Status
	backup    []byte
	backupID  uint64
	expiry    *time.Timer
}

func New(link *device.Link, executable string, testFrame device.Frame) *Manager {
	assets := filepath.Join(filepath.Dir(executable), "maintenance")
	if runtime.GOOS == "darwin" {
		assets = filepath.Join(filepath.Dir(executable), "..", "Resources", "maintenance")
	}
	manager := &Manager{
		link: link, testFrame: testFrame, assets: assets, status: Status{State: "idle"},
	}
	file, err := os.ReadFile(filepath.Join(assets, "firmware", "standard", "version.txt"))
	if err == nil {
		version := strings.TrimSpace(string(file))
		if _, valid := device.ParseFirmwareVersion(version); valid {
			manager.version = version
		}
	}
	return manager
}

func (manager *Manager) FirmwareVersion() string {
	return manager.version
}

func (manager *Manager) tool() string {
	name := "esptool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(manager.assets, name)
}

func (manager *Manager) Status() Status {
	manager.mu.Lock()
	status := manager.status
	manager.mu.Unlock()
	if info, err := os.Stat(manager.tool()); err == nil && info.Mode().IsRegular() {
		status.Available = true
	}
	if !status.Busy {
		status.MAC, status.Port = "", ""
		if target, err := manager.link.Target(); err == nil {
			mac := canonicalMAC(target.USBSerial)
			if macPattern.MatchString(mac) {
				status.MAC, status.Port = mac, target.Port
			}
		}
	}
	return status
}

func (manager *Manager) Busy() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.status.Busy
}

func (manager *Manager) CloseIdle() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.status.Busy {
		return false
	}
	manager.closing = true
	manager.clearBackup()
	return true
}

func validateRequest(request Request, backup []byte) ([]byte, error) {
	if !request.Confirm || !macPattern.MatchString(request.MAC) {
		return nil, errors.New("请确认设备及本次操作")
	}
	switch request.Action {
	case "backup":
		if !validVariant(request.Variant) {
			return nil, errors.New("请选择屏幕版本")
		}
		if len(backup) != 0 {
			return nil, errors.New("备份原厂不需要上传文件")
		}
		return nil, nil
	case "install":
		if request.Variant != "standard" {
			return nil, errors.New("请选择标准版；首发版（UC8151）暂不支持安装 Link 固件")
		}
		return nil, nil
	case "restore":
	default:
		return nil, errors.New("未知的设备维护操作")
	}
	if len(backup) == 0 {
		return nil, errors.New("请选择这台设备的 .q0backup 原厂备份")
	}
	header, image, err := parseBackup(backup)
	if err != nil {
		return nil, err
	}
	if header.MAC != request.MAC {
		return nil, errors.New("备份不属于当前设备；没有写入")
	}
	return image, nil
}

func (manager *Manager) Start(request Request, backup []byte) error {
	restoreImage, err := validateRequest(request, backup)
	if err != nil {
		return err
	}
	if info, err := os.Stat(manager.tool()); err != nil || !info.Mode().IsRegular() {
		return errors.New("此开发包缺少 esptool，请使用完整安装包")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closing || manager.status.Busy {
		return errors.New("另一个维护任务正在进行，或应用正退出")
	}
	if request.Action == "backup" {
		manager.clearBackup()
	}
	manager.status = Status{
		Busy: true, MAC: request.MAC, Action: request.Action, State: "running", Download: manager.status.Download,
		Message: "等待释放串口。请保持 USB 连接，不要退出 App。",
	}
	go func() {
		err := manager.link.Maintain(func(target device.Target, show func(device.Frame) error) error {
			return manager.perform(target, show, request, restoreImage)
		})
		manager.mu.Lock()
		defer manager.mu.Unlock()
		manager.status.Busy = false
		if err != nil {
			manager.status.State, manager.status.Message = "error", err.Error()
		} else {
			manager.status.State = "done"
			manager.status.Message = map[string]string{
				"backup":  "双读 SHA-256 校验一致，请下载并妥善保存 .q0backup 文件。",
				"install": "固件已写入并校验，设备已确认刷新测试卡。",
				"restore": "原厂 Flash 已写回并校验。请在 Dot. App 检查设备是否正常。",
			}[request.Action]
		}
	}()
	return nil
}

func (manager *Manager) Download(id uint64) (Download, []byte, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.status.Download == nil || manager.status.Download.ID != id {
		return Download{}, nil, errors.New("备份下载已过期或不存在，请重新备份")
	}
	return *manager.status.Download, manager.backup, nil
}

func (manager *Manager) stashBackup(mac string, data []byte) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.clearBackup()
	manager.backupID++
	id := manager.backupID
	manager.backup = data
	manager.status.Download = &Download{ID: id, Filename: "quote0-" + mac + ".q0backup"}
	manager.expiry = time.AfterFunc(10*time.Minute, func() {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if manager.backupID == id {
			manager.clearBackup()
			if manager.status.Action == "backup" && manager.status.State == "done" {
				manager.status.Message = "此备份的临时下载已过期。需要新副本时，请重新备份。"
			}
		}
	})
}

func (manager *Manager) clearBackup() {
	if manager.expiry != nil {
		manager.expiry.Stop()
		manager.expiry = nil
	}
	manager.status.Download = nil
	manager.backup = nil
}

func (manager *Manager) step(message string) {
	manager.mu.Lock()
	manager.status.Message = message
	manager.mu.Unlock()
}

func (manager *Manager) perform(target device.Target, show func(device.Frame) error, request Request, restoreImage []byte) error {
	if canonicalMAC(target.USBSerial) != request.MAC {
		return errors.New("已连接设备已变更；请重新选择并确认")
	}
	var segments []string
	if request.Action == "install" {
		var err error
		segments, err = manager.imageSegments()
		if err != nil {
			return err
		}
	}
	work, err := os.MkdirTemp("", "quote0-link-maintenance-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	call := func(after, command string, args ...string) (string, error) {
		return manager.runTool(work, target, request.ManualROM, after, command, args...)
	}
	manager.step("正在核对芯片 ID、启动安全设置及 Flash 容量…")
	identity, err := call("no-reset", "read-mac")
	if err != nil {
		return err
	}
	match := romMAC.FindStringSubmatch(identity)
	if len(match) != 2 || canonicalMAC(match[1]) != request.MAC {
		return errors.New("USB 序列号与芯片 MAC 不匹配；没有写入")
	}
	security, err := call("no-reset", "get-security-info")
	if err != nil {
		return err
	}
	if !secureOff.MatchString(security) || !encryptionOff.MatchString(security) {
		return errors.New("不能确认安全启动和 Flash 加密均关闭；没有写入")
	}
	size, err := call("no-reset", "flash-id")
	if err != nil {
		return err
	}
	if !flash4.MatchString(size) {
		return errors.New("只支持已确认的 4 MiB Flash；没有写入")
	}

	if request.Action == "backup" {
		return manager.capture(work, request, call)
	}
	if request.Action == "restore" {
		imagePath := filepath.Join(work, "stock.bin")
		if err := os.WriteFile(imagePath, restoreImage, 0600); err != nil {
			return err
		}
		segments = []string{"0x0", imagePath}
	}
	manager.step("正在写入 Flash。请勿拔线、休眠或退出 App。")
	flashArgs := append([]string{"--flash-mode", "keep", "--flash-freq", "keep", "--flash-size", "keep"}, segments...)
	if _, err := call("no-reset", "write-flash", flashArgs...); err != nil {
		return fmt.Errorf("写入未完成。请保持设备连接并重试「恢复原厂」：%w", err)
	}
	manager.step("正在校验写入内容，随后重启设备…")
	if _, err := call("hard-reset", "verify-flash", flashArgs...); err != nil {
		return fmt.Errorf("写入后的校验未完成。请使用原厂备份恢复：%w", err)
	}
	if request.Action == "install" {
		manager.step("固件校验通过，正在等待设备重启并发送测试卡…")
		time.Sleep(1200 * time.Millisecond)
		if err := show(manager.testFrame); err != nil {
			return fmt.Errorf("固件已写入并校验，但未确认测试卡上屏：%w", err)
		}
	}
	return nil
}

func (manager *Manager) capture(work string, request Request, call func(string, string, ...string) (string, error)) error {
	first, second := filepath.Join(work, "read-a.bin"), filepath.Join(work, "read-b.bin")
	manager.step("正在读取整片 Flash（第 1 / 2 次）…")
	if _, err := call("no-reset", "read-flash", "0", "ALL", first); err != nil {
		return err
	}
	manager.step("正在读取整片 Flash（第 2 / 2 次），完成后重启设备…")
	if _, err := call("hard-reset", "read-flash", "0", "ALL", second); err != nil {
		return err
	}
	left, err := os.ReadFile(first)
	if err != nil {
		return err
	}
	right, err := os.ReadFile(second)
	if err != nil {
		return err
	}
	if len(left) != FlashSize || len(right) != FlashSize || sha256.Sum256(left) != sha256.Sum256(right) {
		return errors.New("两次完整读取的尺寸或 SHA-256 不一致；没有生成备份")
	}
	content, err := makeBackup(request.MAC, request.Variant, left)
	if err != nil {
		return err
	}
	manager.stashBackup(request.MAC, content)
	return nil
}

func (manager *Manager) runTool(work string, target device.Target, manual bool, after, command string, args ...string) (string, error) {
	before := "default-reset"
	if manual {
		before = "no-reset"
	}
	parameters := []string{
		"--chip", "esp32c3", "--baud", "460800", "--connect-attempts", "3",
	}
	if command == "read-mac" || command == "get-security-info" || command == "flash-id" {
		parameters = append(parameters, "--no-stub")
	}
	parameters = append(parameters,
		"--port-filter", "vid=0x303a", "pid=0x1001", "serial="+target.USBSerial,
		"--before", before, "--after", after, command,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, manager.tool(), append(parameters, args...)...)
	cmd.Dir = work
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "ESPTOOL") && !strings.HasPrefix(strings.ToUpper(key), "PYTHON") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "NO_COLOR=1", "TERM=dumb")
	configureProcess(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s 超时", command)
		}
		const maxLog = 2400
		if len(output) > maxLog {
			output = output[len(output)-maxLog:]
		}
		return "", fmt.Errorf("%s 未完成：%w\n%s", command, err, bytes.TrimSpace(output))
	}
	return string(output), nil
}

func (manager *Manager) imageSegments() ([]string, error) {
	dir := filepath.Join(manager.assets, "firmware", "standard")
	data, err := os.ReadFile(filepath.Join(dir, "sha256.json"))
	var manifest map[string]string
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		return nil, errors.New("安装包没有有效的标准版固件清单")
	}
	var segments []string
	for _, item := range firmwareFiles {
		name := filepath.Join(dir, item.name)
		data, err := os.ReadFile(name)
		if err != nil || len(data) == 0 || len(data) > item.max {
			return nil, fmt.Errorf("安装包固件文件无效：%s", item.name)
		}
		sum := sha256.Sum256(data)
		if manifest[item.name] != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("安装包固件校验失败：%s", item.name)
		}
		segments = append(segments, item.address, name)
	}
	return segments, nil
}
