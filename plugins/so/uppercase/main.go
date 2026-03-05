package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	workflow "github.com/ninenhan/go-workflow"
)

const unitName = "UppercaseUnitSO"
const pluginName = "plugins/so/uppercase"

type UppercaseUnitSO struct {
	workflow.Unit
}

func (u *UppercaseUnitSO) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *UppercaseUnitSO) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("%s: missing input", unitName)
	}
	slog.Info("WF_SO_PLUGIN_EXECUTE",
		"plugin", pluginName,
		"unit", unitName,
		"node_id", self.ID,
		"node_name", self.Name,
	)
	return &workflow.ExecutionResult{
		NodeName: u.UnitName,
		Data:     strings.ToUpper(fmt.Sprint(self.Input.Data)),
	}, nil
}

// WorkflowRegister is the default symbol searched by LoadUnitPlugins.
func WorkflowRegister() error {
	slog.Info("WF_SO_PLUGIN_REGISTER",
		"plugin", pluginName,
		"unit", unitName,
	)
	workflow.RegisterUnitFactory(unitName, func() workflow.ExecutableUnit {
		u := &UppercaseUnitSO{}
		u.UnitName = unitName
		return u
	})
	return nil
}
