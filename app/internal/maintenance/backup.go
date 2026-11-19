package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const FlashSize = 4 << 20
const MaxBackup = FlashSize + 4096
const backupMagic = "QUOTE0-LINK-BACKUP-1\n"

var macPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

type backupHeader struct {
	MAC     string    `json:"mac"`
	Variant string    `json:"variant"`
	SHA256  string    `json:"sha256"`
	At      time.Time `json:"created_at"`
}

func canonicalMAC(value string) string {
	return strings.ToLower(strings.ReplaceAll(value, ":", ""))
}

func validVariant(value string) bool {
	return value == "standard" || value == "early"
}

func makeBackup(mac, variant string, firmware []byte) ([]byte, error) {
	if !macPattern.MatchString(mac) || !validVariant(variant) || !isStockCandidate(firmware) {
		return nil, errors.New("原厂备份不符合预期：无法确认是完整的 4 MiB 镜像，或检测到了 Link 固件")
	}
	sum := sha256.Sum256(firmware)
	header, err := json.Marshal(backupHeader{mac, variant, hex.EncodeToString(sum[:]), time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	return bytes.Join([][]byte{[]byte(backupMagic), header, []byte{'\n'}, firmware}, nil), nil
}

func parseBackup(content []byte) (backupHeader, []byte, error) {
	var header backupHeader
	errInvalid := errors.New("备份格式无效或 SHA-256 校验失败")
	if !bytes.HasPrefix(content, []byte(backupMagic)) || len(content) > MaxBackup {
		return header, nil, errInvalid
	}
	rawHeader, firmware, found := bytes.Cut(content[len(backupMagic):], []byte{'\n'})
	if !found || json.Unmarshal(rawHeader, &header) != nil ||
		!macPattern.MatchString(header.MAC) || !validVariant(header.Variant) || !isStockCandidate(firmware) {
		return header, nil, errInvalid
	}
	sum := sha256.Sum256(firmware)
	if header.SHA256 != hex.EncodeToString(sum[:]) {
		return header, nil, errInvalid
	}
	return header, firmware, nil
}

func isStockCandidate(firmware []byte) bool {
	return len(firmware) == FlashSize && firmware[0] == 0xe9 &&
		!bytes.Contains(firmware, []byte("Q0LINK 1 152 296\n"))
}

func (manager *Manager) writeBackup(data []byte) (string, error) {
	header, _, err := parseBackup(data)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(manager.backups, 0700); err != nil {
		return "", err
	}
	path := manager.backupPath(header.MAC)
	if _, err := os.Lstat(path); err == nil {
		return "", errors.New("这台设备的备份已存在，未覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.CreateTemp(manager.backups, ".backup-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return "", err
	}
	if err := os.Link(file.Name(), path); err != nil {
		return "", err
	}
	return header.MAC, nil
}

func (manager *Manager) backupPath(mac string) string {
	return filepath.Join(manager.backups, mac+".q0backup")
}

func (manager *Manager) readBackup(mac string) ([]byte, error) {
	if !macPattern.MatchString(mac) {
		return nil, errors.New("无效的备份标识")
	}
	path := manager.backupPath(mac)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBackup {
		return nil, errors.New("备份不是有效的本机备份文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	header, _, err := parseBackup(data)
	if err != nil {
		return nil, err
	}
	if header.MAC != mac {
		return nil, errors.New("备份标识不匹配")
	}
	return data, nil
}

func (manager *Manager) listBackups() []string {
	names := []string{}
	entries, _ := os.ReadDir(manager.backups)
	for _, item := range entries {
		if item.Type().IsRegular() && strings.HasSuffix(item.Name(), ".q0backup") {
			if mac := strings.TrimSuffix(item.Name(), ".q0backup"); macPattern.MatchString(mac) {
				names = append(names, mac)
			}
		}
	}
	return names
}
