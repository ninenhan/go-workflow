// Package memory provides a complete non-persistent workflow storage bundle.
package memory

import (
	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/persist"
	"github.com/ninenhan/go-workflow/scheduler"
)

func New() (*persist.Stores, error) {
	return persist.NewStores(
		definition.NewMemoryRepository(),
		definition.NewMemoryWorkspaceRepository(),
		wfruntime.NewMemoryStore(),
		scheduler.NewMemoryAutomationStore(),
		credential.NewMemoryStore(),
		nil,
	)
}
