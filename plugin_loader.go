package workflow

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
)

const DefaultPluginRegisterSymbol = "WorkflowRegister"

// PluginLoadOptions controls startup plugin loading.
type PluginLoadOptions struct {
	Registry  *Registry
	Logger    *slog.Logger
	Recursive bool
	Symbol    string
}

func normalizePluginLoadOptions(opts *PluginLoadOptions) *PluginLoadOptions {
	if opts == nil {
		opts = &PluginLoadOptions{}
	}
	if opts.Registry == nil {
		opts.Registry = DefaultRegistry
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Symbol == "" {
		opts.Symbol = DefaultPluginRegisterSymbol
	}
	return opts
}

func callPluginRegister(symbol any, registry *Registry) error {
	switch fn := symbol.(type) {
	case func() error:
		return fn()
	case func(*Registry) error:
		return fn(registry)
	case func(*Registry):
		fn(registry)
		return nil
	default:
		return fmt.Errorf("unsupported register symbol signature: %T", symbol)
	}
}

// DefaultPluginDir returns plugins/<goos>-<goarch> under baseDir.
func DefaultPluginDir(baseDir string) string {
	return filepath.Join(baseDir, "plugins", runtime.GOOS+"-"+runtime.GOARCH)
}
