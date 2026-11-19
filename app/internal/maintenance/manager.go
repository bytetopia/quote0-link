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
	Available bool     `json:"available"`
	MAC       string   `json:"mac,omitempty"`
	Port      string   `json:"port,omitempty"`
	Backups   []string `json:"backups"`
	Busy      bool     `json:"busy"`
	State     string   `json:"state"`
	Message   string   `json:"message,omitempty"`
}

type Manager struct {
	link      *device.Link
	testFrame device.Frame
	assets    string
	backups   string
	mu        sync.Mutex
	closing   bool
	status    Status
}

func New(link *device.Link, executable, dataDirectory string, testFrame device.Frame) *Manager {
	assets := filepath.Join(filepath.Dir(executable), "maintenance")
	if runtime.GOOS == "darwin" {
		assets = filepath.Join(filepath.Dir(executable), "..", "Resources", "maintenance")
	}
	return &Manager{
		link: link, testFrame: testFrame, assets: assets,
		backups: filepath.Join(dataDirectory, "backups"), status: Status{State: "idle"},
	}
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
	status.Backups = manager.listBackups()
	if !status.Busy {
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
	return true
}

func (manager *Manager) Start(request Request) error {
	if !request.Confirm || !macPattern.MatchString(request.MAC) || !validVariant(request.Variant) {
		return errors.New("请确认设备、屏幕版本及本次操作")
	}
	switch request.Action {
	case "backup", "restore":
	case "install":
		if request.Variant != "standard" {
			return errors.New("首发版（UC8151）暂不支持安装 Link 固件")
		}
	default:
		return errors.New("未知的设备维护操作")
	}
	if info, err := os.Stat(manager.tool()); err != nil || !info.Mode().IsRegular() {
		return errors.New("此开发包缺少 esptool，请使用完整安装包")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closing || manager.status.Busy {
		return errors.New("另一个维护任务正在进行，或应用正退出")
	}
	manager.status = Status{
		Busy: true, MAC: request.MAC, State: "running",
		Message: "等待释放串口。请保持 USB 连接，不要退出 App。",
	}
	go func() {
		err := manager.link.Maintain(func(target device.Target, show func(device.Frame) error) error {
			return manager.perform(target, show, request)
		})
		manager.mu.Lock()
		defer manager.mu.Unlock()
		manager.status.Busy = false
		if err != nil {
			manager.status.State, manager.status.Message = "error", err.Error()
		} else {
			manager.status.State = "done"
			manager.status.Message = map[string]string{
				"backup":  "原厂备份已保存，双读 SHA-256 校验一致。建议导出保留一份。",
				"install": "固件已写入并校验，设备已确认刷新测试卡。",
				"restore": "原厂 Flash 已写回并校验。请在 Dot. App 检查设备是否正常。",
			}[request.Action]
		}
	}()
	return nil
}

func (manager *Manager) Import(data []byte) (string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.status.Busy || manager.closing {
		return "", errors.New("请等当前维护操作完成")
	}
	return manager.writeBackup(data)
}

func (manager *Manager) Export(mac string) ([]byte, error) {
	return manager.readBackup(mac)
}

func (manager *Manager) step(message string) {
	manager.mu.Lock()
	manager.status.Message = message
	manager.mu.Unlock()
}

func (manager *Manager) perform(target device.Target, show func(device.Frame) error, request Request) error {
	if canonicalMAC(target.USBSerial) != request.MAC {
		return errors.New("已连接设备已变更；请重新选择并确认")
	}
	var restoreImage []byte
	var segments []string
	if request.Action == "backup" {
		if _, err := os.Lstat(manager.backupPath(request.MAC)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("这台设备的备份已存在或无法访问；不会覆盖")
		}
	} else {
		data, err := manager.readBackup(request.MAC)
		if err != nil {
			return fmt.Errorf("未找到同一台设备的有效备份：%w", err)
		}
		header, image, err := parseBackup(data)
		if err != nil || header.Variant != request.Variant {
			return errors.New("所选屏幕版本与备份记录不匹配")
		}
		restoreImage = image
		if request.Action == "install" {
			segments, err = manager.imageSegments()
			if err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(manager.backups), 0700); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(manager.backups), ".maintenance-")
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
		return errors.New("两次完整读取的尺寸或 SHA-256 不一致；没有保存备份")
	}
	content, err := makeBackup(request.MAC, request.Variant, left)
	if err != nil {
		return err
	}
	_, err = manager.writeBackup(content)
	return err
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
