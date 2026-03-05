package uppercase

import (
	"context"
	"fmt"
	"strings"

	workflow "github.com/ninenhan/go-workflow"
)

const unitName = "UppercaseUnit"

// UppercaseUnit is an example plugin unit for auto registration.
type UppercaseUnit struct {
	workflow.Unit
}

var _ workflow.ExecutableUnit = (*UppercaseUnit)(nil)

func (u *UppercaseUnit) GetUnitName() string {
	return unitName
}

func (u *UppercaseUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *UppercaseUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("%s: missing input", unitName)
	}

	input := ""
	switch v := self.Input.Data.(type) {
	case string:
		input = v
	default:
		input = fmt.Sprint(v)
	}

	return &workflow.ExecutionResult{
		NodeName: u.UnitName,
		Data:     strings.ToUpper(input),
	}, nil
}

func init() {
	workflow.RegisterUnitFactory(unitName, func() workflow.ExecutableUnit {
		u := &UppercaseUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
