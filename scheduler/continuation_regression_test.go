package scheduler

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/runner"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker/unit"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type continuationRegressionUnit struct {
	unit.Unit
	calls *atomic.Int32
}

func (u *continuationRegressionUnit) GetUnitMeta() *unit.Unit { return &u.Unit }

func (u *continuationRegressionUnit) Execute(ctx context.Context, _ unit.ContextMap, _ *unit.Node) (*unit.ExecutionResult, error) {
	u.calls.Add(1)
	return &unit.ExecutionResult{Status: executor.StatusAccepted, ExternalTaskID: "external-" + unit.DispatchID(ctx)}, nil
}

func openContinuationRegressionStore(t *testing.T, path string) (*wfruntime.GormStore, func()) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	closeDB := func() { _ = sqlDB.Close() }
	t.Cleanup(closeDB)
	store, err := wfruntime.NewGormStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, closeDB
}

func TestAsyncContinuationRegressionRestart(t *testing.T) {
	for _, scenario := range []string{"waiting", "legacy_running", "applied", "applied_loop", "cancelled", "paused"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "restart.db")
			store, closeDB := openContinuationRegressionStore(t, path)
			var submits, successors atomic.Int32
			units := unit.NewRegistry()
			if err := units.Register("await-callback", func() unit.Executable {
				return &continuationRegressionUnit{calls: &submits}
			}); err != nil {
				t.Fatal(err)
			}
			reg := executor.NewRegistry()
			if err := reg.Register(unit.NewExecutor(units)); err != nil {
				t.Fatal(err)
			}
			local := executor.NewLocalExecutor()
			local.Register("successor", func(_ context.Context, request executor.Request) (executor.Result, error) {
				successors.Add(1)
				ids, ok := request.Params["artistIds"].([]any)
				if !ok || len(ids) != 0 {
					t.Errorf("explicit empty callback variable replaced by default: %#v", request.Params)
				}
				return executor.Result{Output: "finished"}, nil
			})
			if err := reg.Register(local); err != nil {
				t.Fatal(err)
			}
			version := &definition.WorkflowVersion{
				ID: "async:v1", WorkflowID: "async", Version: 1,
				Definition: &definition.WorkflowDefinition{
					ID: "async", Name: "callback restart", EntryNodes: []string{"async"},
					Nodes: []definition.Node{
						{ID: "async", Name: "async", Executor: definition.ExecutorSpec{Type: string(executor.TypeUnit), Ref: "await-callback"}},
						{ID: "next", Name: "next", Executor: definition.ExecutorSpec{Type: string(executor.TypeLocalGo), Ref: "successor"}, Params: map[string]any{"fn": "successor"}, ParamBindings: map[string]definition.InputBinding{
							"artistIds": {Source: definition.InputSourceVar, From: "artistIds", Default: []any{"default"}},
						}},
					},
					Edges: []definition.Edge{{From: "async", To: "next"}},
				},
			}
			if scenario == "applied_loop" {
				version.Definition.Nodes[0].Loop = &definition.LoopPolicy{MaxIterations: 2}
			}
			newService := func(store wfruntime.Store) *Service {
				defs := definition.NewMemoryRepository()
				if err := defs.SaveVersion(ctx, version); err != nil {
					t.Fatal(err)
				}
				controller := runner.NewMemoryRunController()
				sched := runner.NewDefaultScheduler(reg, store)
				sched.RunController = controller
				return &Service{store: store, definitions: defs, engine: runner.NewEngine(nil, sched), controller: controller, continuationWake: make(chan struct{}, 1)}
			}
			svc := newService(store)
			plan, err := svc.engine.Compiler.Compile(version)
			if err != nil {
				t.Fatal(err)
			}
			run, err := svc.engine.Scheduler.Run(ctx, plan, nil)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != wfruntime.StatusWaiting || submits.Load() != 1 || successors.Load() != 0 {
				t.Fatalf("unit did not suspend cleanly: status=%s submits=%d successors=%d", run.Status, submits.Load(), successors.Load())
			}
			dispatchID := run.NodeRuns["async"].DispatchID
			result := executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "callback", Variables: map[string]any{"artistIds": []any{}}}
			if accepted, err := svc.SubmitAsyncResult(ctx, dispatchID, result); err != nil || !accepted {
				t.Fatalf("submit: %v %v", accepted, err)
			}
			if successors.Load() != 0 {
				t.Fatal("callback synchronously executed successor")
			}
			tasks, err := store.ClaimCompletedAsyncTasks(ctx, time.Now().UTC(), 1, time.Minute)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("claim: %d %v", len(tasks), err)
			}
			task := tasks[0]
			run, err = store.LoadRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "legacy_running" {
				run.Status = wfruntime.StatusRunning
				run.NodeRuns["async"].Status = wfruntime.StatusRunning
				run.CurrentNodes = []string{"async"}
				if err := store.SaveRun(ctx, run); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "applied" || scenario == "applied_loop" || scenario == "cancelled" {
				if err := svc.engine.Scheduler.(*runner.DefaultScheduler).ApplyAsyncResult(ctx, plan, run, task); err != nil {
					t.Fatal(err)
				}
				if !task.ResultApplied {
					t.Fatal("missing committed application marker")
				}
				if scenario == "applied_loop" && run.NodeRuns["async"].Status != wfruntime.StatusPending {
					t.Fatal("loop did not reset node to pending")
				}
			}
			if scenario == "cancelled" {
				if _, err := svc.CancelRun(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "paused" {
				paused, err := svc.PauseRun(ctx, run.ID)
				if err != nil || paused.Status != wfruntime.StatusPaused {
					t.Fatalf("pause waiting run: %v %v", paused, err)
				}
				if err := svc.processCompletedAsyncTask(ctx, task); err == nil {
					t.Fatal("paused task resumed")
				}
				if err := store.ReleaseAsyncTask(ctx, task.DispatchID, task.ClaimToken); err != nil {
					t.Fatal(err)
				}
				resumed, err := svc.ResumeRun(ctx, run.ID)
				if err != nil || resumed.Status != wfruntime.StatusWaiting || submits.Load() != 1 {
					t.Fatalf("resume waiting run: %v %v submits=%d", resumed, err, submits.Load())
				}
			}
			// Close the database without acknowledging/releasing the old claim.
			closeDB()
			store, _ = openContinuationRegressionStore(t, path)
			svc = newService(store)
			tasks, err = store.ClaimCompletedAsyncTasks(ctx, time.Now().Add(2*time.Minute), 1, 5*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "cancelled" {
				if len(tasks) != 0 {
					t.Fatal("cancelled task reclaimed")
				}
				if err := svc.processCompletedAsyncTask(ctx, task); err == nil {
					t.Fatal("cancelled task resumed by stale owner")
				}
			} else {
				if len(tasks) != 1 {
					t.Fatalf("restart reclaimed %d tasks", len(tasks))
				}
				if tasks[0].ClaimToken == task.ClaimToken {
					t.Fatal("restart reused stale claim")
				}
				if tasks[0].ResultApplied != task.ResultApplied {
					t.Fatal("restart lost application marker")
				}
				if err := svc.processCompletedAsyncTask(ctx, tasks[0]); err != nil {
					t.Fatalf("resume: %v", err)
				}
			}
			loaded, err := store.LoadRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "cancelled":
				if loaded.Status != wfruntime.StatusCancelled || successors.Load() != 0 {
					t.Fatal("cancelled workflow continued")
				}
			case "applied_loop":
				if loaded.Status != wfruntime.StatusWaiting || submits.Load() != 2 || successors.Load() != 0 {
					t.Fatalf("loop callback applied twice or not resumed: status=%s submits=%d successors=%d", loaded.Status, submits.Load(), successors.Load())
				}
			default:
				if loaded.Status != wfruntime.StatusSuccess || submits.Load() != 1 || successors.Load() != 1 {
					t.Fatalf("incorrect recovery: status=%s submits=%d successors=%d", loaded.Status, submits.Load(), successors.Load())
				}
			}
		})
	}
}
