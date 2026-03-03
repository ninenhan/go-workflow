package units

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/expr-lang/expr"

	core "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/fn"
)

type WhileUnit struct {
	core.Unit
}

var _ core.ExecutableUnit = (*WhileUnit)(nil)

func (u *WhileUnit) GetUnitName() string {
	return reflect.TypeOf(WhileUnit{}).Name()
}

type whileParams struct {
	Condition   string                     `json:"condition"`
	Max         int                        `json:"max"`
	Body        *core.WorkflowDefinition   `json:"body"`
	Start       []string                   `json:"start,omitempty"`
	ResultNode  string                     `json:"result_node,omitempty"`
	Concurrency int                        `json:"concurrency,omitempty"`
	FailFast    *bool                      `json:"fail_fast,omitempty"`
}

type loopEnv struct {
	State map[string]*core.ExecutionResult
	Iter  int
	Node  string
}

func (u *WhileUnit) Execute(ctx context.Context, state core.ContextMap, self *core.Node) (*core.ExecutionResult, error) {
	if self == nil {
		return nil, errors.New("WhileUnit: missing node")
	}
	params, err := fn.ConvertByJSON[any, whileParams](self.Params)
	if err != nil {
		return nil, fmt.Errorf("WhileUnit: invalid params: %w", err)
	}
	if params.Body == nil {
		return nil, errors.New("WhileUnit: body is required")
	}
	cond := strings.TrimSpace(params.Condition)
	if cond == "" {
		cond = "true"
	}
	prog, err := expr.Compile(cond, expr.Env(loopEnv{}), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("WhileUnit: condition compile failed: %w", err)
	}
	max := params.Max
	if max <= 0 {
		max = 1000
	}

	engine := core.NewEngine()
	engine.Registry = core.DefaultRegistry
	if params.Concurrency > 0 {
		engine.Concurrency = params.Concurrency
	}
	if params.FailFast != nil {
		engine.FailFast = *params.FailFast
	}

	var loopState map[string]*core.ExecutionResult
	var lastResult *core.ExecutionResult
	var iterations int
	baseStart := params.Start

	for iter := 0; iter < max; iter++ {
		merged := mergeState(state, loopState)
		out, err := expr.Run(prog, loopEnv{
			State: merged,
			Iter:  iter,
			Node:  self.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("WhileUnit: condition eval failed: %w", err)
		}
		condOk, ok := out.(bool)
		if !ok || !condOk {
			break
		}

		runStart := baseStart

		seedState := map[string]*core.ExecutionResult{}
		seedExports := map[string][]string{}
		bodyNodes := params.Body.Nodes
		if bodyNodes == nil {
			bodyNodes = map[string]*core.NodeSpec{}
		}
		if _, exists := bodyNodes["loop"]; !exists {
			seedState["loop"] = &core.ExecutionResult{
				Data: map[string]any{
					"iter": iter,
				},
			}
			seedExports["loop"] = []string{"iter"}
		}

		subState, runErr := engine.Run(ctx, params.Body, &core.RunOptions{
			Start:         runStart,
			Concurrency:   params.Concurrency,
			FailFast:      params.FailFast,
			StopOnControl: true,
			SeedState:     seedState,
			SeedExports:   seedExports,
		})

		if subState != nil {
			loopState = extractResults(subState)
			if params.ResultNode != "" {
				if res := loopState[params.ResultNode]; res != nil {
					lastResult = res
				}
			}
		}
		iterations++

		if runErr != nil {
			if ce, ok := runErr.(*core.ControlSignalError); ok {
				switch ce.Signal {
				case core.ControlBreak:
					return finalizeWhile(iterations, lastResult), nil
				case core.ControlContinue:
					continue
				case core.ControlGoto:
					continue
				default:
					return nil, runErr
				}
			}
			return nil, runErr
		}

	}

	return finalizeWhile(iterations, lastResult), nil
}

func (u *WhileUnit) GetUnitMeta() *core.Unit {
	return &u.Unit
}

func finalizeWhile(iterations int, last *core.ExecutionResult) *core.ExecutionResult {
	if last != nil {
		return last
	}
	return &core.ExecutionResult{
		Data: map[string]any{
			"iterations": iterations,
		},
	}
}

func mergeState(outer core.ContextMap, inner map[string]*core.ExecutionResult) map[string]*core.ExecutionResult {
	merged := make(map[string]*core.ExecutionResult)
	for k, v := range outer {
		merged[k] = v
	}
	for k, v := range inner {
		merged[k] = v
	}
	return merged
}

func extractResults(state *core.ExecutionState) map[string]*core.ExecutionResult {
	if state == nil {
		return nil
	}
	out := make(map[string]*core.ExecutionResult, len(state.Nodes))
	for id, node := range state.Nodes {
		if node == nil || node.Result == nil {
			continue
		}
		out[id] = node.Result
	}
	return out
}

func init() {
	unit := &WhileUnit{}
	core.RegisterUnit(unit.GetUnitName(), unit)
}
