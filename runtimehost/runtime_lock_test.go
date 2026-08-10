package runtimehost

import (
	"strings"
	"testing"
)

func TestRuntimeDirectoryLockRejectsConcurrentOwnerAndReleases(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireRuntimeDirectoryLock(directory)
	if err != nil {
		t.Fatalf("acquire first runtime lock: %v", err)
	}
	second, err := acquireRuntimeDirectoryLock(directory)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second runtime lock error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release first runtime lock: %v", err)
	}

	reacquired, err := acquireRuntimeDirectoryLock(directory)
	if err != nil {
		t.Fatalf("reacquire runtime lock: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatalf("release reacquired runtime lock: %v", err)
	}
}
