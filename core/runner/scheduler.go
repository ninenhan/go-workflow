package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/planning"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/fn"
)

type DefaultScheduler struct {
	ExecutorDispatcher executor.Dispatcher
	NodeDispatcher     NodeDispatcher
	Store              wfruntime.Store
	Sink               EventSink
	ResultReporter     ResultReporter
	HeartbeatReporter  HeartbeatReporter
	RunController      RunController
}

const (
	eachLoopItemCountMetadataKey  = "_each_item_count"
	eachLoopOutputsMetadataKey    = "_each_outputs"
	loopGroupItemCountMetadataKey = "_loop_group_item_count"
	loopGroupOutputsMetadataKey   = "_loop_group_outputs"
	loopGroupIterationMetadataKey = "_loop_group_iteration"
	resolvedLoopCountMetadataKey  = "_resolved_loop_count"
)

func NewDefaultScheduler(executors *executor.Registry, store wfruntime.Store) *DefaultScheduler {
	execDispatcher := executor.NewRegistryDispatcher(executors)
	return &DefaultScheduler{
		ExecutorDispatcher: execDispatcher,
		NodeDispatcher:     NewDefaultDispatcher(),
		Store:              store,
		ResultReporter:     &NopResultReporter{},
		HeartbeatReporter:  &NopHeartbeatReporter{},
	}
}

// PrepareRun materializes the stable runtime identity and pending node records
// before execution starts. Async callers can persist this state and return the
// run ID without racing the scheduler goroutine.
func PrepareRun(plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun) *wfruntime.WorkflowRun {
	if run == nil {
		run = wfruntime.NewWorkflowRun(NewRunID(), plan.WorkflowID, plan.WorkflowVersionID, plan.PlanID)
	} else {
		if run.ID == "" {
			run.ID = NewRunID()
		}
		if run.WorkflowID == "" {
			run.WorkflowID = plan.WorkflowID
		}
		if run.WorkflowVersionID == "" {
			run.WorkflowVersionID = plan.WorkflowVersionID
		}
		if run.PlanID == "" {
			run.PlanID = plan.PlanID
		}
		if run.Context.Variables == nil {
			run.Context.Variables = map[string]any{}
		}
		if run.Context.NodeResults == nil {
			run.Context.NodeResults = map[string]any{}
		}
		if run.Status == "" {
			run.Status = wfruntime.StatusPending
		}
		now := time.Now()
		if run.CreatedAt.IsZero() {
			run.CreatedAt = now
		}
		if run.UpdatedAt.IsZero() {
			run.UpdatedAt = now
		}
	}
	if run.NodeRuns == nil {
		run.NodeRuns = make(map[string]*wfruntime.NodeRun, len(plan.Nodes))
	}
	for id, node := range plan.Nodes {
		if run.NodeRuns[id] != nil {
			continue
		}
		maxAttempts := node.Retry.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 1
		}
		run.NodeRuns[id] = &wfruntime.NodeRun{
			NodeID:      id,
			Status:      wfruntime.StatusPending,
			MaxAttempts: maxAttempts,
		}
	}
	return run
}

func (s *DefaultScheduler) Run(ctx context.Context, plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun) (*wfruntime.WorkflowRun, error) {
	if plan == nil {
		return nil, errors.New("execution plan is nil")
	}
	if s == nil {
		return nil, errors.New("scheduler is nil")
	}
	if s.ExecutorDispatcher == nil {
		return nil, errors.New("executor dispatcher is nil")
	}
	if s.NodeDispatcher == nil {
		s.NodeDispatcher = NewDefaultDispatcher()
	}
	if s.ResultReporter == nil {
		s.ResultReporter = &NopResultReporter{}
	}
	if s.HeartbeatReporter == nil {
		s.HeartbeatReporter = &NopHeartbeatReporter{}
	}

	run = PrepareRun(plan, run)

	now := time.Now()
	run.Status = wfruntime.StatusRunning
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.UpdatedAt = now
	s.saveRun(ctx, run)
	s.emit(ctx, run, wfruntime.EventRunStarted, "", wfruntime.StatusRunning, "")

	for {
		if stopped := s.applyRunCommand(ctx, run); stopped {
			return run, nil
		}
		if err := ctx.Err(); err != nil {
			s.cancelRun(context.WithoutCancel(ctx), run, err)
			return run, err
		}

		ready := s.NodeDispatcher.Dispatch(plan, run)
		if len(ready) == 0 {
			if pruned := s.pruneSkippedNodes(ctx, plan, run); pruned {
				continue
			}
			if allNodesTerminal(run) {
				run.FinishedAt = time.Now()
				run.UpdatedAt = run.FinishedAt
				run.CurrentNodes = nil
				if hasNodeFailed(run) {
					run.Status = wfruntime.StatusFailed
				} else {
					run.Status = wfruntime.StatusSuccess
				}
				s.saveRun(ctx, run)
				s.emit(ctx, run, wfruntime.EventRunFinished, "", run.Status, "")
				return run, nil
			}

			blocked := firstBlockedNode(run)
			err := fmt.Errorf("no runnable nodes, blocked on unresolved dependencies: %s", blocked)
			run.Status = wfruntime.StatusFailed
			run.CurrentNodes = nil
			run.UpdatedAt = time.Now()
			run.FinishedAt = run.UpdatedAt
			s.saveRun(ctx, run)
			s.emit(ctx, run, wfruntime.EventRunFinished, blocked, run.Status, err.Error())
			return run, err
		}

		for _, nodeID := range ready {
			nodePlan := plan.Nodes[nodeID]
			nodeRun := run.NodeRuns[nodeID]
			nodeRun.Attempt++
			nodeRun.StartedAt = time.Now()
			nodeRun.Status = wfruntime.StatusRunning
			run.CurrentNodes = []string{nodeID}
			run.UpdatedAt = time.Now()
			s.saveRun(ctx, run)
			s.emit(ctx, run, wfruntime.EventNodeRunning, nodeID, nodeRun.Status, "")

			task, emptyEach, err := buildExecuteTask(run, plan, nodePlan, nodeRun)
			if err != nil {
				s.handleNodeError(ctx, run, nodePlan, nodeRun, err)
				continue
			}
			if emptyEach {
				if completed, completeErr := s.completeEmptyLoopGroup(ctx, plan, run, nodeID, task.Input); completeErr != nil {
					s.handleNodeError(ctx, run, nodePlan, nodeRun, completeErr)
					continue
				} else if completed {
					continue
				}
				nodeRun.Input = task.Input
				nodeRun.Status = wfruntime.StatusSuccess
				nodeRun.Error = ""
				nodeRun.Result = []any{}
				nodeRun.Metadata = mergeMap(nodeRun.Metadata, map[string]any{"loop_iteration": 0})
				delete(nodeRun.Metadata, eachLoopItemCountMetadataKey)
				delete(nodeRun.Metadata, eachLoopOutputsMetadataKey)
				nodeRun.FinishedAt = time.Now()
				run.Context.NodeResults[nodeID] = []any{}
				run.Context.Variables[nodeID] = []any{}
				run.UpdatedAt = nodeRun.FinishedAt
				s.saveRun(ctx, run)
				s.emit(ctx, run, wfruntime.EventNodeDone, nodeID, nodeRun.Status, "empty each-item input")
				continue
			}
			// Persist the fully resolved task input before execution so the latest
			// run payload can render per-node input/output in the editor.
			nodeRun.Input = task.Input
			execImpl, err := s.ExecutorDispatcher.Dispatch(task)
			if err != nil {
				s.handleNodeError(ctx, run, nodePlan, nodeRun, err)
				continue
			}

			execCtx := ctx
			var cancel context.CancelFunc
			if task.Timeout > 0 {
				execCtx, cancel = context.WithTimeout(ctx, task.Timeout)
			}
			result, err := execImpl.Execute(execCtx, task)
			if cancel != nil {
				cancel()
			}
			if err != nil {
				if contextErr := ctx.Err(); contextErr != nil {
					s.cancelRun(context.WithoutCancel(ctx), run, contextErr)
					return run, contextErr
				}
				s.handleNodeError(ctx, run, nodePlan, nodeRun, err)
				continue
			}
			_ = s.ResultReporter.ReportResult(ctx, task, result)

			status := result.NormalizedStatus()
			if status == executor.StatusAccepted || status == executor.StatusRunning {
				asyncResult, asyncErr := s.waitAsyncResult(ctx, execImpl, task, result)
				if asyncErr != nil {
					if errors.Is(asyncErr, ErrRunPaused) || errors.Is(asyncErr, ErrRunCancelled) {
						if stopped := s.applyRunCommand(ctx, run); stopped {
							return run, nil
						}
					}
					s.handleNodeError(ctx, run, nodePlan, nodeRun, asyncErr)
					continue
				}
				result = asyncResult
				status = result.NormalizedStatus()
				_ = s.ResultReporter.ReportResult(ctx, task, result)
			}

			switch status {
			case executor.StatusSucceeded:
				eachOutputs, appendErr := appendEachLoopOutput(nodePlan, nodeRun, result.Output)
				if appendErr != nil {
					s.handleNodeError(ctx, run, nodePlan, nodeRun, appendErr)
					continue
				}
				run.Context.NodeResults[nodeID] = result.Output
				run.Context.Variables[nodeID] = result.Output
				applyResultVariables(run, result)
				if continued, iteration, err := shouldContinueLoop(nodePlan, nodeRun, run, result.Output); err != nil {
					s.handleNodeError(ctx, run, nodePlan, nodeRun, err)
					continue
				} else if continued {
					nodeRun.Status = wfruntime.StatusPending
					nodeRun.Error = ""
					nodeRun.Result = result.Output
					nodeRun.Metadata = mergeMap(nodeRun.Metadata, result.Metadata)
					nodeRun.Metadata["loop_iteration"] = iteration
					nodeRun.FinishedAt = time.Now()
					run.UpdatedAt = nodeRun.FinishedAt
					s.saveRun(ctx, run)
					s.emit(ctx, run, wfruntime.EventNodeLoop, nodeID, nodeRun.Status, fmt.Sprintf("loop iteration %d", iteration))
					continue
				}
				finalOutput := result.Output
				groupOutput, groupContinued, grouped, groupIterations, groupErr := s.advanceLoopGroup(ctx, plan, run, nodeID, result.Output)
				if groupErr != nil {
					s.handleNodeError(ctx, run, nodePlan, nodeRun, groupErr)
					continue
				}
				if groupContinued {
					continue
				}
				if grouped {
					finalOutput = groupOutput
					run.Context.NodeResults[nodeID] = finalOutput
					run.Context.Variables[nodeID] = finalOutput
				}
				if loopMode(nodePlan.Loop) == "each" {
					finalOutput = append([]any(nil), eachOutputs...)
					run.Context.NodeResults[nodeID] = finalOutput
					run.Context.Variables[nodeID] = finalOutput
				}
				nodeRun.Status = wfruntime.StatusSuccess
				nodeRun.Error = ""
				nodeRun.Result = finalOutput
				nodeRun.Metadata = mergeMap(nodeRun.Metadata, result.Metadata)
				delete(nodeRun.Metadata, eachLoopItemCountMetadataKey)
				delete(nodeRun.Metadata, eachLoopOutputsMetadataKey)
				if grouped {
					nodeRun.Metadata["loop_iteration"] = groupIterations
				} else {
					nodeRun.Metadata["loop_iteration"] = loopIteration(nodeRun) + 1
				}
				nodeRun.FinishedAt = time.Now()
				run.UpdatedAt = nodeRun.FinishedAt
				if looped, err := s.applyBackEdges(ctx, plan, run, nodeID); err != nil {
					s.handleNodeError(ctx, run, nodePlan, nodeRun, err)
					continue
				} else if looped {
					continue
				}
				s.saveRun(ctx, run)
				s.emit(ctx, run, wfruntime.EventNodeDone, nodeID, nodeRun.Status, "")
			case executor.StatusRetryable:
				s.handleNodeFailure(
					ctx,
					run,
					nodePlan,
					nodeRun,
					resultError(result, "executor requested retry"),
					true,
					result.RetryAfter,
				)
			case executor.StatusFailed:
				s.handleNodeFailure(
					ctx,
					run,
					nodePlan,
					nodeRun,
					resultError(result, "executor failed"),
					false,
					0,
				)
			default:
				s.handleNodeError(ctx, run, nodePlan, nodeRun, fmt.Errorf("unsupported executor status: %s", status))
			}
		}
	}
}

func applyResultVariables(run *wfruntime.WorkflowRun, result executor.ExecuteResult) {
	if run == nil {
		return
	}
	if run.Context.Variables == nil {
		run.Context.Variables = map[string]any{}
	}
	for _, key := range result.DeleteVariables {
		if key != "" {
			delete(run.Context.Variables, key)
		}
	}
	for key, value := range result.Variables {
		if key != "" {
			run.Context.Variables[key] = value
		}
	}
}

func (s *DefaultScheduler) pruneSkippedNodes(ctx context.Context, plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun) bool {
	if plan == nil || run == nil {
		return false
	}
	pruned := false
	for _, nodeID := range plan.TopologicalOrder {
		nodeRun := run.NodeRuns[nodeID]
		if nodeRun == nil || nodeRun.Status != wfruntime.StatusPending {
			continue
		}
		if !shouldSkipNode(plan, run, nodeID) {
			continue
		}
		nodeRun.Status = wfruntime.StatusSuccess
		nodeRun.Error = ""
		nodeRun.Result = nil
		nodeRun.Metadata = mergeMap(nodeRun.Metadata, map[string]any{
			"skipped":        true,
			"skip_reason":    "branch_not_selected",
			"loop_iteration": loopIteration(nodeRun),
		})
		nodeRun.FinishedAt = time.Now()
		run.Context.NodeResults[nodeID] = nil
		run.Context.Variables[nodeID] = nil
		run.UpdatedAt = nodeRun.FinishedAt
		s.saveRun(ctx, run)
		s.emit(ctx, run, wfruntime.EventNodeDone, nodeID, nodeRun.Status, "node skipped by branch condition")
		pruned = true
	}
	return pruned
}

type loopEnv struct {
	Run        map[string]any
	Output     any
	Iteration  int
	NodeID     string
	WorkflowID string
}

func shouldContinueLoop(node planning.PlanNode, nodeRun *wfruntime.NodeRun, run *wfruntime.WorkflowRun, output any) (bool, int, error) {
	if node.Loop == nil || nodeRun == nil || run == nil {
		return false, 0, nil
	}
	currentIteration := loopIteration(nodeRun) + 1
	if loopMode(node.Loop) == "each" {
		itemCount, ok := metadataPositiveInt(nodeRun.Metadata, eachLoopItemCountMetadataKey)
		if !ok {
			return false, currentIteration, fmt.Errorf("each-item loop for node %s is missing its item count", node.ID)
		}
		return currentIteration < itemCount, currentIteration, nil
	}
	maxIterations := node.Loop.MaxIterations
	if node.Loop.CountBinding != nil {
		var ok bool
		maxIterations, ok = metadataPositiveInt(nodeRun.Metadata, resolvedLoopCountMetadataKey)
		if !ok {
			return false, currentIteration, fmt.Errorf("node %s loop is missing its resolved count", node.ID)
		}
	}
	if maxIterations <= 1 {
		return false, currentIteration, nil
	}
	if currentIteration >= maxIterations {
		return false, currentIteration, nil
	}
	condition := strings.TrimSpace(node.Loop.Condition)
	if condition == "" {
		return true, currentIteration, nil
	}
	program, err := expr.Compile(condition, expr.Env(loopEnv{}), expr.AsBool())
	if err != nil {
		return false, currentIteration, fmt.Errorf("compile loop condition for node %s: %w", node.ID, err)
	}
	result, err := expr.Run(program, loopEnv{
		Run: map[string]any{
			"variables":    copyMap(run.Context.Variables),
			"node_results": copyMap(run.Context.NodeResults),
		},
		Output:     output,
		Iteration:  currentIteration,
		NodeID:     node.ID,
		WorkflowID: run.WorkflowID,
	})
	if err != nil {
		return false, currentIteration, fmt.Errorf("evaluate loop condition for node %s: %w", node.ID, err)
	}
	continued, ok := result.(bool)
	if !ok {
		return false, currentIteration, fmt.Errorf("loop condition for node %s did not return bool", node.ID)
	}
	return continued, currentIteration, nil
}

func loopMode(loop *planning.LoopPolicy) string {
	if loop == nil {
		return ""
	}
	mode := strings.TrimSpace(loop.Mode)
	if mode == "" {
		return "count"
	}
	return mode
}

func ensureResolvedLoopCount(
	run *wfruntime.WorkflowRun,
	plan *planning.ExecutionPlan,
	nodeID string,
	nodeRun *wfruntime.NodeRun,
	binding planning.InputBinding,
	maxIterations int,
	label string,
) (int, error) {
	if nodeRun == nil {
		return 0, fmt.Errorf("%s cannot resolve its count without a node run", label)
	}
	if count, ok := metadataPositiveInt(nodeRun.Metadata, resolvedLoopCountMetadataKey); ok {
		if count > maxIterations {
			return 0, fmt.Errorf("%s resolved count %d exceeds maximum %d", label, count, maxIterations)
		}
		return count, nil
	}
	value, found, err := resolveBindingValue(run, plan, nodeID, binding)
	if err != nil {
		return 0, fmt.Errorf("%s count: %w", label, err)
	}
	if !found {
		return 0, fmt.Errorf("%s count is missing", label)
	}
	count, ok := wholeNumber(value)
	if !ok || count < 1 || count > maxIterations {
		return 0, fmt.Errorf(
			"%s count must be a whole number between 1 and %d, got %v",
			label,
			maxIterations,
			value,
		)
	}
	if nodeRun.Metadata == nil {
		nodeRun.Metadata = map[string]any{}
	}
	nodeRun.Metadata[resolvedLoopCountMetadataKey] = count
	return count, nil
}

func wholeNumber(value any) (int, bool) {
	if number, ok := value.(json.Number); ok {
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil || parsed < 0 || parsed > int64(math.MaxInt) {
			return 0, false
		}
		return int(parsed), true
	}
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil || parsed < 0 || parsed > int64(math.MaxInt) {
			return 0, false
		}
		return int(parsed), true
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return 0, false
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number := reflected.Int()
		if number < 0 || number > int64(math.MaxInt) {
			return 0, false
		}
		return int(number), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number := reflected.Uint()
		if number > uint64(math.MaxInt) {
			return 0, false
		}
		return int(number), true
	case reflect.Float32, reflect.Float64:
		number := reflected.Float()
		if math.IsNaN(number) ||
			math.IsInf(number, 0) ||
			number != math.Trunc(number) ||
			number < 0 ||
			number > float64(math.MaxInt) {
			return 0, false
		}
		return int(number), true
	default:
		return 0, false
	}
}

func metadataPositiveInt(metadata map[string]any, key string) (int, bool) {
	if len(metadata) == 0 {
		return 0, false
	}
	switch value := metadata[key].(type) {
	case int:
		return value, value > 0
	case int64:
		return int(value), value > 0
	case float64:
		return int(value), value > 0 && value == float64(int(value))
	default:
		return 0, false
	}
}

func appendEachLoopOutput(node planning.PlanNode, nodeRun *wfruntime.NodeRun, output any) ([]any, error) {
	if loopMode(node.Loop) != "each" {
		return nil, nil
	}
	if nodeRun.Metadata == nil {
		nodeRun.Metadata = map[string]any{}
	}
	var outputs []any
	if raw, exists := nodeRun.Metadata[eachLoopOutputsMetadataKey]; exists {
		var ok bool
		outputs, ok = raw.([]any)
		if !ok {
			return nil, fmt.Errorf("each-item loop for node %s has invalid stored outputs", node.ID)
		}
	}
	outputs = append(outputs, output)
	nodeRun.Metadata[eachLoopOutputsMetadataKey] = outputs
	return outputs, nil
}

func loopIteration(nodeRun *wfruntime.NodeRun) int {
	if nodeRun == nil || len(nodeRun.Metadata) == 0 {
		return 0
	}
	switch value := nodeRun.Metadata["loop_iteration"].(type) {
	case int:
		if value > 0 {
			return value
		}
	case int64:
		if value > 0 {
			return int(value)
		}
	case float64:
		if value > 0 {
			return int(value)
		}
	}
	return 0
}

func (s *DefaultScheduler) applyBackEdges(ctx context.Context, plan *planning.ExecutionPlan, run *wfruntime.WorkflowRun, sourceID string) (bool, error) {
	if plan == nil || run == nil {
		return false, nil
	}
	meta, ok := plan.BackEdges[sourceID]
	if !ok || len(meta.Edges) == 0 {
		return false, nil
	}
	targets, err := activatedTargets(meta, run, sourceID)
	if err != nil {
		return false, err
	}
	if len(targets) == 0 {
		return false, nil
	}
	scope := backEdgeScope(plan, targets, sourceID)
	if len(scope) == 0 {
		return false, nil
	}
	for _, nodeID := range scope {
		nodeRun := run.NodeRuns[nodeID]
		if nodeRun == nil {
			continue
		}
		nodeRun.Status = wfruntime.StatusPending
		nodeRun.Error = ""
		nodeRun.Result = nil
		nodeRun.FinishedAt = time.Time{}
	}
	run.CurrentNodes = nil
	run.UpdatedAt = time.Now()
	s.saveRun(ctx, run)
	s.emit(ctx, run, wfruntime.EventNodeLoop, sourceID, wfruntime.StatusPending, fmt.Sprintf("back edge to %s", strings.Join(targets, ",")))
	return true, nil
}

func backEdgeScope(plan *planning.ExecutionPlan, targets []string, sourceID string) []string {
	if plan == nil || len(targets) == 0 {
		return nil
	}
	ancestors := reverseReachable(plan.Dependencies, sourceID)
	seen := make(map[string]struct{})
	scope := make([]string, 0)
	for _, target := range targets {
		reachable := forwardReachable(plan.Adjacency, target)
		for nodeID := range reachable {
			if _, ok := ancestors[nodeID]; !ok {
				continue
			}
			if _, ok := seen[nodeID]; ok {
				continue
			}
			seen[nodeID] = struct{}{}
			scope = append(scope, nodeID)
		}
	}
	sort.Slice(scope, func(i, j int) bool {
		left := topoIndex(plan.TopologicalOrder, scope[i])
		right := topoIndex(plan.TopologicalOrder, scope[j])
		if left != right {
			return left < right
		}
		return scope[i] < scope[j]
	})
	return scope
}

func forwardReachable(adjacency map[string][]string, start string) map[string]struct{} {
	seen := map[string]struct{}{}
	var visit func(string)
	visit = func(nodeID string) {
		if _, ok := seen[nodeID]; ok {
			return
		}
		seen[nodeID] = struct{}{}
		for _, next := range adjacency[nodeID] {
			visit(next)
		}
	}
	visit(start)
	return seen
}

func reverseReachable(deps map[string][]string, start string) map[string]struct{} {
	seen := map[string]struct{}{}
	var visit func(string)
	visit = func(nodeID string) {
		if _, ok := seen[nodeID]; ok {
			return
		}
		seen[nodeID] = struct{}{}
		for _, prev := range deps[nodeID] {
			visit(prev)
		}
	}
	visit(start)
	return seen
}

func topoIndex(order []string, nodeID string) int {
	for idx, current := range order {
		if current == nodeID {
			return idx
		}
	}
	return len(order) + 1
}

func buildExecuteTask(run *wfruntime.WorkflowRun, plan *planning.ExecutionPlan, node planning.PlanNode, nodeRun *wfruntime.NodeRun) (executor.ExecuteTask, bool, error) {
	input, err := resolveNodeInput(run, plan, node)
	if err != nil {
		return executor.ExecuteTask{}, false, err
	}
	group, grouped := loopGroupStartingAt(plan, node.ID)
	if grouped && group.Mode == "count" && group.CountBinding != nil {
		if _, err := ensureResolvedLoopCount(
			run,
			plan,
			node.ID,
			nodeRun,
			*group.CountBinding,
			group.MaxIterations,
			fmt.Sprintf("loop group %s", group.ID),
		); err != nil {
			return executor.ExecuteTask{}, false, err
		}
	} else if !grouped && loopMode(node.Loop) == "count" && node.Loop.CountBinding != nil {
		if _, err := ensureResolvedLoopCount(
			run,
			plan,
			node.ID,
			nodeRun,
			*node.Loop.CountBinding,
			node.Loop.MaxIterations,
			fmt.Sprintf("node %s loop", node.ID),
		); err != nil {
			return executor.ExecuteTask{}, false, err
		}
	}
	var emptyEach bool
	if grouped {
		input, emptyEach, err = prepareLoopGroupInput(group, nodeRun, input)
	} else {
		input, emptyEach, err = prepareEachLoopInput(node, nodeRun, input)
	}
	if err != nil {
		return executor.ExecuteTask{}, false, err
	}
	if emptyEach {
		return executor.ExecuteTask{Input: input}, true, nil
	}
	if grouped {
		applyLoopGroupVariables(run, group, input, loopGroupIteration(nodeRun)+1)
	}
	params, err := resolveNodeParams(run, plan, node)
	if err != nil {
		return executor.ExecuteTask{}, false, err
	}
	params["__executor_ref"] = node.ExecutorRef
	pollInterval := durationFromMap(params, "poll_interval", time.Second)
	hbFreq := durationFromMap(params, "heartbeat_interval", 2*time.Second)
	async := boolFromMap(params, "async")
	return executor.ExecuteTask{
		RunID:           run.ID,
		NodeID:          node.ID,
		ExecutorType:    node.ExecutorType,
		ExecutorRef:     node.ExecutorRef,
		CredentialScope: run.CredentialScope,
		Attempt:         nodeRun.Attempt,
		MaxAttempts:     nodeRun.MaxAttempts,
		Input:           input,
		Params:          params,
		Context:         copyMap(run.Context.Variables),
		Timeout:         node.Timeout,
		Deadline:        time.Now().Add(node.Timeout),
		Async:           async,
		PollInterval:    pollInterval,
		HeartbeatFreq:   hbFreq,
	}, false, nil
}

func prepareEachLoopInput(node planning.PlanNode, nodeRun *wfruntime.NodeRun, input any) (any, bool, error) {
	if loopMode(node.Loop) != "each" {
		return input, false, nil
	}
	items, err := eachLoopItems(input)
	if err != nil {
		return nil, false, fmt.Errorf("each-item loop for node %s: %w", node.ID, err)
	}
	if len(items) > node.Loop.MaxIterations {
		return nil, false, fmt.Errorf("each-item loop for node %s received %d items; maximum is %d", node.ID, len(items), node.Loop.MaxIterations)
	}
	if len(items) == 0 {
		return input, true, nil
	}
	if nodeRun.Metadata == nil {
		nodeRun.Metadata = map[string]any{}
	}
	nodeRun.Metadata[eachLoopItemCountMetadataKey] = len(items)
	index := loopIteration(nodeRun)
	if index < 0 || index >= len(items) {
		return nil, false, fmt.Errorf("each-item loop for node %s has invalid item index %d", node.ID, index)
	}
	return items[index], false, nil
}

func eachLoopItems(input any) ([]any, error) {
	value := reflect.ValueOf(input)
	if !value.IsValid() || (value.Kind() != reflect.Slice && value.Kind() != reflect.Array) {
		return nil, fmt.Errorf("input must be a list, got %T", input)
	}
	items := make([]any, value.Len())
	for index := range items {
		items[index] = value.Index(index).Interface()
	}
	return items, nil
}

func loopGroupStartingAt(plan *planning.ExecutionPlan, nodeID string) (planning.LoopGroup, bool) {
	if plan == nil {
		return planning.LoopGroup{}, false
	}
	for _, group := range plan.LoopGroups {
		if group.Start == nodeID {
			return group, true
		}
	}
	return planning.LoopGroup{}, false
}

func loopGroupEndingAt(plan *planning.ExecutionPlan, nodeID string) (planning.LoopGroup, bool) {
	if plan == nil {
		return planning.LoopGroup{}, false
	}
	for _, group := range plan.LoopGroups {
		if group.End == nodeID {
			return group, true
		}
	}
	return planning.LoopGroup{}, false
}

func loopGroupIteration(nodeRun *wfruntime.NodeRun) int {
	if nodeRun == nil || len(nodeRun.Metadata) == 0 {
		return 0
	}
	value, ok := metadataNonNegativeInt(nodeRun.Metadata, loopGroupIterationMetadataKey)
	if !ok {
		return 0
	}
	return value
}

func metadataNonNegativeInt(metadata map[string]any, key string) (int, bool) {
	if len(metadata) == 0 {
		return 0, false
	}
	switch value := metadata[key].(type) {
	case int:
		return value, value >= 0
	case int64:
		return int(value), value >= 0
	case float64:
		return int(value), value >= 0 && value == float64(int(value))
	default:
		return 0, false
	}
}

func loopGroupOutputs(nodeRun *wfruntime.NodeRun) ([]any, error) {
	if nodeRun == nil || len(nodeRun.Metadata) == 0 {
		return nil, nil
	}
	raw, exists := nodeRun.Metadata[loopGroupOutputsMetadataKey]
	if !exists {
		return nil, nil
	}
	outputs, ok := raw.([]any)
	if !ok {
		return nil, errors.New("loop group has invalid stored outputs")
	}
	return outputs, nil
}

func prepareLoopGroupInput(group planning.LoopGroup, nodeRun *wfruntime.NodeRun, input any) (any, bool, error) {
	if group.Mode != "each" {
		return input, false, nil
	}
	items, err := eachLoopItems(input)
	if err != nil {
		return nil, false, fmt.Errorf("loop group %s: %w", group.ID, err)
	}
	if len(items) > group.MaxIterations {
		return nil, false, fmt.Errorf("loop group %s received %d items; maximum is %d", group.ID, len(items), group.MaxIterations)
	}
	if len(items) == 0 {
		return input, true, nil
	}
	if nodeRun.Metadata == nil {
		nodeRun.Metadata = map[string]any{}
	}
	nodeRun.Metadata[loopGroupItemCountMetadataKey] = len(items)
	index := loopGroupIteration(nodeRun)
	if index >= len(items) {
		return nil, false, fmt.Errorf("loop group %s has invalid item index %d", group.ID, index)
	}
	return items[index], false, nil
}

func applyLoopGroupVariables(run *wfruntime.WorkflowRun, group planning.LoopGroup, input any, index int) {
	if run.Context.Variables == nil {
		run.Context.Variables = map[string]any{}
	}
	run.Context.Variables[planning.LoopGroupIndexVariable(group.ID)] = index
	itemKey := planning.LoopGroupItemVariable(group.ID)
	if group.Mode == "each" {
		run.Context.Variables[itemKey] = input
		return
	}
	delete(run.Context.Variables, itemKey)
}

func clearLoopGroupVariables(run *wfruntime.WorkflowRun, group planning.LoopGroup) {
	delete(run.Context.Variables, planning.LoopGroupItemVariable(group.ID))
	delete(run.Context.Variables, planning.LoopGroupIndexVariable(group.ID))
}

func (s *DefaultScheduler) completeEmptyLoopGroup(
	ctx context.Context,
	plan *planning.ExecutionPlan,
	run *wfruntime.WorkflowRun,
	startID string,
	input any,
) (bool, error) {
	group, ok := loopGroupStartingAt(plan, startID)
	if !ok || group.Mode != "each" {
		return false, nil
	}
	clearLoopGroupVariables(run, group)
	now := time.Now()
	for _, nodeID := range group.Scope {
		nodeRun := run.NodeRuns[nodeID]
		if nodeRun == nil {
			return false, fmt.Errorf("loop group %s node run is missing: %s", group.ID, nodeID)
		}
		nodeRun.Status = wfruntime.StatusSuccess
		nodeRun.Error = ""
		nodeRun.Input = nil
		nodeRun.Result = nil
		nodeRun.Metadata = map[string]any{
			"skipped":        true,
			"skip_reason":    "empty_loop_input",
			"loop_iteration": 0,
		}
		nodeRun.FinishedAt = now
		delete(run.Context.NodeResults, nodeID)
		delete(run.Context.Variables, nodeID)
	}
	startRun := run.NodeRuns[group.Start]
	startRun.Input = input
	endRun := run.NodeRuns[group.End]
	endRun.Result = []any{}
	endRun.Metadata = map[string]any{"loop_iteration": 0}
	run.Context.NodeResults[group.End] = []any{}
	run.Context.Variables[group.End] = []any{}
	run.CurrentNodes = nil
	run.UpdatedAt = now
	s.saveRun(ctx, run)
	for _, nodeID := range group.Scope {
		s.emit(ctx, run, wfruntime.EventNodeDone, nodeID, wfruntime.StatusSuccess, "empty loop group input")
	}
	return true, nil
}

func (s *DefaultScheduler) advanceLoopGroup(
	ctx context.Context,
	plan *planning.ExecutionPlan,
	run *wfruntime.WorkflowRun,
	endID string,
	output any,
) (any, bool, bool, int, error) {
	group, ok := loopGroupEndingAt(plan, endID)
	if !ok {
		return nil, false, false, 0, nil
	}
	startRun := run.NodeRuns[group.Start]
	if startRun == nil {
		return nil, false, true, 0, fmt.Errorf("loop group %s start node run is missing", group.ID)
	}
	outputs, err := loopGroupOutputs(startRun)
	if err != nil {
		return nil, false, true, 0, fmt.Errorf("loop group %s: %w", group.ID, err)
	}
	outputs = append(outputs, output)
	if startRun.Metadata == nil {
		startRun.Metadata = map[string]any{}
	}
	startRun.Metadata[loopGroupOutputsMetadataKey] = outputs
	completed := loopGroupIteration(startRun) + 1
	total := group.MaxIterations
	if group.Mode == "each" {
		var found bool
		total, found = metadataPositiveInt(startRun.Metadata, loopGroupItemCountMetadataKey)
		if !found {
			return nil, false, true, 0, fmt.Errorf("loop group %s is missing its item count", group.ID)
		}
	} else if group.CountBinding != nil {
		var found bool
		total, found = metadataPositiveInt(startRun.Metadata, resolvedLoopCountMetadataKey)
		if !found {
			return nil, false, true, 0, fmt.Errorf("loop group %s is missing its resolved count", group.ID)
		}
	}
	if completed < total {
		state := map[string]any{
			loopGroupIterationMetadataKey: completed,
			loopGroupOutputsMetadataKey:   outputs,
		}
		if group.Mode == "each" {
			state[loopGroupItemCountMetadataKey] = total
		}
		if group.CountBinding != nil {
			state[resolvedLoopCountMetadataKey] = total
		}
		for _, nodeID := range group.Scope {
			nodeRun := run.NodeRuns[nodeID]
			if nodeRun == nil {
				return nil, false, true, 0, fmt.Errorf("loop group %s node run is missing: %s", group.ID, nodeID)
			}
			nodeRun.Status = wfruntime.StatusPending
			nodeRun.Attempt = 0
			nodeRun.StartedAt = time.Time{}
			nodeRun.FinishedAt = time.Time{}
			nodeRun.Input = nil
			nodeRun.Result = nil
			nodeRun.Error = ""
			nodeRun.Metadata = nil
			delete(run.Context.NodeResults, nodeID)
			delete(run.Context.Variables, nodeID)
		}
		startRun.Metadata = state
		run.CurrentNodes = nil
		run.UpdatedAt = time.Now()
		s.saveRun(ctx, run)
		s.emit(ctx, run, wfruntime.EventNodeLoop, endID, wfruntime.StatusPending, fmt.Sprintf("loop group %s iteration %d", group.ID, completed))
		return nil, true, true, completed, nil
	}
	delete(startRun.Metadata, loopGroupIterationMetadataKey)
	delete(startRun.Metadata, loopGroupOutputsMetadataKey)
	delete(startRun.Metadata, loopGroupItemCountMetadataKey)
	clearLoopGroupVariables(run, group)
	return append([]any(nil), outputs...), false, true, completed, nil
}

func resolveNodeParams(run *wfruntime.WorkflowRun, plan *planning.ExecutionPlan, node planning.PlanNode) (map[string]any, error) {
	params := copyMap(node.Params)
	if len(node.ParamBindings) == 0 && len(node.ParamTemplates) == 0 {
		return params, nil
	}
	templateKeys := make([]string, 0, len(node.ParamTemplates))
	for key := range node.ParamTemplates {
		templateKeys = append(templateKeys, key)
	}
	sort.Strings(templateKeys)
	for _, key := range templateKeys {
		value, err := resolveParamTemplate(run, plan, node.ID, node.ParamTemplates[key])
		if err != nil {
			return nil, fmt.Errorf("node %s parameter template %s: %w", node.ID, key, err)
		}
		params[key] = value
	}
	keys := make([]string, 0, len(node.ParamBindings))
	for key := range node.ParamBindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, ok, err := resolveBindingValue(run, plan, node.ID, node.ParamBindings[key])
		if err != nil {
			return nil, fmt.Errorf("node %s parameter %s: %w", node.ID, key, err)
		}
		if ok {
			params[key] = value
		}
	}
	return params, nil
}

func resolveParamTemplate(run *wfruntime.WorkflowRun, plan *planning.ExecutionPlan, nodeID string, template planning.ParamTemplate) (string, error) {
	var builder strings.Builder
	for index, segment := range template.Segments {
		switch segment.Type {
		case "text":
			builder.WriteString(segment.Value)
		case "binding":
			if segment.Binding == nil {
				return "", fmt.Errorf("binding segment %d is missing its binding", index)
			}
			value, ok, err := resolveBindingValue(run, plan, nodeID, *segment.Binding)
			if err != nil {
				return "", fmt.Errorf("resolve segment %d: %w", index, err)
			}
			if !ok {
				continue
			}
			text, err := paramTemplateString(value)
			if err != nil {
				return "", fmt.Errorf("encode segment %d: %w", index, err)
			}
			builder.WriteString(text)
		default:
			return "", fmt.Errorf("segment %d has unsupported type %q", index, segment.Type)
		}
	}
	return builder.String(), nil
}

func paramTemplateString(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(typed), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func resolveNodeInput(run *wfruntime.WorkflowRun, plan *planning.ExecutionPlan, node planning.PlanNode) (any, error) {
	if node.InputSpec == nil || len(node.InputSpec.Bindings) == 0 {
		return node.Input, nil
	}
	mode := strings.TrimSpace(node.InputSpec.Mode)
	if mode == "" {
		if node.Input == nil && len(node.InputSpec.Bindings) == 1 {
			mode = "replace"
		} else {
			mode = "object"
		}
	}

	resolved := make([]resolvedInputBinding, 0, len(node.InputSpec.Bindings))
	for _, binding := range node.InputSpec.Bindings {
		value, ok, err := resolveBindingValue(run, plan, node.ID, binding)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		resolved = append(resolved, resolvedInputBinding{Binding: binding, Value: value})
	}

	switch mode {
	case "replace":
		if len(resolved) == 0 {
			return node.Input, nil
		}
		if len(resolved) > 1 {
			return nil, fmt.Errorf("node %s input mode replace expects exactly one resolved binding", node.ID)
		}
		return resolved[0].Value, nil
	case "object":
		base, err := normalizeObjectInput(node.ID, node.Input)
		if err != nil {
			return nil, err
		}
		for _, item := range resolved {
			key := strings.TrimSpace(item.Binding.As)
			if key == "" {
				key = item.Binding.From
			}
			base[key] = item.Value
		}
		return base, nil
	case "array":
		base := normalizeArrayInput(node.Input)
		for _, item := range resolved {
			base = append(base, item.Value)
		}
		return base, nil
	default:
		return nil, fmt.Errorf("node %s has unsupported input mode: %s", node.ID, mode)
	}
}

type resolvedInputBinding struct {
	Binding planning.InputBinding
	Value   any
}

func resolveBindingValue(run *wfruntime.WorkflowRun, plan *planning.ExecutionPlan, nodeID string, binding planning.InputBinding) (any, bool, error) {
	if run == nil {
		return nil, false, fmt.Errorf("node %s input binding resolution requires a run", nodeID)
	}
	sourceType := strings.TrimSpace(binding.Source)
	if sourceType == "" {
		sourceType = "node"
	}
	source, ok, err := bindingSourceValue(run, plan, nodeID, sourceType, binding)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		if binding.Default != nil {
			return applyBindingTransform(binding, binding.Default, nil, run, nodeID)
		}
		if binding.Required {
			return nil, false, fmt.Errorf("node %s required input from %s:%s is missing", nodeID, sourceType, binding.From)
		}
		return nil, false, nil
	}
	value, found, err := lookupInputPath(source, binding.Path)
	if err != nil {
		return nil, false, fmt.Errorf("node %s input binding %s.%s: %w", nodeID, binding.From, binding.Path, err)
	}
	if !found {
		if binding.Default != nil {
			return applyBindingTransform(binding, binding.Default, source, run, nodeID)
		}
		if binding.Required {
			return nil, false, fmt.Errorf("node %s required input path %s.%s is missing", nodeID, binding.From, binding.Path)
		}
		return nil, false, nil
	}
	return applyBindingTransform(binding, value, source, run, nodeID)
}

func bindingSourceValue(run *wfruntime.WorkflowRun, plan *planning.ExecutionPlan, nodeID, sourceType string, binding planning.InputBinding) (any, bool, error) {
	switch sourceType {
	case "node":
		if plan != nil {
			valid := false
			for _, dep := range plan.Dependencies[nodeID] {
				if dep == binding.From {
					valid = true
					break
				}
			}
			if !valid {
				return nil, false, fmt.Errorf("node %s input binding source %s is not a direct dependency", nodeID, binding.From)
			}
		}
		value, ok := run.Context.NodeResults[binding.From]
		return value, ok, nil
	case "var":
		value, ok := run.Context.Variables[binding.From]
		return value, ok, nil
	case "request":
		value, ok := run.Context.Variables["request"]
		if !ok {
			return nil, false, nil
		}
		if binding.From == "" {
			return value, true, nil
		}
		next, found, err := lookupInputPath(value, binding.From)
		return next, found, err
	case "run":
		scope := map[string]any{
			"id":                  run.ID,
			"workflow_id":         run.WorkflowID,
			"workflow_version_id": run.WorkflowVersionID,
			"plan_id":             run.PlanID,
			"status":              string(run.Status),
			"current_nodes":       append([]string{}, run.CurrentNodes...),
			"variables":           copyMap(run.Context.Variables),
			"node_results":        copyMap(run.Context.NodeResults),
		}
		if binding.From == "" {
			return scope, true, nil
		}
		next, found, err := lookupInputPath(scope, binding.From)
		return next, found, err
	default:
		return nil, false, fmt.Errorf("node %s has unsupported input source: %s", nodeID, sourceType)
	}
}

func normalizeObjectInput(nodeID string, input any) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	base, ok := input.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("node %s input mode object requires static input to be map[string]any", nodeID)
	}
	return copyMap(base), nil
}

func normalizeArrayInput(input any) []any {
	if input == nil {
		return []any{}
	}
	if items, ok := input.([]any); ok {
		out := make([]any, len(items))
		copy(out, items)
		return out
	}
	return []any{input}
}

func lookupInputPath(value any, path string) (any, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return value, true, nil
	}
	parts := strings.Split(path, ".")
	current := value
	for _, part := range parts {
		var ok bool
		current, ok = nextPathValue(current, part)
		if !ok {
			return nil, false, nil
		}
	}
	return current, true, nil
}

func nextPathValue(value any, segment string) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		next, ok := current[segment]
		return next, ok
	case []any:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(current) {
			return nil, false
		}
		return current[index], true
	}

	rv := reflect.ValueOf(value)
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil, false
	}

	switch rv.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(segment)
		mv := rv.MapIndex(key)
		if !mv.IsValid() {
			return nil, false
		}
		return mv.Interface(), true
	case reflect.Struct:
		field := rv.FieldByName(segment)
		if field.IsValid() && field.CanInterface() {
			return field.Interface(), true
		}
	case reflect.Slice, reflect.Array:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= rv.Len() {
			return nil, false
		}
		return rv.Index(index).Interface(), true
	}
	return nil, false
}

func applyBindingTransform(binding planning.InputBinding, value any, source any, run *wfruntime.WorkflowRun, nodeID string) (any, bool, error) {
	transform := strings.TrimSpace(binding.Transform)
	if transform == "" {
		return value, true, nil
	}
	env := map[string]any{
		"Value":  value,
		"Source": source,
		"Run": map[string]any{
			"variables":    copyMap(run.Context.Variables),
			"node_results": copyMap(run.Context.NodeResults),
		},
		"NodeID": nodeID,
		"From":   binding.From,
	}
	program, err := expr.Compile(transform, expr.Env(env))
	if err != nil {
		return nil, false, fmt.Errorf("compile transform for node %s binding from %s: %w", nodeID, binding.From, err)
	}
	result, err := expr.Run(program, env)
	if err != nil {
		return nil, false, fmt.Errorf("evaluate transform for node %s binding from %s: %w", nodeID, binding.From, err)
	}
	return result, true, nil
}

func (s *DefaultScheduler) waitAsyncResult(ctx context.Context, execImpl executor.Executor, task executor.ExecuteTask, first executor.ExecuteResult) (executor.ExecuteResult, error) {
	asyncExec, ok := execImpl.(executor.AsyncExecutor)
	if !ok {
		return executor.ExecuteResult{}, fmt.Errorf("executor %s returned async status but does not implement AsyncExecutor", execImpl.Type())
	}
	externalTaskID := first.ExternalTaskID
	if externalTaskID == "" {
		externalTaskID, _ = first.Metadata["external_task_id"].(string)
	}
	if externalTaskID == "" {
		return executor.ExecuteResult{}, fmt.Errorf("async result missing external_task_id")
	}

	pollEvery := task.PollInterval
	if pollEvery <= 0 {
		pollEvery = time.Second
	}
	hbEvery := task.HeartbeatFreq
	if hbEvery <= 0 {
		hbEvery = 2 * time.Second
	}
	nextHeartbeat := time.Now().Add(hbEvery)

	for {
		if s.RunController != nil {
			switch s.RunController.Get(task.RunID) {
			case RunCommandPause:
				_ = asyncExec.Cancel(context.Background(), task, externalTaskID)
				return executor.ExecuteResult{}, ErrRunPaused
			case RunCommandCancel:
				_ = asyncExec.Cancel(context.Background(), task, externalTaskID)
				return executor.ExecuteResult{}, ErrRunCancelled
			}
		}
		if ctx.Err() != nil {
			_ = asyncExec.Cancel(context.Background(), task, externalTaskID)
			return executor.ExecuteResult{}, ctx.Err()
		}
		if time.Now().After(nextHeartbeat) {
			_ = s.HeartbeatReporter.ReportHeartbeat(ctx, Heartbeat{
				RunID:          task.RunID,
				NodeID:         task.NodeID,
				ExecutorType:   task.ExecutorType,
				ExternalTaskID: externalTaskID,
				Status:         executor.StatusRunning,
				At:             time.Now(),
			})
			nextHeartbeat = time.Now().Add(hbEvery)
		}

		result, err := asyncExec.Poll(ctx, task, externalTaskID)
		if err != nil {
			return executor.ExecuteResult{}, err
		}
		status := result.NormalizedStatus()
		if status == executor.StatusSucceeded || status == executor.StatusFailed || status == executor.StatusRetryable {
			if result.ExternalTaskID == "" {
				result.ExternalTaskID = externalTaskID
			}
			return result, nil
		}
		if err := sleepWithContext(ctx, pollEvery); err != nil {
			return executor.ExecuteResult{}, err
		}
	}
}

func (s *DefaultScheduler) handleNodeError(ctx context.Context, run *wfruntime.WorkflowRun, nodePlan planning.PlanNode, nodeRun *wfruntime.NodeRun, err error) {
	s.handleNodeFailure(ctx, run, nodePlan, nodeRun, err, true, 0)
}

func (s *DefaultScheduler) handleNodeFailure(
	ctx context.Context,
	run *wfruntime.WorkflowRun,
	nodePlan planning.PlanNode,
	nodeRun *wfruntime.NodeRun,
	err error,
	retryable bool,
	retryAfter time.Duration,
) {
	nodeRun.Error = err.Error()
	nodeRun.FinishedAt = time.Now()

	if retryable && nodeRun.Attempt < nodeRun.MaxAttempts {
		nodeRun.Status = wfruntime.StatusRetry
		s.emit(ctx, run, wfruntime.EventNodeRetry, nodeRun.NodeID, nodeRun.Status, err.Error())
		backoff := retryBackoff(nodePlan.Retry, nodeRun.Attempt, retryAfter)
		if err := sleepWithContext(ctx, backoff); err != nil {
			nodeRun.Status = wfruntime.StatusCancelled
			nodeRun.Error = err.Error()
			run.UpdatedAt = time.Now()
			s.saveRun(ctx, run)
			s.emit(ctx, run, wfruntime.EventNodeFailed, nodeRun.NodeID, nodeRun.Status, err.Error())
			return
		}
		nodeRun.Status = wfruntime.StatusPending
		run.UpdatedAt = time.Now()
		s.saveRun(ctx, run)
		return
	}

	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		nodeRun.Status = wfruntime.StatusTimeout
	} else {
		nodeRun.Status = wfruntime.StatusFailed
	}
	run.UpdatedAt = time.Now()
	s.saveRun(ctx, run)
	s.emit(ctx, run, wfruntime.EventNodeFailed, nodeRun.NodeID, nodeRun.Status, err.Error())
}

func retryBackoff(policy planning.RetryPolicy, attempt int, requested time.Duration) time.Duration {
	backoff := policy.Backoff
	if backoff <= 0 {
		backoff = 100 * time.Millisecond
	}
	maxBackoff := policy.MaxBackoff
	if maxBackoff <= 0 || maxBackoff > planning.MaxNodeRetryBackoff {
		maxBackoff = planning.MaxNodeRetryBackoff
	}
	if attempt > 1 {
		for index := 1; index < attempt; index++ {
			if backoff >= maxBackoff || backoff > maxBackoff/2 {
				backoff = maxBackoff
				break
			}
			backoff *= 2
		}
	}
	if requested > backoff {
		backoff = requested
	}
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

func (s *DefaultScheduler) saveRun(ctx context.Context, run *wfruntime.WorkflowRun) {
	if s.Store == nil || run == nil {
		return
	}
	_ = s.Store.SaveRun(ctx, run)
	_ = s.Store.SaveSnapshot(ctx, &wfruntime.RunSnapshot{
		RunID:    run.ID,
		Status:   run.Status,
		NodeRuns: copyNodeRuns(run.NodeRuns),
		Context: wfruntime.RunContext{
			Variables:   copyMap(run.Context.Variables),
			NodeResults: copyMap(run.Context.NodeResults),
		},
		At: time.Now(),
	})
}

func (s *DefaultScheduler) cancelRun(ctx context.Context, run *wfruntime.WorkflowRun, cause error) {
	now := time.Now()
	for _, node := range run.NodeRuns {
		if node == nil || node.Status != wfruntime.StatusRunning {
			continue
		}
		node.Status = wfruntime.StatusCancelled
		node.FinishedAt = now
	}
	run.Status = wfruntime.StatusCancelled
	run.CurrentNodes = nil
	run.UpdatedAt = now
	run.FinishedAt = now
	s.saveRun(ctx, run)
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	s.emit(ctx, run, wfruntime.EventRunFinished, "", run.Status, message)
}

func (s *DefaultScheduler) emit(ctx context.Context, run *wfruntime.WorkflowRun, typ wfruntime.EventType, nodeID string, status wfruntime.Status, msg string) {
	event := wfruntime.RunEvent{
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		Type:       typ,
		NodeID:     nodeID,
		Status:     status,
		Message:    msg,
		Time:       time.Now(),
	}
	if s.Store != nil {
		_ = s.Store.AppendEvent(ctx, event)
	}
	if s.Sink != nil {
		s.Sink.Emit(ctx, event)
	}
}

func (s *DefaultScheduler) applyRunCommand(ctx context.Context, run *wfruntime.WorkflowRun) bool {
	if s == nil || s.RunController == nil || run == nil {
		return false
	}
	switch s.RunController.Get(run.ID) {
	case RunCommandPause:
		s.RunController.Clear(run.ID)
		for _, node := range run.NodeRuns {
			if node != nil && node.Status == wfruntime.StatusRunning {
				node.Status = wfruntime.StatusPending
			}
		}
		run.Status = wfruntime.StatusPaused
		run.UpdatedAt = time.Now()
		s.saveRun(ctx, run)
		s.emit(ctx, run, wfruntime.EventRunPaused, "", run.Status, "run paused")
		return true
	case RunCommandCancel:
		s.RunController.Clear(run.ID)
		run.Status = wfruntime.StatusCancelled
		run.UpdatedAt = time.Now()
		run.FinishedAt = run.UpdatedAt
		s.saveRun(ctx, run)
		s.emit(ctx, run, wfruntime.EventRunFinished, "", run.Status, "run cancelled")
		return true
	default:
		return false
	}
}

func allNodesTerminal(run *wfruntime.WorkflowRun) bool {
	for _, node := range run.NodeRuns {
		if node == nil {
			continue
		}
		if !wfruntime.IsTerminal(node.Status) {
			return false
		}
	}
	return true
}

func hasNodeFailed(run *wfruntime.WorkflowRun) bool {
	for _, node := range run.NodeRuns {
		if node == nil {
			continue
		}
		switch node.Status {
		case wfruntime.StatusFailed, wfruntime.StatusTimeout:
			return true
		}
	}
	return false
}

func firstBlockedNode(run *wfruntime.WorkflowRun) string {
	for id, node := range run.NodeRuns {
		if node != nil && node.Status == wfruntime.StatusPending {
			return id
		}
	}
	return ""
}

func NewRunID() string {
	if id, err := fn.GenerateShortID(); err == nil {
		return id
	}
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}

func copyNodeRuns(src map[string]*wfruntime.NodeRun) map[string]*wfruntime.NodeRun {
	if len(src) == 0 {
		return map[string]*wfruntime.NodeRun{}
	}
	dst := make(map[string]*wfruntime.NodeRun, len(src))
	for id, node := range src {
		if node == nil {
			dst[id] = nil
			continue
		}
		nc := *node
		nc.Metadata = copyMap(node.Metadata)
		dst[id] = &nc
	}
	return dst
}

func copyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeMap(base, delta map[string]any) map[string]any {
	out := copyMap(base)
	for k, v := range delta {
		out[k] = v
	}
	return out
}

func boolFromMap(src map[string]any, key string) bool {
	if len(src) == 0 {
		return false
	}
	v, ok := src[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func durationFromMap(src map[string]any, key string, fallback time.Duration) time.Duration {
	if len(src) == 0 {
		return fallback
	}
	v, ok := src[key]
	if !ok {
		return fallback
	}
	switch x := v.(type) {
	case time.Duration:
		if x > 0 {
			return x
		}
	case string:
		if d, err := time.ParseDuration(x); err == nil && d > 0 {
			return d
		}
	case int:
		if x > 0 {
			return time.Duration(x) * time.Millisecond
		}
	case int64:
		if x > 0 {
			return time.Duration(x) * time.Millisecond
		}
	case float64:
		if x > 0 {
			return time.Duration(x) * time.Millisecond
		}
	}
	return fallback
}

func resultError(res executor.ExecuteResult, fallback string) error {
	if strings.TrimSpace(res.Error) != "" {
		return errors.New(res.Error)
	}
	if fallback == "" {
		fallback = "executor failed"
	}
	return errors.New(fallback)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
