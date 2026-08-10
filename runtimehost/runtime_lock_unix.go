//go:build !windows

package runtimehost

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryLockRuntimeFile(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return false, err
}

func unlockRuntimeFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
