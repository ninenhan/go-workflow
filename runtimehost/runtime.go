package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/persist/localdb"
	"github.com/ninenhan/go-workflow/scheduler"
)

const defaultShutdownTimeout = 10 * time.Second

// Host owns one single-node workflow runtime: scheduler, optional embedded
// worker, stores, automations, and HTTP transport. Constructing a disabled Host
// has no filesystem, network, or goroutine side effects.
type Host struct {
	mu sync.Mutex

	config      Config
	logger      *log.Logger
	runtimeLock *runtimeDirectoryLock
	database    *localdb.Database
	service     *scheduler.Service
	server      *http.Server
	listener    net.Listener

	started      bool
	shuttingDown bool
	closed       bool
	serveDone    chan struct{}
	shutdownDone chan struct{}
	serveErr     error
	shutdownErr  error
}

// New constructs an explicitly enabled single-node host. It opens the runtime
// stores so callers can configure the embedded worker through Service before
// Start. When config.Enabled is false, New returns an inert Host.
func New(config Config, logger *log.Logger) (*Host, error) {
	host := &Host{
		config:       config,
		logger:       logger,
		serveDone:    make(chan struct{}),
		shutdownDone: make(chan struct{}),
	}
	if !config.Enabled {
		host.closed = true
		close(host.serveDone)
		close(host.shutdownDone)
		return host, nil
	}
	if logger == nil {
		return nil, errors.New("workflow host logger is nil")
	}
	if config.DefaultScope == "" {
		config.DefaultScope = "local-workspace"
	}
	if config.AutomationPeriod <= 0 {
		config.AutomationPeriod = time.Second
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	host.config = config

	runtimeLock, err := acquireRuntimeDirectoryLock(config.DataDirectory)
	if err != nil {
		return nil, err
	}
	host.runtimeLock = runtimeLock
	fail := func(cause error) (*Host, error) {
		return nil, errors.Join(cause, host.closeStores())
	}

	credentialStore, err := credential.OpenFileStore(filepath.Join(config.DataDirectory, "credentials"))
	if err != nil {
		return fail(fmt.Errorf("open encrypted credential store: %w", err))
	}
	database, err := localdb.Open(filepath.Join(config.DataDirectory, "workflow.db"))
	if err != nil {
		return fail(fmt.Errorf("open workflow database: %w", err))
	}
	host.database = database

	service, err := scheduler.NewService(scheduler.Options{
		EnableEmbeddedWorker:   !config.DisableEmbeddedWorker,
		Store:                  database.Runtime,
		Definitions:            database.Definitions,
		Workspace:              database.Workspace,
		Automations:            database.Automations,
		Credentials:            credentialStore,
		DefaultCredentialScope: config.DefaultScope,
	})
	if err != nil {
		return fail(fmt.Errorf("create scheduler service: %w", err))
	}
	host.service = service

	handler, err := NewHTTPHandler(
		scheduler.NewHTTPHandler(service).Handler(),
		config.WebDirectory,
		config.DesktopToken,
		host.requestShutdown,
	)
	if err != nil {
		return fail(fmt.Errorf("configure workflow web application: %w", err))
	}
	host.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	return host, nil
}

// Start begins serving without blocking. Cancelling ctx gracefully shuts down
// the Host. Start may be called exactly once.
func (host *Host) Start(ctx context.Context) error {
	if host == nil {
		return errors.New("workflow host is nil")
	}
	if ctx == nil {
		return errors.New("workflow host context is nil")
	}
	if !host.config.Enabled {
		return nil
	}

	host.mu.Lock()
	defer host.mu.Unlock()
	if host.closed || host.shuttingDown {
		return errors.New("workflow host is closed")
	}
	if host.started {
		return errors.New("workflow host is already started")
	}
	listener, err := net.Listen("tcp", host.config.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", host.config.Address, err)
	}
	if !host.config.DisableAutomations {
		if err := host.service.StartAutomations(context.Background(), host.config.AutomationPeriod); err != nil {
			_ = listener.Close()
			return fmt.Errorf("start workflow automations: %w", err)
		}
	}
	host.listener = listener
	host.started = true
	host.logger.Printf("workflow host %s (%s) listening on http://%s", host.config.Version, host.config.Mode, listener.Addr())

	go host.serve()
	go func() {
		select {
		case <-ctx.Done():
		case <-host.serveDone:
		}
		host.requestShutdown()
	}()
	return nil
}

// Service exposes the canonical scheduler so an embedding application can
// register Units or executors before Start.
func (host *Host) Service() *scheduler.Service {
	if host == nil {
		return nil
	}
	return host.service
}

// Address returns the bound address after Start. It is empty for a disabled or
// not-yet-started Host.
func (host *Host) Address() string {
	if host == nil {
		return ""
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.listener == nil {
		return ""
	}
	return host.listener.Addr().String()
}

// Wait blocks until all Host resources are closed.
func (host *Host) Wait() error {
	if host == nil {
		return errors.New("workflow host is nil")
	}
	host.mu.Lock()
	if !host.started && !host.closed {
		host.mu.Unlock()
		return errors.New("workflow host is not started")
	}
	done := host.shutdownDone
	host.mu.Unlock()
	<-done
	host.mu.Lock()
	defer host.mu.Unlock()
	return errors.Join(host.serveErr, host.shutdownErr)
}

// Shutdown is idempotent and closes the HTTP server, scheduler, database, and
// runtime-directory lock in ownership order.
func (host *Host) Shutdown(ctx context.Context) error {
	if host == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	host.mu.Lock()
	if host.closed {
		err := host.shutdownErr
		host.mu.Unlock()
		return err
	}
	if host.shuttingDown {
		done := host.shutdownDone
		host.mu.Unlock()
		select {
		case <-done:
			host.mu.Lock()
			err := host.shutdownErr
			host.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	host.shuttingDown = true
	server := host.server
	service := host.service
	started := host.started
	host.mu.Unlock()

	var shutdownErr error
	if started && server != nil {
		if err := server.Shutdown(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown workflow API: %w", err))
		}
	}
	if service != nil {
		if err := service.Shutdown(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown workflow scheduler: %w", err))
		}
	}
	shutdownErr = errors.Join(shutdownErr, host.closeStores())

	host.mu.Lock()
	host.shutdownErr = shutdownErr
	host.closed = true
	host.shuttingDown = false
	close(host.shutdownDone)
	host.mu.Unlock()
	return shutdownErr
}

func (host *Host) serve() {
	err := host.server.Serve(host.listener)
	host.mu.Lock()
	if !errors.Is(err, http.ErrServerClosed) {
		host.serveErr = fmt.Errorf("serve workflow API: %w", err)
	}
	close(host.serveDone)
	host.mu.Unlock()
}

func (host *Host) requestShutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil && host.logger != nil {
		host.logger.Printf("shutdown workflow host: %v", err)
	}
}

func (host *Host) closeStores() error {
	var closeErr error
	if host.database != nil {
		closeErr = errors.Join(closeErr, host.database.Close())
		host.database = nil
	}
	if host.runtimeLock != nil {
		closeErr = errors.Join(closeErr, host.runtimeLock.Close())
		host.runtimeLock = nil
	}
	return closeErr
}

// Run preserves the blocking CLI entrypoint while sharing the Host lifecycle
// used by embedded applications.
func Run(ctx context.Context, config Config, logger *log.Logger) error {
	if ctx == nil {
		return errors.New("workflow host context is nil")
	}
	host, err := New(config, logger)
	if err != nil {
		return err
	}
	if err := host.Start(ctx); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		return errors.Join(err, host.Shutdown(shutdownCtx))
	}
	return host.Wait()
}
