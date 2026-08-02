package units

import (
	"context"
	"errors"
	"testing"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestTimeoutUnitUsesDurationParamAndPreservesInput(t *testing.T) {
	u := &TimeoutUnit{Unit: unit.Unit{UnitName: "TimeoutUnit"}}
	started := time.Now()
	result, err := u.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: "payload"},
		Params: map[string]any{"duration_ms": float64(25)},
	})
	if err != nil {
		t.Fatalf("execute timeout unit: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("duration parameter was ignored: %s", elapsed)
	}
	if result == nil || result.Data != "payload" {
		t.Fatalf("input was not preserved: %+v", result)
	}
}

func TestTimeoutUnitRejectsInvalidDuration(t *testing.T) {
	u := &TimeoutUnit{Unit: unit.Unit{UnitName: "TimeoutUnit"}}
	_, err := u.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{"duration_ms": 0}})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}

func TestTimeoutUnitStopsWithContext(t *testing.T) {
	u := &TimeoutUnit{Unit: unit.Unit{UnitName: "TimeoutUnit"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := u.Execute(ctx, nil, &unit.Node{Params: map[string]any{"duration_ms": 5000}})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("cancellation was not prompt: %s", elapsed)
	}
}
