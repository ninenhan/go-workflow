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
	if len(deps) == 0 {
		return true
	}
	hasActivePath := false
	for _, dep := range deps {
		depRun := run.NodeRuns[dep]
		if depRun == nil {
			return false
		}
		// A conditional edge cannot be classified as inactive until its source
		// has produced a terminal result.
		if !wfruntime.IsTerminal(depRun.Status) {
			return false
		}
		// A skipped predecessor never activates any outgoing edge. Handle it
		// before branch evaluation because a skipped branch source has no result.
		if nodeRunSkipped(depRun) {
			continue
		}
		activated, conditional, err := edgeActivated(plan, run, dep, nodeID)
		if err != nil {
			return false
		}
		if conditional && !activated {
			continue
		}
		switch depRun.Status {
		case wfruntime.StatusSuccess:
			if !nodeRunSkipped(depRun) {
				hasActivePath = true
			}
		case wfruntime.StatusFailed, wfruntime.StatusTimeout:
			if depNode, ok := plan.Nodes[dep]; ok && depNode.ContinueOnError {
				hasActivePath = true
				continue
			}
			return false
		default:
			return false
		}
	}
	return hasActivePath
}

func shouldSkipNode(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun, nodeID string) bool {
	deps := plan.Dependencies[nodeID]
	if len(deps) == 0 {
		return false
	}
	hasActivePath := false
	hasInactivePath := false
	allResolved := true
	for _, dep := range deps {
		depRun := run.NodeRuns[dep]
		if depRun == nil || !wfruntime.IsTerminal(depRun.Status) {
			allResolved = false
			continue
		}
		// Skipping a structured branch controller must propagate through its
		// descendants without evaluating conditions against a missing result.
		if nodeRunSkipped(depRun) {
			hasInactivePath = true
			continue
		}
		activated, conditional, err := edgeActivated(plan, run, dep, nodeID)
		if err != nil {
			return false
		}
		if conditional {
			if activated {
				hasActivePath = true
			} else {
				hasInactivePath = true
			}
			continue
		}
		hasActivePath = true
	}
	return allResolved && hasInactivePath && !hasActivePath
}

func nodeRunSkipped(run *wfruntime.NodeRun) bool {
	if run == nil || run.Metadata == nil {
		return false
	}
	skipped, _ := run.Metadata["skipped"].(bool)
	return skipped
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
