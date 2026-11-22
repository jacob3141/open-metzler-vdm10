//go:build !linux

package main

import (
	"errors"
	"os"
)

// readTamper needs the vendor driver of the door station.
func readTamper(*os.File) (bool, error) { return false, errors.ErrUnsupported }
