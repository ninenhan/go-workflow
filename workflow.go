// Package workflow provides the high-level embedding facade. Applications that
// need complete control can continue composing scheduler, worker, and stores
// directly from their respective packages.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/persist"
	"github.com/ninenhan/go-workflow/persist/defaultstore"
	memorypersist "github.com/ninenhan/go-workflow/persist/memory"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/worker"
)

const DefaultDataDirectory = "var/go-workflow"

type BuiltinMode string

const (
	BuiltinStandard BuiltinMode = "standard"
	BuiltinDisabled BuiltinMode = "disabled"
)

// RuntimeOptions configures the worker and scheduler independently from the
// selected persistence adapter.
type RuntimeOptions struct {
	CredentialScope string
	Builtins        BuiltinMode
	ConfigureWorker func(*worker.Service) error
}

// Options is the strict advanced-construction path. Stores must be complete;
// nil components are rejected instead of receiving implicit memory fallbacks.
type Options struct {
	Stores  *persist.Stores
	Runtime RuntimeOptions
}

type DefaultOptions struct {
	DataDirectory string
	TablePrefix   string
	Runtime       RuntimeOptions
}

func DefaultConfig() DefaultOptions {
	return DefaultOptions{
		DataDirectory: DefaultDataDirectory,
		Runtime:       RuntimeOptions{Builtins: BuiltinStandard},
	}
}

type Application struct {
	Scheduler *scheduler.Service
	Worker    *worker.Service
	Stores    *persist.Stores

	shutdownMu sync.Mutex
	closed     bool
}

// OpenDefault starts the stable default ORM configuration using SQLite in
// var/go-workflow. It never falls back to memory when opening storage fails.
func OpenDefault(ctx context.Context) (*Application, error) {
	return OpenDefaultWithOptions(ctx, DefaultConfig())
}

func OpenDefaultWithOptions(ctx context.Context, opts DefaultOptions) (*Application, error) {
	if strings.TrimSpace(opts.DataDirectory) == "" {
		return nil, errors.New("default data directory is required")
	}
	stores, err := defaultstore.Open(defaultstore.Config{
		DataDirectory: filepath.Clean(opts.DataDirectory),
		TablePrefix:   opts.TablePrefix,
	})
	if err != nil {
		return nil, err
	}
	application, err := New(ctx, Options{Stores: stores, Runtime: opts.Runtime})
	if err != nil {
		return nil, errors.Join(err, stores.Close())
	}
	return application, nil
}

func OpenMemory(ctx context.Context) (*Application, error) {
	return OpenMemoryWithOptions(ctx, RuntimeOptions{Builtins: BuiltinStandard})
}

func OpenMemoryWithOptions(ctx context.Context, opts RuntimeOptions) (*Application, error) {
	stores, err := memorypersist.New()
	if err != nil {
		return nil, err
	}
	application, err := New(ctx, Options{Stores: stores, Runtime: opts})
	if err != nil {
		return nil, errors.Join(err, stores.Close())
	}
	return application, nil
}

func New(_ context.Context, opts Options) (*Application, error) {
	if err := opts.Stores.Validate(); err != nil {
		return nil, err
	}
	builtins, err := resolveBuiltinMode(opts.Runtime.Builtins)
	if err != nil {
		return nil, err
	}
	workerService, err := worker.NewService(worker.Options{
		Enabled:            true,
		RegisterBuiltins:   builtins,
		CredentialResolver: opts.Stores.Credentials,
	})
	if err != nil {
		return nil, err
	}
	if opts.Runtime.ConfigureWorker != nil {
		if err := opts.Runtime.ConfigureWorker(workerService); err != nil {
			return nil, fmt.Errorf("configure workflow worker: %w", err)
		}
	}
	service, err := scheduler.NewService(scheduler.Options{
		EnableEmbeddedWorker:   true,
		EmbeddedWorker:         workerService,
		Store:                  opts.Stores.Runtime,
		Definitions:            opts.Stores.Definitions,
		Workspace:              opts.Stores.Workspace,
		Automations:            opts.Stores.Automations,
		Credentials:            opts.Stores.Credentials,
		DefaultCredentialScope: opts.Runtime.CredentialScope,
	})
	if err != nil {
		return nil, err
	}
	return &Application{
		Scheduler: service,
		Worker:    workerService,
		Stores:    opts.Stores,
	}, nil
}

func resolveBuiltinMode(mode BuiltinMode) (bool, error) {
	if mode == "" {
		mode = BuiltinStandard
	}
	switch mode {
	case BuiltinStandard:
		return true, nil
	case BuiltinDisabled:
		return false, nil
	default:
		return false, fmt.Errorf("unsupported builtin mode %q", mode)
	}
}

// Shutdown stops scheduler-owned goroutines before closing persistence. A
// timeout leaves stores open so the caller can retry shutdown safely.
func (a *Application) Shutdown(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	a.shutdownMu.Lock()
	defer a.shutdownMu.Unlock()
	if a.closed {
		return a.Stores.Close()
	}
	if err := a.Scheduler.Shutdown(ctx); err != nil {
		return err
	}
	a.closed = true
	return a.Stores.Close()
}

func (a *Application) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.Shutdown(ctx)
}
