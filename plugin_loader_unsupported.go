//go:build !darwin && !linux

package workflow

import (
	"fmt"
	"runtime"
)

// LoadUnitPlugins is unsupported on current platform because Go's plugin package
// only works on a limited set of systems.
func LoadUnitPlugins(dir string, opts *PluginLoadOptions) ([]string, error) {
	return nil, fmt.Errorf("go plugin loading is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
}
