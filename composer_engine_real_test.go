package workflow_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	workflow "github.com/ninenhan/go-workflow"
	xhttp "github.com/ninenhan/go-workflow/kit"
	"github.com/ninenhan/go-workflow/units"
)

type readableUnit struct {
	workflow.Unit
	name string
}

func (u *readableUnit) GetUnitName() string { return u.name }
func (u *readableUnit) GetUnitMeta() *workflow.Unit {
	return &u.Unit
}

func (u *readableUnit) Execute(ctx context.Context, state workflow.ContextMap, self *workflow.Node) (*workflow.ExecutionResult, error) {
	raw := state["http"].Data
	var sb strings.Builder
	if arr, ok := raw.([]any); ok {
		for _, v := range arr {
			sb.WriteString(fmt.Sprint(v))
		}
	} else {
		sb.WriteString(fmt.Sprint(raw))
	}
	reader := strings.NewReader(sb.String())
	return &workflow.ExecutionResult{
		NodeName: u.UnitName,
		Data:     reader,
	}, nil
}

func TestWorkflow_WithHttpLogReadable(t *testing.T) {
	// Local HTTP server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// Register core HttpUnit
	units.AutoRegister()

	// Register test readable unit
	readableName := "ReadableUnit_" + strings.ReplaceAll(t.Name(), "/", "_")
	workflow.RegisterUnitFactory(readableName, func() workflow.ExecutableUnit {
		u := &readableUnit{name: readableName}
		u.UnitName = u.GetUnitName()
		return u
	})

	def := &workflow.WorkflowDefinition{
		ID:    "wf-http",
		Start: []string{"http"},
		Nodes: map[string]*workflow.NodeSpec{
			"http": {
				Unit: "HttpUnit",
				Input: &workflow.Input{
					Data: xhttp.XRequest{
						Url:    srv.URL,
						Method: "POST",
						Body:   map[string]any{"q": "ping"},
					},
				},
			},
			"read": {
				Unit: readableName,
			},
		},
		Edges: []workflow.EdgeSpec{
			{From: "http", To: "read"},
		},
	}

	engine := workflow.NewEngine()
	state, err := engine.Run(context.Background(), def, &workflow.RunOptions{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if state.Status != workflow.RunSucceeded {
		t.Fatalf("unexpected status: %s", state.Status)
	}
	res := state.Nodes["read"].Result
	reader, ok := res.Data.(io.Reader)
	if !ok {
		t.Fatalf("expected io.Reader, got %T", res.Data)
	}
	body, _ := io.ReadAll(reader)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected body: %s", string(body))
	}
}
