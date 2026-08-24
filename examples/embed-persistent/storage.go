package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	workflow "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/persist/gormstore"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const (
	databaseDriverEnvironment = "WORKFLOW_DATABASE_DRIVER"
	databaseDSNEnvironment    = "WORKFLOW_DATABASE_DSN"
)

func openWorkflowApplication(
	ctx context.Context,
	dataDirectory string,
	runtimeOptions workflow.RuntimeOptions,
) (*workflow.Application, error) {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv(databaseDriverEnvironment)))
	if driver == "" {
		driver = "memory"
	}
	switch driver {
	case "memory":
		return workflow.OpenMemoryWithOptions(ctx, runtimeOptions)
	case "sqlite":
		return workflow.OpenDefaultWithOptions(ctx, workflow.DefaultOptions{
			DataDirectory: dataDirectory,
			Runtime:       runtimeOptions,
		})
	case "mysql":
		return openMySQLApplication(ctx, dataDirectory, strings.TrimSpace(os.Getenv(databaseDSNEnvironment)), runtimeOptions)
	default:
		return nil, fmt.Errorf("unsupported workflow database driver %q; expected memory, sqlite, or mysql", driver)
	}
}

func openMySQLApplication(
	ctx context.Context,
	dataDirectory, dsn string,
	runtimeOptions workflow.RuntimeOptions,
) (*workflow.Application, error) {
	if dsn == "" {
		return nil, fmt.Errorf("%s is required when %s=mysql", databaseDSNEnvironment, databaseDriverEnvironment)
	}
	database, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open MySQL workflow database: %w", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("access MySQL workflow connection pool: %w", err)
	}
	closeOnError := func(cause error) (*workflow.Application, error) {
		return nil, errors.Join(cause, sqlDB.Close())
	}
	sqlDB.SetMaxOpenConns(32)
	sqlDB.SetMaxIdleConns(8)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping MySQL workflow database: %w", err))
	}

	credentials, err := credential.OpenFileStore(filepath.Join(dataDirectory, "credentials"))
	if err != nil {
		return closeOnError(err)
	}
	stores, err := gormstore.New(ctx, database, gormstore.Options{
		Credentials: credentials,
		Recovery:    gormstore.RecoveryDisabled,
		Close:       sqlDB.Close,
	})
	if err != nil {
		return closeOnError(err)
	}
	application, err := workflow.New(ctx, workflow.Options{Stores: stores, Runtime: runtimeOptions})
	if err != nil {
		return nil, errors.Join(err, stores.Close())
	}
	return application, nil
}
