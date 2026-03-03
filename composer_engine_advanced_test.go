package workflow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow"
)

type slowUnit struct {
	workflow.Unit
	name string
}

func (u *slowUnit) GetUnitName() string { return u.name }
func (u *slowUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *slowUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	wait := 200 * time.Millisecond
	select {
	case <-time.After(wait):
		return &workflow.ExecutionResult{Data: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type failUnit struct {
	workflow.Unit
	name string
}

func (u *failUnit) GetUnitName() string { return u.name }
func (u *failUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *failUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	return nil, errors.New("boom")
}

type okUnit struct {
	workflow.Unit
	name string
}

func (u *okUnit) GetUnitName() string { return u.name }
func (u *okUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *okUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self != nil && self.Params != nil {
		if flag, ok := self.Params["flag"].(*atomic.Bool); ok {
			flag.Store(true)
		}
	}
	return &workflow.ExecutionResult{Data: "ok"}, nil
}

type retryUnit struct {
	workflow.Unit
	name string
}

func (u *retryUnit) GetUnitName() string { return u.name }
func (u *retryUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *retryUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self == nil || self.Params == nil {
		return nil, errors.New("missing params")
	}
	counter, _ := self.Params["counter"].(*int32)
	failUntil, _ := self.Params["fail_until"].(int32)
	if counter != nil {
		atomic.AddInt32(counter, 1)
		if atomic.LoadInt32(counter) <= failUntil {
			return nil, errors.New("retry please")
		}
	}
	return &workflow.ExecutionResult{Data: "ok"}, nil
}

type waitUnit struct {
	workflow.Unit
	name string
}

func (u *waitUnit) GetUnitName() string { return u.name }
func (u *waitUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *waitUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	if self != nil && self.Params != nil {
		if ch, ok := self.Params["wait"].(chan struct{}); ok && ch != nil {
			select {
			case <-ch:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return &workflow.ExecutionResult{Data: map[string]any{"ok": true}}, nil
}

type inspectUnit struct {
	workflow.Unit
	name string
}

func (u *inspectUnit) GetUnitName() string { return u.name }
func (u *inspectUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *inspectUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	_, hasA := state["a"]
	_, hasB := state["b"]
	if self != nil && self.Params != nil {
		if ch, ok := self.Params["result"].(chan [2]bool); ok && ch != nil {
			ch <- [2]bool{hasA, hasB}
		}
	}
	return &workflow.ExecutionResult{Data: map[string]any{"a": hasA, "b": hasB}}, nil
}

func TestWorkflow_Timeout(t *testing.T) {
	name := "SlowUnit_" + t.Name()
	workflow.RegisterUnitFactory(name, func() workflow.ExecutableUnit {
		u := &slowUnit{name: name}
		u.UnitName = u.GetUnitName()
		return u
	})

	def := &workflow.WorkflowDefinition{
		ID:    "wf-timeout",
		Start: []string{"slow"},
		Nodes: map[string]*workflow.NodeSpec{
			"slow": {
				Unit:    name,
				Options: &workflow.NodeOptions{Timeout: workflow.Duration{Duration: 50 * time.Millisecond}},
			},
		},
	}
	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if state.Nodes["slow"].Status != workflow.NodeFailed {
		t.Fatalf("unexpected node status: %s", state.Nodes["slow"].Status)
	}
	if state.Status != workflow.RunFailed {
		t.Fatalf("unexpected run status: %s", state.Status)
	}
}

func TestWorkflow_Retry(t *testing.T) {
	name := "RetryUnit_" + t.Name()
	workflow.RegisterUnitFactory(name, func() workflow.ExecutableUnit {
		u := &retryUnit{name: name}
		u.UnitName = u.GetUnitName()
		return u
	})

	var counter int32
	def := &workflow.WorkflowDefinition{
		ID:    "wf-retry",
		Start: []string{"retry"},
		Nodes: map[string]*workflow.NodeSpec{
			"retry": {
				Unit: name,
				Params: map[string]any{
					"counter":    &counter,
					"fail_until": int32(2),
				},
				Options: &workflow.NodeOptions{Retries: 2},
			},
		},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if state.Status != workflow.RunSucceeded {
		t.Fatalf("unexpected run status: %s", state.Status)
	}
	if atomic.LoadInt32(&counter) != 3 {
		t.Fatalf("unexpected retry count: %d", atomic.LoadInt32(&counter))
	}
}

func TestWorkflow_FailFast(t *testing.T) {
	failName := "FailUnit_" + t.Name()
	okName := "OkUnit_" + t.Name()
	workflow.RegisterUnitFactory(failName, func() workflow.ExecutableUnit {
		u := &failUnit{name: failName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(okName, func() workflow.ExecutableUnit {
		u := &okUnit{name: okName}
		u.UnitName = u.GetUnitName()
		return u
	})

	var ran atomic.Bool
	def := &workflow.WorkflowDefinition{
		ID:    "wf-failfast",
		Start: []string{"a", "b"},
		Nodes: map[string]*workflow.NodeSpec{
			"a": {Unit: failName},
			"b": {
				Unit: okName,
				Params: map[string]any{
					"flag": &ran,
				},
			},
		},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{
		Concurrency: 1,
		FailFast:    func() *bool { b := true; return &b }(),
	})
	if err == nil {
		t.Fatalf("expected failfast error")
	}
	if state.Nodes["a"].Status != workflow.NodeFailed {
		t.Fatalf("unexpected node a status: %s", state.Nodes["a"].Status)
	}
	if ran.Load() {
		t.Fatalf("node b should not run under failfast")
	}
	if !state.Nodes["b"].StartedAt.IsZero() {
		t.Fatalf("node b should not start under failfast")
	}
}

func TestWorkflow_JoinAny(t *testing.T) {
	waitName := "WaitUnit_" + t.Name()
	inspectName := "InspectUnit_" + t.Name()
	workflow.RegisterUnitFactory(waitName, func() workflow.ExecutableUnit {
		u := &waitUnit{name: waitName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(inspectName, func() workflow.ExecutableUnit {
		u := &inspectUnit{name: inspectName}
		u.UnitName = u.GetUnitName()
		return u
	})

	chA := make(chan struct{})
	chB := make(chan struct{})
	resultCh := make(chan [2]bool, 1)

	def := &workflow.WorkflowDefinition{
		ID:    "wf-join-any",
		Start: []string{"a", "b"},
		Nodes: map[string]*workflow.NodeSpec{
			"a": {Unit: waitName, Params: map[string]any{"wait": chA}},
			"b": {Unit: waitName, Params: map[string]any{"wait": chB}},
			"c": {
				Unit: inspectName,
				Params: map[string]any{
					"result": resultCh,
				},
				Options: &workflow.NodeOptions{Join: workflow.JoinAny},
			},
		},
		Edges: []workflow.EdgeSpec{
			{From: "a", To: "c"},
			{From: "b", To: "c"},
		},
	}

	engine := workflow.NewEngine()
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(context.Background(), def, &workflow.RunOptions{Concurrency: 2})
		done <- err
	}()

	close(chA)
	select {
	case res := <-resultCh:
		if !res[0] || res[1] {
			t.Fatalf("expected only a=true, b=false, got %+v", res)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for join-any result")
	}

	close(chB)
	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}
}

func TestWorkflow_JoinAll(t *testing.T) {
	waitName := "WaitUnitAll_" + t.Name()
	inspectName := "InspectUnitAll_" + t.Name()
	workflow.RegisterUnitFactory(waitName, func() workflow.ExecutableUnit {
		u := &waitUnit{name: waitName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(inspectName, func() workflow.ExecutableUnit {
		u := &inspectUnit{name: inspectName}
		u.UnitName = u.GetUnitName()
		return u
	})

	chA := make(chan struct{})
	chB := make(chan struct{})
	resultCh := make(chan [2]bool, 1)

	def := &workflow.WorkflowDefinition{
		ID:    "wf-join-all",
		Start: []string{"a", "b"},
		Nodes: map[string]*workflow.NodeSpec{
			"a": {Unit: waitName, Params: map[string]any{"wait": chA}},
			"b": {Unit: waitName, Params: map[string]any{"wait": chB}},
			"c": {
				Unit: inspectName,
				Params: map[string]any{
					"result": resultCh,
				},
				Options: &workflow.NodeOptions{Join: workflow.JoinAll},
			},
		},
		Edges: []workflow.EdgeSpec{
			{From: "a", To: "c"},
			{From: "b", To: "c"},
		},
	}

	engine := workflow.NewEngine()
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(context.Background(), def, &workflow.RunOptions{Concurrency: 2})
		done <- err
	}()

	close(chA)
	time.Sleep(50 * time.Millisecond)
	close(chB)

	select {
	case res := <-resultCh:
		if !res[0] || !res[1] {
			t.Fatalf("expected a=true, b=true, got %+v", res)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for join-all result")
	}

	if err := <-done; err != nil {
		t.Fatalf("run failed: %v", err)
	}
}

func TestWorkflow_BranchFirst(t *testing.T) {
	judgeName := "JudgeUnit_" + t.Name()
	okName := "FlagUnit_" + t.Name()

	workflow.RegisterUnitFactory(judgeName, func() workflow.ExecutableUnit {
		u := &valueUnit{name: judgeName}
		u.UnitName = u.GetUnitName()
		return u
	})
	workflow.RegisterUnitFactory(okName, func() workflow.ExecutableUnit {
		u := &okUnit{name: okName}
		u.UnitName = u.GetUnitName()
		return u
	})

	var ranA, ranB, ranC atomic.Bool
	def := &workflow.WorkflowDefinition{
		ID:    "wf-branch-first",
		Start: []string{"judge"},
		Nodes: map[string]*workflow.NodeSpec{
			"judge": {
				Unit: judgeName,
				Params: map[string]any{
					"value": "b",
				},
				Options: &workflow.NodeOptions{BranchMode: workflow.BranchFirst},
			},
			"a": {Unit: okName, Params: map[string]any{"flag": &ranA}},
			"b": {Unit: okName, Params: map[string]any{"flag": &ranB}},
			"c": {Unit: okName, Params: map[string]any{"flag": &ranC}},
		},
		Edges: []workflow.EdgeSpec{
			{From: "judge", To: "a", When: `Result.Data["value"] == "a"`, Order: 1},
			{From: "judge", To: "b", When: `Result.Data["value"] == "b"`, Order: 2},
			{From: "judge", To: "c"}, // else
		},
	}

	engine := workflow.NewEngine()
	_, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !ranB.Load() || ranA.Load() || ranC.Load() {
		t.Fatalf("branch mismatch: a=%v b=%v c=%v", ranA.Load(), ranB.Load(), ranC.Load())
	}
}
