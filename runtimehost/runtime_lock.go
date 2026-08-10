package runtimehost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const runtimeLockFilename = ".runtime.lock"

type runtimeDirectoryLock struct {
	file *os.File
}

func acquireRuntimeDirectoryLock(directory string) (*runtimeDirectoryLock, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create workflow data directory: %w", err)
	}
	path := filepath.Join(directory, runtimeLockFilename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open workflow runtime lock: %w", err)
	}
	locked, err := tryLockRuntimeFile(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock workflow data directory: %w", err)
	}
	if !locked {
		owner := runtimeLockOwner(file)
		_ = file.Close()
		if owner == "" {
			return nil, errors.New("workflow data directory is already in use")
		}
		return nil, fmt.Errorf("workflow data directory is already in use by PID %s", owner)
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockRuntimeFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("reset workflow runtime lock: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = unlockRuntimeFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("seek workflow runtime lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = unlockRuntimeFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("write workflow runtime lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = unlockRuntimeFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("sync workflow runtime lock: %w", err)
	}
	return &runtimeDirectoryLock{file: file}, nil
}

func (lock *runtimeDirectoryLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := unlockRuntimeFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock workflow data directory: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close workflow runtime lock: %w", closeErr)
	}
	return nil
}

func runtimeLockOwner(file *os.File) string {
	if file == nil {
		return ""
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	contents, err := io.ReadAll(io.LimitReader(file, 64))
	if err != nil {
		return ""
	}
	owner := strings.TrimSpace(string(contents))
	for _, character := range owner {
		if character < '0' || character > '9' {
			return ""
		}
	}
	return owner
}
