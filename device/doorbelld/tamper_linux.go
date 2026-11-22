package main

import (
	"os"
	"syscall"
	"unsafe"
)

// readTamper returns true while the switch is open, i.e. the station is off the wall.
func readTamper(f *os.File) (bool, error) {
	v := int32(-1)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ddmIocDoorTamper, uintptr(unsafe.Pointer(&v))); e != 0 {
		return false, e
	}
	return v == 0, nil
}
