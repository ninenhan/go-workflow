package units

import (
	"context"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestBranchJoinUnitSelectsActiveBranch(t *testing.T) {
	u := &BranchJoinUnit{
		Unit:        unit.Unit{UnitName: "BranchJoinUnit"},
		BranchOrder: []string{"then", "otherwise"},
	}
	result, err := u.Execute(context.Background(), nil, &unit.Node{Input: &unit.Input{Data: map[string]any{
		"then":      map[string]any{"message": "selected"},
		"otherwise": nil,
	}}})
	if err != nil {
		t.Fatalf("execute branch join: %v", err)
	}
	value, ok := result.Data.(map[string]any)
	if !ok || value["message"] != "selected" {
		t.Fatalf("unexpected branch result: %#v", result.Data)
	}
}

func TestBranchJoinUnitRejectsAmbiguousConfiguration(t *testing.T) {
	u := &BranchJoinUnit{Unit: unit.Unit{UnitName: "BranchJoinUnit"}}
	if _, err := u.Execute(context.Background(), nil, &unit.Node{}); err == nil {
		t.Fatal("expected missing branch order to fail")
	}

	u.BranchOrder = []string{"then", "otherwise"}
	if _, err := u.Execute(context.Background(), nil, &unit.Node{Input: &unit.Input{Data: "invalid"}}); err == nil {
		t.Fatal("expected non-object input to fail")
	}
}

func TestBranchJoinUnitSelectsExactMenuBranch(t *testing.T) {
	u := &BranchJoinUnit{
		Unit:         unit.Unit{UnitName: "BranchJoinUnit"},
		BranchOrder:  []string{"menu-1", "menu-2"},
		BranchLabels: map[string]string{"menu-1": "Approve", "menu-2": "Reject"},
	}
	result, err := u.Execute(context.Background(), nil, &unit.Node{Input: &unit.Input{Data: map[string]any{
		"__selected": "Reject",
		"menu-1":     "Reject",
		"menu-2":     "Rejected branch ran",
	}}})
	if err != nil {
		t.Fatalf("execute menu branch join: %v", err)
	}
	if result.Data != "Rejected branch ran" {
		t.Fatalf("unexpected menu branch result: %#v", result.Data)
	}
}

func TestBranchJoinUnitRejectsUnknownMenuSelection(t *testing.T) {
	u := &BranchJoinUnit{
		Unit:         unit.Unit{UnitName: "BranchJoinUnit"},
		BranchOrder:  []string{"menu-1"},
		BranchLabels: map[string]string{"menu-1": "Approve"},
	}
	if _, err := u.Execute(context.Background(), nil, &unit.Node{Input: &unit.Input{Data: map[string]any{
		"__selected": "Reject",
	}}}); err == nil {
		t.Fatal("expected unknown menu selection to fail")
	}
}
