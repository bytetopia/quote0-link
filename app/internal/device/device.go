package device

import (
	"bufio"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

const (
	Width     = 152
	Height    = 296
	FrameSize = Width * Height / 8
)

type Frame [FrameSize]byte

type Link struct {
	mu   sync.Mutex
	Port string
}

type Target struct {
	Port, USBSerial string
}

func (link *Link) Target() (Target, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return Target{}, err
	}
	var selected Target
	for _, port := range ports {
		if !port.IsUSB || !strings.EqualFold(port.VID, "303a") || port.PID != "1001" {
			continue
		}
		if link.Port != "" && !strings.EqualFold(link.Port, port.Name) {
			continue
		}
		if selected.Port != "" {
			return Target{}, errors.New("multiple Espressif USB devices; disconnect the others or start with --port")
		}
		selected = Target{Port: port.Name, USBSerial: port.SerialNumber}
	}
	if selected.Port == "" {
		return Target{}, errors.New("Quote/0 USB serial port not found")
	}
	return selected, nil
}

func (link *Link) PortName() (string, error) {
	if link.Port != "" {
		ports, err := serial.GetPortsList()
		if err != nil {
			return "", err
		}
		for _, name := range ports {
			if strings.EqualFold(name, link.Port) {
				return name, nil
			}
		}
		return "", fmt.Errorf("configured serial port not found: %s", link.Port)
	}
	target, err := link.Target()
	return target.Port, err
}

func (link *Link) Maintain(work func(Target, func(Frame) error) error) error {
	link.mu.Lock()
	defer link.mu.Unlock()
	target, err := link.Target()
	if err != nil {
		return err
	}
	return work(target, func(frame Frame) error {
		current, err := link.Target()
		if err != nil {
			return err
		}
		if !strings.EqualFold(current.USBSerial, target.USBSerial) {
			return errors.New("USB device changed during maintenance")
		}
		return send(current.Port, frame)
	})
}

func (link *Link) Send(frame Frame) error {
	link.mu.Lock()
	defer link.mu.Unlock()
	name := link.Port
	var err error
	if name == "" {
		name, err = link.PortName()
	}
	if err != nil {
		return err
	}
	return send(name, frame)
}

func send(name string, frame Frame) error {
	port, err := serial.Open(name, &serial.Mode{BaudRate: 115200, InitialStatusBits: &serial.ModemOutputBits{}})
	if err != nil {
		return err
	}
	defer port.Close()
	if err := port.SetReadTimeout(250 * time.Millisecond); err != nil {
		return err
	}
	if err := port.ResetInputBuffer(); err != nil {
		return err
	}
	return exchange(port, frame)
}

func exchange(port io.ReadWriter, frame Frame) error {
	reader := bufio.NewReaderSize(timeoutReader{port}, 256)
	if _, err := io.WriteString(port, "\nQ0PING\n"); err != nil {
		return err
	}
	if err := await(reader, 2*time.Second, "Q0LINK 1 152 296", false); err != nil {
		return fmt.Errorf("Quote0 Link firmware not responding: %w", err)
	}
	if _, err := fmt.Fprintf(port, "Q0IMG1 %d %d 1BPP %d %08x\n", Width, Height, len(frame), crc32.ChecksumIEEE(frame[:])); err != nil {
		return err
	}
	if written, err := port.Write(frame[:]); err != nil {
		return err
	} else if written != len(frame) {
		return io.ErrShortWrite
	}
	return await(reader, 25*time.Second, "OK", true)
}

var errReadTimeout = errors.New("read timeout")

type timeoutReader struct{ io.Reader }

func (reader timeoutReader) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	if count == 0 && err == nil {
		err = errReadTimeout
	}
	return count, err
}

func await(reader *bufio.Reader, timeout time.Duration, expected string, reportError bool) error {
	end := time.Now().Add(timeout)
	var line []byte
	discard := false
	for time.Now().Before(end) {
		value, err := reader.ReadByte()
		if errors.Is(err, errReadTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		if value == '\n' {
			if !discard {
				message := strings.TrimSpace(string(line))
				if message == expected {
					return nil
				}
				if reportError && strings.HasPrefix(message, "ERR ") {
					return errors.New(message)
				}
			}
			line, discard = line[:0], false
		} else if len(line) < 128 {
			line = append(line, value)
		} else {
			discard = true
		}
	}
	return errors.New("serial response timed out")
}
