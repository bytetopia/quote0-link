package device

import (
	"slices"
	"strconv"
	"strings"
)

func ParseFirmwareVersion(value string) ([3]uint32, bool) {
	var version [3]uint32
	parts := strings.Split(value, ".")
	if len(parts) != len(version) {
		return version, false
	}
	for index, part := range parts {
		if len(part) == 0 || len(part) > 9 || (len(part) > 1 && part[0] == '0') {
			return version, false
		}
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return version, false
		}
		version[index] = uint32(number)
	}
	return version, true
}

func (status Status) CanUpdateTo(value string) bool {
	bundled, valid := ParseFirmwareVersion(value)
	if !valid || status.State != "connected" {
		return false
	}
	if status.LegacyFirmware {
		return true
	}
	current, valid := ParseFirmwareVersion(status.FirmwareVersion)
	return valid && slices.Compare(current[:], bundled[:]) < 0
}
