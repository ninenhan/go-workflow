package localdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/glebarez/sqlite"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Database struct {
	Definitions *definition.GormRepository
	Workspace   *definition.GormWorkspaceRepository
	Runtime     *wfruntime.GormStore
	Automations *scheduler.GormAutomationStore
	db          *gorm.DB
}

type Options struct {
	TablePrefix string
}

func Open(path string) (*Database, error) {
	return OpenWithOptions(path, Options{})
}

func OpenWithOptions(path string, opts Options) (*Database, error) {
	absolutePath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("resolve runtime database path: %w", err)
	}
	if filepath.Clean(path) == "." || path == "" {
		return nil, errors.New("runtime database path is required")
	}
	if err := prepareDatabaseFile(absolutePath); err != nil {
		return nil, err
	}

	dsn := (&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(absolutePath),
	}).String()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open runtime database: %w", err)
	}
	closeOnError := func(cause error) (*Database, error) {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
		return nil, cause
	}
	sqlDB, err := db.DB()
	if err != nil {
		return closeOnError(fmt.Errorf("access runtime database: %w", err))
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA locking_mode = EXCLUSIVE",
	} {
		if err := db.Exec(statement).Error; err != nil {
			return closeOnError(fmt.Errorf("configure runtime database: %w", err))
		}
	}
	if err := db.Exec("BEGIN EXCLUSIVE").Error; err != nil {
		return closeOnError(fmt.Errorf("lock runtime database: %w", err))
	}
	if err := db.Exec("COMMIT").Error; err != nil {
		return closeOnError(fmt.Errorf("confirm runtime database lock: %w", err))
	}
	var integrity string
	if err := db.Raw("PRAGMA quick_check").Scan(&integrity).Error; err != nil {
		return closeOnError(fmt.Errorf("check runtime database integrity: %w", err))
	}
	if integrity != "ok" {
		return closeOnError(fmt.Errorf("runtime database integrity check failed: %s", integrity))
	}

	definitions, err := definition.NewGormRepositoryWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return closeOnError(err)
	}
	workspace, err := definition.NewGormWorkspaceRepositoryWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return closeOnError(err)
	}
	runtimeStore, err := wfruntime.NewGormStoreWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return closeOnError(err)
	}
	if _, err := runtimeStore.FailInterruptedRuns(context.Background()); err != nil {
		return closeOnError(err)
	}
	automationStore, err := scheduler.NewGormAutomationStoreWithTablePrefix(db, opts.TablePrefix)
	if err != nil {
		return closeOnError(err)
	}
	return &Database{
		Definitions: definitions,
		Workspace:   workspace,
		Runtime:     runtimeStore,
		Automations: automationStore,
		db:          db,
	}, nil
}

func (d *Database) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func prepareDatabaseFile(path string) error {
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	switch {
	case err == nil:
		if !directoryInfo.IsDir() {
			return errors.New("runtime data directory must be a directory")
		}
		if runtime.GOOS != "windows" && directoryInfo.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("runtime data directory permissions must not allow group or world access: %s", directoryInfo.Mode().Perm())
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create runtime data directory: %w", err)
		}
	default:
		return fmt.Errorf("inspect runtime data directory: %w", err)
	}

	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return errors.New("runtime database must be a regular file")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("runtime database permissions must not allow group or world access: %s", info.Mode().Perm())
		}
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("inspect runtime database: %w", err)
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create runtime database: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync runtime database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close runtime database: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("secure runtime database: %w", err)
		}
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync runtime data directory: %w", err)
	}
	complete = true
	return nil
}

func syncDirectory(directory string) error {
	// Windows protects LocalAppData with the user's inherited ACL and does not
	// support syncing a directory handle through os.File.
	if runtime.GOOS == "windows" {
		return nil
	}
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}
