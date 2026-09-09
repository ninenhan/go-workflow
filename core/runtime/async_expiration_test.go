package wfruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

func TestAsyncExpiration(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		for _, scenario := range []string{"expired", "unlimited", "cancelled", "accepted_before_deadline"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				ctx := context.Background()
				store := newAsyncCommitRegressionStore(t, backend)
				now := time.Now().UTC()
				task := &AsyncTask{DispatchID: "dispatch-expiry", RunID: "run-expiry", NodeID: "async", ExternalTaskID: "external-expiry", ExpireAt: now.Add(-time.Minute)}
				if scenario == "unlimited" {
					task.ExpireAt = time.Time{}
				}
				if scenario == "accepted_before_deadline" {
					task.ExpireAt = now.Add(time.Hour)
				}
				if err := store.SaveAsyncTask(ctx, task); err != nil {
					t.Fatal(err)
				}
				result := executor.ExecuteResult{Status: executor.StatusSucceeded, Output: "done"}
				switch scenario {
				case "expired":
					if accepted, err := store.SubmitAsyncResult(ctx, task.DispatchID, result); accepted || !errors.Is(err, ErrAsyncTaskExpired) {
						t.Fatalf("late callback before sweep: accepted=%v error=%v", accepted, err)
					}
				case "cancelled":
					if err := store.CancelAsyncTasks(ctx, task.RunID); err != nil {
						t.Fatal(err)
					}
				case "accepted_before_deadline":
					if accepted, err := store.SubmitAsyncResult(ctx, task.DispatchID, result); !accepted || err != nil {
						t.Fatalf("callback: %v %v", accepted, err)
					}
				}
				tasks, err := store.ClaimCompletedAsyncTasks(ctx, now.Add(2*time.Hour), 10, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "unlimited" || scenario == "cancelled" {
					if len(tasks) != 0 {
						t.Fatalf("unexpected expiry: %+v", tasks)
					}
					return
				}
				if len(tasks) != 1 || !tasks[0].ExpireAt.Equal(task.ExpireAt) {
					t.Fatalf("deadline not persisted: %+v", tasks)
				}
				if scenario == "expired" {
					if tasks[0].Result.Status != executor.StatusFailed || tasks[0].Result.Error != ErrAsyncTaskExpired.Error() {
						t.Fatalf("wrong timeout result: %+v", tasks[0].Result)
					}
					if accepted, err := store.SubmitAsyncResult(ctx, task.DispatchID, result); accepted || !errors.Is(err, ErrAsyncTaskExpired) {
						t.Fatalf("late callback after sweep: %v %v", accepted, err)
					}
				} else {
					if tasks[0].Result.Status != executor.StatusSucceeded {
						t.Fatal("on-time callback overwritten by expiration")
					}
					if accepted, err := store.SubmitAsyncResult(ctx, task.DispatchID, result); accepted || err != nil {
						t.Fatalf("duplicate callback: %v %v", accepted, err)
					}
				}
				again, err := store.ClaimCompletedAsyncTasks(ctx, now.Add(2*time.Hour), 10, time.Minute)
				if err != nil || len(again) != 0 {
					t.Fatalf("duplicate timeout claim: %d %v", len(again), err)
				}
			})
		}
	}
}
