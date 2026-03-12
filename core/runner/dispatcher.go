package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

type DefaultDispatcher struct{}

func NewDefaultDispatcher() *DefaultDispatcher {
	return &DefaultDispatcher{}
}

func (d *DefaultDispatcher) Dispatch(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun) []string {
	if plan == nil || run == nil {
		return nil
	}
	ready := make([]string, 0)
	for _, nodeID := range plan.TopologicalOrder {
		nodeRun := run.NodeRuns[nodeID]
		if nodeRun == nil || nodeRun.Status != wfruntime.StatusPending {
			continue
		}
		if depsDone(plan, run, nodeID) {
			ready = append(ready, nodeID)
		}
	}
	sort.Strings(ready)
	return ready
}

func depsDone(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun, nodeID string) bool {
	deps := plan.Dependencies[nodeID]
	branchRequired := false
	branchActivated := false
	for _, dep := range deps {
		depRun := run.NodeRuns[dep]
		if depRun == nil {
			return false
		}
		activated, conditional, err := edgeActivated(plan, run, dep, nodeID)
		if err != nil {
			return false
		}
		if conditional {
			branchRequired = true
			if activated {
				branchActivated = true
			}
			continue
		}
		switch depRun.Status {
		case wfruntime.StatusSuccess:
			continue
		case wfruntime.StatusFailed, wfruntime.StatusTimeout:
			if depNode, ok := plan.Nodes[dep]; ok && depNode.ContinueOnError {
				continue
			}
			return false
		default:
			return false
		}
	}
	if branchRequired && !branchActivated {
		return false
	}
	return true
}

func shouldSkipNode(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun, nodeID string) bool {
	deps := plan.Dependencies[nodeID]
	if len(deps) == 0 {
		return false
	}
	branchRequired := false
	branchActivated := false
	allResolved := true
	for _, dep := range deps {
		depRun := run.NodeRuns[dep]
		if depRun == nil || !wfruntime.IsTerminal(depRun.Status) {
			allResolved = false
			continue
		}
		activated, conditional, err := edgeActivated(plan, run, dep, nodeID)
		if err != nil {
			return false
		}
		if conditional {
			branchRequired = true
			if activated {
				branchActivated = true
			}
		}
	}
	return allResolved && branchRequired && !branchActivated
}

type branchEvalEnv struct {
	Run        map[string]any
	Output     any
	NodeID     string
	WorkflowID string
}

func edgeActivated(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun, fromID, toID string) (bool, bool, error) {
	if plan == nil || run == nil {
		return false, false, nil
	}
	meta, ok := plan.Branches[fromID]
	if !ok {
		return true, false, nil
	}
	conditional := false
	matched := false
	for _, edge := range meta.Edges {
		if edge.To != toID {
			continue
		}
		if strings.TrimSpace(edge.Condition) == "" {
			return true, false, nil
		}
		conditional = true
	}
	if !conditional {
		return true, false, nil
	}

	activeTargets, err := activatedTargets(meta, run, fromID)
	if err != nil {
		return false, true, err
	}
	for _, target := range activeTargets {
		if target == toID {
			matched = true
			break
		}
	}
	return matched, true, nil
}

func activatedTargets(meta planning.BranchMeta, run *wfruntime.WorkflowRun, fromID string) ([]string, error) {
	nodeRun := run.NodeRuns[fromID]
	if nodeRun == nil {
		return nil, fmt.Errorf("branch source node %s not found", fromID)
	}
	selected := make([]string, 0, len(meta.Edges))
	for _, edge := range meta.Edges {
		condition := strings.TrimSpace(edge.Condition)
		passed := condition == ""
		if condition != "" {
			ok, err := evalBranchCondition(condition, run, fromID, nodeRun.Result)
			if err != nil {
				return nil, err
			}
			passed = ok
		}
		if !passed {
			continue
		}
		selected = append(selected, edge.To)
		if meta.Mode == "first" {
			break
		}
	}
	return selected, nil
}

func evalBranchCondition(condition string, run *wfruntime.WorkflowRun, nodeID string, output any) (bool, error) {
	program, err := expr.Compile(condition, expr.Env(branchEvalEnv{}), expr.AsBool())
	if err != nil {
		return false, err
	}
	result, err := expr.Run(program, branchEvalEnv{
		Run: map[string]any{
			"variables":    copyMap(run.Context.Variables),
			"node_results": copyMap(run.Context.NodeResults),
		},
		Output:     output,
		NodeID:     nodeID,
		WorkflowID: run.WorkflowID,
	})
	if err != nil {
		return false, err
	}
	passed, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("branch condition did not return bool")
	}
	return passed, nil
}
