//go:build darwin || linux

package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"sort"
	"strings"
)

// LoadUnitPlugins scans dir for .so files and invokes register symbol on each plugin.
// Missing directory returns no error and zero loaded plugins.
func LoadUnitPlugins(dir string, opts *PluginLoadOptions) ([]string, error) {
	opts = normalizePluginLoadOptions(opts)

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("plugin dir is not a directory: %s", dir)
	}

	files := make([]string, 0, 8)
	root := filepath.Clean(dir)
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if !opts.Recursive && filepath.Clean(path) != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".so") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Strings(files)
	loaded := make([]string, 0, len(files))
	var loadErr error

	for _, file := range files {
		p, err := plugin.Open(file)
		if err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("open plugin %s: %w", file, err))
			continue
		}
		symbol, err := p.Lookup(opts.Symbol)
		if err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("lookup symbol %s in %s: %w", opts.Symbol, file, err))
			continue
		}
		if err := callPluginRegister(symbol, opts.Registry); err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("invoke register in %s: %w", file, err))
			continue
		}
		opts.Logger.Info("WF_SO_PLUGIN_LOADED", "file", file, "symbol", opts.Symbol)
		loaded = append(loaded, file)
	}

	return loaded, loadErr
}
