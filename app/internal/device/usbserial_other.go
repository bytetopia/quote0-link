//go:build !windows

package device

func parentUSBSerial(string) string { return "" }
