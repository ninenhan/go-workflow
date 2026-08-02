package units

import (
	"context"
	"fmt"
	"reflect"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// BranchJoinUnit is an internal control-flow unit used by structured branches.
// Inputs are keyed by branch name; the first non-nil value in BranchOrder wins.
type BranchJoinUnit struct {
	unit.Unit
	BranchOrder  []string          `json:"branch_order,omitempty"`
	BranchLabels map[string]string `json:"branch_labels,omitempty"`
}

var _ unit.ExecutableUnit = (*BranchJoinUnit)(nil)

func (u *BranchJoinUnit) GetUnitName() string {
	return reflect.TypeOf(BranchJoinUnit{}).Name()
}

func (u *BranchJoinUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if len(u.BranchOrder) == 0 {
		return nil, fmt.Errorf("branch join requires an explicit branch order")
	}
	if self == nil || self.Input == nil || self.Input.Data == nil {
		return &unit.ExecutionResult{NodeName: u.UnitName}, nil
	}
	inputs, ok := self.Input.Data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("branch join input must be an object, got %T", self.Input.Data)
	}
	if len(u.BranchLabels) > 0 {
		selected, ok := inputs["__selected"].(string)
		if !ok || selected == "" {
			return nil, fmt.Errorf("menu branch join requires the selected menu item")
		}
		for _, branch := range u.BranchOrder {
			if u.BranchLabels[branch] != selected {
				continue
			}
			return &unit.ExecutionResult{NodeName: u.UnitName, Data: inputs[branch]}, nil
		}
		return nil, fmt.Errorf("menu branch join has no branch for selection %q", selected)
	}
	for _, branch := range u.BranchOrder {
		if value, exists := inputs[branch]; exists && value != nil {
			return &unit.ExecutionResult{NodeName: u.UnitName, Data: value}, nil
		}
	}
	return &unit.ExecutionResult{NodeName: u.UnitName}, nil
}

func (u *BranchJoinUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func init() {
	unit.RegisterUnitFactory("BranchJoinUnit", func() unit.ExecutableUnit {
		u := &BranchJoinUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
