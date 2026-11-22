package main

import (
	"os"
	"syscall"
	"unsafe"
)

// readTamper returns true while the tamper switch is pressed: the driver gives 0 then and 1
// while the switch is free.
func readTamper(f *os.File) (bool, error) {
	v := int32(-1)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ddmIocDoorTamper, uintptr(unsafe.Pointer(&v))); e != 0 {
		return false, e
	}
	return v == 0, nil
}
