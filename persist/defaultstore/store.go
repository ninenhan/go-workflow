// Package defaultstore provides the stable default ORM configuration: a
// single-process SQLite database plus an encrypted file credential store.
package defaultstore

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/persist"
	"github.com/ninenhan/go-workflow/persist/localdb"
)

const DatabaseFilename = "workflow.db"

type Config struct {
	DataDirectory string
}

func Open(config Config) (*persist.Stores, error) {
	dataDirectory := strings.TrimSpace(config.DataDirectory)
	if dataDirectory == "" {
		return nil, errors.New("default store data directory is required")
	}
	database, err := localdb.Open(filepath.Join(dataDirectory, DatabaseFilename))
	if err != nil {
		return nil, err
	}
	credentials, err := credential.OpenFileStore(filepath.Join(dataDirectory, "credentials"))
	if err != nil {
		return nil, errors.Join(err, database.Close())
	}
	stores, err := persist.NewStores(
		database.Definitions,
		database.Workspace,
		database.Runtime,
		database.Automations,
		credentials,
		database.Close,
	)
	if err != nil {
		return nil, errors.Join(err, database.Close())
	}
	return stores, nil
}
