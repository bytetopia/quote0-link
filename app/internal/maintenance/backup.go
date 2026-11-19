package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
