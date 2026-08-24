// Package persist defines the storage bundle consumed by the workflow facade.
// Concrete database adapters live in subpackages and core workflow packages do
// not select or open a database implicitly.
package persist

import (
	"errors"
	"sync"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
)

// Stores is the complete persistence contract required by a fully featured
// scheduler, including automations and credentials. A Stores value must not be
// copied after first use; constructors return a pointer for this reason.
type Stores struct {
	Definitions definition.Repository
	Workspace   definition.WorkspaceRepository
	Runtime     wfruntime.Store
	Automations scheduler.AutomationStore
	Credentials credential.Store
	close       func() error
	closeOnce   sync.Once
	closeErr    error
}

// NewStores constructs a validated storage bundle. A nil component is rejected
// so advanced composition cannot accidentally fall back to an in-memory store.
func NewStores(
	definitions definition.Repository,
	workspace definition.WorkspaceRepository,
	runtimeStore wfruntime.Store,
	automations scheduler.AutomationStore,
	credentials credential.Store,
	close func() error,
) (*Stores, error) {
	stores := &Stores{
		Definitions: definitions,
		Workspace:   workspace,
		Runtime:     runtimeStore,
		Automations: automations,
		Credentials: credentials,
		close:       close,
	}
	if err := stores.Validate(); err != nil {
		return nil, err
	}
	return stores, nil
}

func (s *Stores) Validate() error {
	if s == nil {
		return errors.New("workflow stores are required")
	}
	switch {
	case s.Definitions == nil:
		return errors.New("workflow definition repository is required")
	case s.Workspace == nil:
		return errors.New("workflow workspace repository is required")
	case s.Runtime == nil:
		return errors.New("workflow runtime store is required")
	case s.Automations == nil:
		return errors.New("workflow automation store is required")
	case s.Credentials == nil:
		return errors.New("workflow credential store is required")
	default:
		return nil
	}
}

func (s *Stores) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.close != nil {
			s.closeErr = s.close()
		}
	})
	return s.closeErr
}
