package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	workflow "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/server"
	"github.com/ninenhan/go-workflow/store"
)
import _ "github.com/ninenhan/go-workflow/units"

func TestAPIServerRunHttpUnit(t *testing.T) {

	memStore := store.NewMemoryStateStore()
	engine := workflow.NewEngine()
	api := server.NewAPIServer(engine, memStore)
	httpSrv := httptest.NewServer(api.Handler())
	defer httpSrv.Close()

	def := &workflow.WorkflowDefinition{
		ID:    "wf-http-demo",
		Name:  "HTTP Demo",
		Start: []string{"http1"},
		Nodes: map[string]*workflow.NodeSpec{
			"http1": {
				ID:   "http1",
				Name: "Call Http",
				Unit: "HttpUnit",
				Input: &workflow.Input{
					Data: map[string]any{
						"id":     "req-1",
						"url":    "https://example.com/api",
						"method": "POST",
						"headers": map[string][]string{
							"X-Demo": {"1"},
						},
						"body": map[string]any{
							"message": "hello",
						},
					},
				},
				ExportFields: []string{"data"},
			},
		},
	}

	payload, err := json.Marshal(map[string]any{"definition": def})
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}

	resp, err := http.Post(httpSrv.URL+"/v1/runs", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("run request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, string(body))
	}

	var runResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if runResp.RunID == "" {
		t.Fatalf("missing run_id")
	}

	var state *workflow.ExecutionState
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stateResp, err := http.Get(httpSrv.URL + "/v1/runs/" + runResp.RunID)
		if err != nil {
			t.Fatalf("get state: %v", err)
		}
		if stateResp.StatusCode == http.StatusNotFound {
			stateResp.Body.Close()
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if stateResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(stateResp.Body)
			stateResp.Body.Close()
			t.Fatalf("state status: %d body=%s", stateResp.StatusCode, string(body))
		}
		if err := json.NewDecoder(stateResp.Body).Decode(&state); err != nil {
			stateResp.Body.Close()
			t.Fatalf("decode state: %v", err)
		}
		stateResp.Body.Close()
		if state.Status != workflow.RunRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if state == nil {
		t.Fatalf("state not ready")
	}
	node := state.Nodes["http1"]
	if node == nil || node.Status != workflow.NodeSucceeded {
		t.Fatalf("http1 not succeeded: %+v", node)
	}
}
