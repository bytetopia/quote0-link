//go:build windows

package device

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	cfgmgr32          = windows.NewLazySystemDLL("cfgmgr32.dll")
	procCMGetParent   = cfgmgr32.NewProc("CM_Get_Parent")
	procCMGetDevIDW   = cfgmgr32.NewProc("CM_Get_Device_IDW")
	espressifUSBRoot  = `USB\VID_303A&PID_1001\`
	guidDevClassPorts = windows.GUID{Data1: 0x4d36e978, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}
)

// parentUSBSerial recovers the USB serial number for a composite device's
// CDC interface. Windows only assigns the serial to the parent device, so the
// enumerator reports an empty serial for the COM port itself.
func parentUSBSerial(portName string) string {
	devs, err := windows.SetupDiGetClassDevsEx(&guidDevClassPorts, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return ""
	}
	defer devs.Close()
	for index := 0; ; index++ {
		info, err := devs.EnumDeviceInfo(index)
		if err != nil {
			return ""
		}
		handle, err := devs.OpenDevRegKey(info, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DEV, windows.KEY_READ)
		if err != nil {
			continue
		}
		key := registry.Key(handle)
		name, _, err := key.GetStringValue("PortName")
		key.Close()
		if err != nil || !strings.EqualFold(name, portName) {
			continue
		}
		var parent uint32
		if ret, _, _ := procCMGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(info.DevInst), 0); ret != 0 {
			return ""
		}
		buffer := make([]uint16, 256)
		if ret, _, _ := procCMGetDevIDW.Call(uintptr(parent), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0); ret != 0 {
			return ""
		}
		id := windows.UTF16ToString(buffer)
		if len(id) <= len(espressifUSBRoot) || !strings.EqualFold(id[:len(espressifUSBRoot)], espressifUSBRoot) {
			return ""
		}
		serial := id[len(espressifUSBRoot):]
		// Windows synthesises an instance ID containing '&' when the device has no serial.
		if strings.ContainsAny(serial, `&\`) {
			return ""
		}
		return serial
	}
}
