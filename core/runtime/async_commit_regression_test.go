package wfruntime

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type asyncCommitRegressionStore interface {
	Store
	AsyncTaskStore
	AsyncResultStore
}

func newAsyncCommitRegressionStore(t *testing.T, backend string) asyncCommitRegressionStore {
	t.Helper()
	if backend == "memory" {
		return NewMemoryStore()
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "runtime.db")+"?_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := NewGormStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func claimAsyncCommitRegressionTask(t *testing.T, store AsyncTaskStore) *AsyncTask {
	t.Helper()
	tasks, err := store.ClaimCompletedAsyncTasks(context.Background(), time.Now().UTC(), 1, time.Minute)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("claim: tasks=%d error=%v", len(tasks), err)
	}
	return tasks[0]
}

func seedAsyncCommitRegression(t *testing.T, store asyncCommitRegressionStore) (*WorkflowRun, *AsyncTask) {
	t.Helper()
	ctx := context.Background()
	run := NewWorkflowRun("run-commit", "workflow", "version", "plan")
	run.Status = StatusWaiting
	run.NodeRuns["async"] = &NodeRun{NodeID: "async", DispatchID: "dispatch-commit", Status: StatusWaiting, Attempt: 1, MaxAttempts: 2}
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	task := &AsyncTask{DispatchID: "dispatch-commit", RunID: run.ID, NodeID: "async", ExternalTaskID: "external-commit"}
	if err := store.SaveAsyncTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	result := executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "done"}
	if accepted, err := store.SubmitAsyncResult(ctx, task.DispatchID, result); err != nil || !accepted {
		t.Fatalf("submit: accepted=%v error=%v", accepted, err)
	}
	if accepted, err := store.SubmitAsyncResult(ctx, task.DispatchID, result); err != nil || accepted {
		t.Fatalf("duplicate submit: accepted=%v error=%v", accepted, err)
	}
	if _, err := store.SubmitAsyncResult(ctx, task.DispatchID, executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "different"}); err == nil {
		t.Fatal("conflicting callback accepted")
	}
	loaded, err := store.LoadRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return loaded, claimAsyncCommitRegressionTask(t, store)
}

func TestAsyncCommitRegression(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		for _, scenario := range []string{"success", "changed_run", "cancelled_task", "cancelled_run", "stale_claim", "cancelled_context"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				store := newAsyncCommitRegressionStore(t, backend)
				run, task := seedAsyncCommitRegression(t, store)
				ctx := context.Background()
				updated := run.Clone()
				updated.Status = StatusRunning
				updated.NodeRuns["async"].Status = StatusSuccess
				updated.NodeRuns["async"].Result = "done"
				updated.Context.Variables["artistIds"] = []any{}
				updated.UpdatedAt = run.UpdatedAt.Add(time.Second)
				expected := run.Clone()
				switch scenario {
				case "changed_run":
					expected.UpdatedAt = run.UpdatedAt.Add(2 * time.Second)
					expected.Context.Variables["concurrent"] = "preserve"
					if err := store.SaveRun(ctx, expected); err != nil {
						t.Fatal(err)
					}
				case "cancelled_task":
					if err := store.CancelAsyncTasks(ctx, run.ID); err != nil {
						t.Fatal(err)
					}
				case "cancelled_run":
					expected.Status = StatusCancelled
					if err := store.SaveRun(ctx, expected); err != nil {
						t.Fatal(err)
					}
				case "stale_claim":
					if err := store.ReleaseAsyncTask(ctx, task.DispatchID, task.ClaimToken); err != nil {
						t.Fatal(err)
					}
					newTask := claimAsyncCommitRegressionTask(t, store)
					if newTask.ClaimToken == task.ClaimToken {
						t.Fatal("claim token reused")
					}
					if renewed, err := store.RenewAsyncTask(ctx, task.DispatchID, task.ClaimToken, time.Now().Add(time.Minute)); err != nil || renewed {
						t.Fatalf("stale claim renewed: %v %v", renewed, err)
					}
				case "cancelled_context":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				snapshot := &RunSnapshot{RunID: run.ID, Status: updated.Status, NodeRuns: updated.NodeRuns, Context: updated.Context, At: updated.UpdatedAt}
				event := RunEvent{RunID: run.ID, NodeID: "async", Type: EventNodeDone, Status: StatusSuccess, Time: updated.UpdatedAt}
				err := store.CommitAsyncResult(ctx, task, updated, run.UpdatedAt, []*RunSnapshot{snapshot}, []RunEvent{event})
				if scenario == "success" {
					if err != nil {
						t.Fatal(err)
					}
					expected = updated
					if err := store.CommitAsyncResult(ctx, task, updated, run.UpdatedAt, []*RunSnapshot{snapshot}, []RunEvent{event}); err == nil {
						t.Fatal("callback was applied twice")
					}
					if err := store.ReleaseAsyncTask(ctx, task.DispatchID, task.ClaimToken); err != nil {
						t.Fatal(err)
					}
					if reclaimed := claimAsyncCommitRegressionTask(t, store); !reclaimed.ResultApplied {
						t.Fatal("application marker lost after release/reclaim")
					}
				} else if err == nil {
					t.Fatal("unsafe commit accepted")
				}
				actual, err := store.LoadRun(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if actual.Status != expected.Status || actual.NodeRuns["async"].Status != expected.NodeRuns["async"].Status || !actual.UpdatedAt.Equal(expected.UpdatedAt) {
					t.Fatalf("incorrect persisted checkpoint: %+v", actual)
				}
				events, err := store.Events(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				snapshots, err := store.Snapshots(context.Background(), run.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if scenario == "success" {
					want = 1
				}
				if len(events) != want || len(snapshots) != want {
					t.Fatalf("partial/duplicate effects: events=%d snapshots=%d want=%d", len(events), len(snapshots), want)
				}
			})
		}
	}
}

func TestAsyncCommitRegressionDatabaseRollback(t *testing.T) {
	store := newAsyncCommitRegressionStore(t, "sqlite").(*GormStore)
	run, task := seedAsyncCommitRegression(t, store)
	ctx := context.Background()
	updated := run.Clone()
	updated.NodeRuns["async"].Status = StatusSuccess
	updated.UpdatedAt = run.UpdatedAt.Add(time.Second)
	if err := store.db.Exec("CREATE TRIGGER fail_async_event BEFORE INSERT ON workflow_run_events BEGIN SELECT RAISE(ABORT, 'injected event write failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	event := RunEvent{RunID: run.ID, Type: EventNodeDone}
	if err := store.CommitAsyncResult(ctx, task, updated, run.UpdatedAt, nil, []RunEvent{event}); err == nil {
		t.Fatal("expected injected database failure")
	}
	loaded, err := store.LoadRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NodeRuns["async"].Status != StatusWaiting || !loaded.UpdatedAt.Equal(run.UpdatedAt) {
		t.Fatal("failed transaction partially persisted its run")
	}
	if err := store.ReleaseAsyncTask(ctx, task.DispatchID, task.ClaimToken); err != nil {
		t.Fatal(err)
	}
	task = claimAsyncCommitRegressionTask(t, store)
	if task.ResultApplied {
		t.Fatal("failed transaction persisted its application marker")
	}
	if err := store.db.Exec("DROP TRIGGER fail_async_event").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.CommitAsyncResult(ctx, task, updated, run.UpdatedAt, nil, []RunEvent{event}); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestAsyncCommitRegressionConcurrentClaim(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			store := newAsyncCommitRegressionStore(t, backend)
			_, task := seedAsyncCommitRegression(t, store)
			if err := store.ReleaseAsyncTask(context.Background(), task.DispatchID, task.ClaimToken); err != nil {
				t.Fatal(err)
			}
			var claimed atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					tasks, err := store.ClaimCompletedAsyncTasks(context.Background(), time.Now().UTC(), 1, time.Minute)
					if err != nil {
						t.Error(err)
						return
					}
					claimed.Add(int32(len(tasks)))
				}()
			}
			wg.Wait()
			if claimed.Load() != 1 {
				t.Fatalf("claimed by %d owners", claimed.Load())
			}
		})
	}
}
