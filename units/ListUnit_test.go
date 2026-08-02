package units

import (
	"context"
	"reflect"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestListUnitExecutePreservesItemOrder(t *testing.T) {
	action := NewListUnit()
	configured := []any{"alpha", "beta", "gamma"}
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Params: map[string]any{"items": configured},
	})
	if err != nil {
		t.Fatalf("execute ListUnit: %v", err)
	}
	items, ok := result.Data.([]any)
	if !ok || !reflect.DeepEqual(items, configured) {
		t.Fatalf("unexpected list output: %#v", result.Data)
	}
	items[0] = "changed"
	if configured[0] != "alpha" {
		t.Fatal("ListUnit returned the mutable parameter slice")
	}
}

func TestListUnitExecuteWithoutItemsReturnsEmptyList(t *testing.T) {
	action := NewListUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{})
	if err != nil {
		t.Fatalf("execute empty ListUnit: %v", err)
	}
	items, ok := result.Data.([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("expected an empty list, got %#v", result.Data)
	}
}

func TestListUnitRejectsScalarItems(t *testing.T) {
	action := NewListUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{
		Params: map[string]any{"items": "alpha,beta"},
	})
	if err == nil {
		t.Fatal("expected scalar items to be rejected")
	}
}
