package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/runner"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker/unit"
)

type expirationRegressionUnit struct {
	unit.Unit
	ttl      time.Duration
	expireAt time.Time
}

func (u *expirationRegressionUnit) GetUnitMeta() *unit.Unit { return &u.Unit }

func (u *expirationRegressionUnit) Execute(context.Context, unit.ContextMap, *unit.Node) (*unit.ExecutionResult, error) {
	return &unit.ExecutionResult{Status: executor.StatusAccepted, ExternalTaskID: "expiry-job", TTL: u.ttl, ExpireAt: u.expireAt}, nil
}

func TestAsyncExpirationRestart(t *testing.T) {
	for _, mode := range []string{"ttl", "expire_at"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "expiry.db")
			store, closeDB := openContinuationRegressionStore(t, path)
			u := &expirationRegressionUnit{expireAt: time.Now().Add(-time.Minute).UTC()}
			if mode == "ttl" {
				u.expireAt = time.Time{}
				u.ttl = time.Minute
			}
			units := unit.NewRegistry()
			if err := units.Register("expiry", func() unit.Executable { return u }); err != nil {
				t.Fatal(err)
			}
			reg := executor.NewRegistry()
			if err := reg.Register(unit.NewExecutor(units)); err != nil {
				t.Fatal(err)
			}
			version := &definition.WorkflowVersion{ID: "expiry:v1", WorkflowID: "expiry", Version: 1, Definition: &definition.WorkflowDefinition{
				ID: "expiry", Name: "expiry", EntryNodes: []string{"async"},
				Nodes: []definition.Node{{ID: "async", Name: "expiry", Executor: definition.ExecutorSpec{Type: string(executor.TypeUnit), Ref: "expiry"}, Retry: &definition.RetryPolicy{MaxAttempts: 3}}},
			}}
			defs := definition.NewMemoryRepository()
			if err := defs.SaveVersion(ctx, version); err != nil {
				t.Fatal(err)
			}
			sched := runner.NewDefaultScheduler(reg, store)
			engine := runner.NewEngine(nil, sched)
			plan, err := engine.Compiler.Compile(version)
			if err != nil {
				t.Fatal(err)
			}
			before := time.Now().UTC()
			run, err := sched.Run(ctx, plan, nil)
			if err != nil || run.Status != wfruntime.StatusWaiting {
				t.Fatalf("wait: %+v %v", run, err)
			}
			after := time.Now().UTC()
			closeDB()
			store, _ = openContinuationRegressionStore(t, path)
			sched = runner.NewDefaultScheduler(reg, store)
			engine = runner.NewEngine(nil, sched)
			svc := &Service{store: store, definitions: defs, engine: engine}
			// Advancing the claim clock simulates downtime beyond the deadline.
			tasks, err := store.ClaimCompletedAsyncTasks(ctx, after.Add(2*time.Minute), 1, time.Minute)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("claim expiry: %d %v", len(tasks), err)
			}
			task := tasks[0]
			if mode == "ttl" {
				if task.ExpireAt.Before(before.Add(time.Minute)) || task.ExpireAt.After(after.Add(time.Minute)) {
					t.Fatalf("TTL was reset on restart: %v", task.ExpireAt)
				}
			} else if !task.ExpireAt.Equal(u.expireAt) {
				t.Fatal("absolute deadline changed on restart")
			}
			if err := svc.processCompletedAsyncTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.LoadRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			node := loaded.NodeRuns["async"]
			if loaded.Status != wfruntime.StatusFailed || node.Status != wfruntime.StatusTimeout || node.Attempt != 1 {
				t.Fatalf("timeout was not terminal: run=%s node=%+v", loaded.Status, node)
			}
			if mode == "expire_at" {
				if _, err := svc.SubmitAsyncResult(ctx, task.DispatchID, executor.ExecuteResult{Status: executor.StatusSucceeded}); !errors.Is(err, wfruntime.ErrAsyncTaskExpired) {
					t.Fatalf("late callback: %v", err)
				}
			}
		})
	}
}
