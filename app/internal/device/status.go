package device

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

type Status struct {
	State             string `json:"state"`
	Port              string `json:"port,omitempty"`
	FirmwareVersion   string `json:"firmware_version,omitempty"`
	LegacyFirmware    bool   `json:"legacy_firmware,omitempty"`
	USBPower          *bool  `json:"usb_power,omitempty"`
	BatteryMillivolts int    `json:"battery_mv,omitempty"`
	Error             string `json:"error,omitempty"`
}

func (link *Link) Status() Status {
	target, err := link.Target()
	if err != nil {
		return Status{State: "disconnected", Error: err.Error()}
	}
	if !link.mu.TryLock() {
		return Status{State: "busy", Port: target.Port}
	}
	defer link.mu.Unlock()
	port, err := openPort(target.Port)
	if err != nil {
		return Status{State: "unverified", Port: target.Port, Error: err.Error()}
	}
	defer port.Close()
	status := probeStatus(port)
	status.Port = target.Port
	return status
}

func probeStatus(port io.ReadWriter) Status {
	reader := bufio.NewReaderSize(timeoutReader{port}, 256)
	if err := handshake(port, reader); err != nil {
		return Status{State: "unverified", Error: err.Error()}
	}
	status := Status{State: "connected"}
	if _, err := io.WriteString(port, "Q0VERSION\n"); err != nil {
		return status
	}
	line, err := awaitReply(reader, time.Second, true, func(line string) bool {
		return strings.HasPrefix(line, "Q0FW ")
	})
	if err != nil && err.Error() == "ERR header" {
		status.LegacyFirmware = true
	} else if err == nil {
		version := strings.TrimPrefix(line, "Q0FW ")
		if _, valid := ParseFirmwareVersion(version); valid {
			status.FirmwareVersion = version
		}
	}
	if _, err := io.WriteString(port, "Q0STATUS\n"); err != nil {
		return status
	}
	_, _ = awaitReply(reader, time.Second, true, func(line string) bool {
		usb, mv, err := parsePower(line)
		if err != nil {
			return false
		}
		status.USBPower, status.BatteryMillivolts = usb, mv
		return true
	})
	return status
}

func parsePower(line string) (*bool, int, error) {
	fields := strings.Fields(line)
	invalid := errors.New("invalid Quote0 Link power status")
	if len(fields) != 4 || fields[0] != "Q0POWER" || fields[1] != "1" {
		return nil, 0, invalid
	}
	var usb *bool
	switch fields[2] {
	case "0", "1":
		present := fields[2] == "1"
		usb = &present
	case "-1":
	default:
		return nil, 0, invalid
	}
	mv, err := strconv.Atoi(fields[3])
	if err != nil || (mv != -1 && (mv < 2500 || mv > 4500)) {
		return nil, 0, invalid
	}
	return usb, max(mv, 0), nil
}
