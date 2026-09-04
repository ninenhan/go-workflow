// Package gormstore composes the workflow persistence interfaces over a
// caller-owned Gorm database. Database drivers and connection ownership remain
// the responsibility of the host application.
package gormstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/persist"
	"github.com/ninenhan/go-workflow/scheduler"
	"gorm.io/gorm"
)

type RecoveryMode string

const (
	// RecoveryDisabled is safe for shared databases because it does not mutate
	// runs that may be owned by another live process.
	RecoveryDisabled RecoveryMode = "disabled"
	// RecoveryFailInterrupted is only safe when this process is the exclusive
	// scheduler owner for the database.
	RecoveryFailInterrupted RecoveryMode = "fail_interrupted"
)

type Options struct {
	Credentials credential.Store
	Recovery    RecoveryMode
	// TablePrefix is prepended to every workflow table. Leave empty to retain
	// the default table names.
	TablePrefix string
	// Close transfers connection ownership to the returned bundle when set.
	// Leave nil when the host application owns the connection pool.
	Close func() error
}

// New creates all Gorm-backed stores. The supplied *gorm.DB remains caller-owned
// unless Options.Close explicitly transfers cleanup to the returned bundle.
// Constructors currently run Gorm AutoMigrate; production deployments should
// review migrations before startup.
func New(ctx context.Context, db *gorm.DB, opts Options) (*persist.Stores, error) {
	if db == nil {
		return nil, errors.New("gorm database is required")
	}
	if opts.Credentials == nil {
		return nil, errors.New("credential store is required")
	}
	if opts.Recovery == "" {
		opts.Recovery = RecoveryDisabled
	}
	if opts.Recovery != RecoveryDisabled && opts.Recovery != RecoveryFailInterrupted {
		return nil, fmt.Errorf("unsupported Gorm recovery mode %q", opts.Recovery)
	}

	definitions, err := definition.NewGormRepositoryWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return nil, err
	}
	workspace, err := definition.NewGormWorkspaceRepositoryWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return nil, err
	}
	runtimeStore, err := wfruntime.NewGormStoreWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return nil, err
	}
	automations, err := scheduler.NewGormAutomationStoreWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return nil, err
	}
	if opts.Recovery == RecoveryFailInterrupted {
		if _, err := runtimeStore.FailInterruptedRuns(ctx); err != nil {
			return nil, err
		}
	}
	return persist.NewStores(definitions, workspace, runtimeStore, automations, opts.Credentials, opts.Close)
}
