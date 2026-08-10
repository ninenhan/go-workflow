package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

func TestHTTPHandler_RunLifecycle(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	reqBody, err := json.Marshal(RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-http-1",
			Name:       "http-api-demo",
			EntryNodes: []string{"n1"},
			Nodes: []definition.Node{
				{
					ID:       "n1",
					Name:     "log",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					Input:    "hello-http",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/runs", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}

	var createResp struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Run == nil || createResp.Run.ID == "" {
		t.Fatalf("missing run in create response")
	}

	runResp, err := http.Get(server.URL + "/v1/runs/" + createResp.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	defer runResp.Body.Close()
	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get run status: %d", runResp.StatusCode)
	}

	var loaded struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(runResp.Body).Decode(&loaded); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if loaded.Run == nil || loaded.Run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected loaded run: %+v", loaded.Run)
	}

	eventsResp, err := http.Get(server.URL + "/v1/runs/" + createResp.Run.ID + "/events")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer eventsResp.Body.Close()
	var eventsBody struct {
		Events []wfruntime.RunEvent `json:"events"`
	}
	if err := json.NewDecoder(eventsResp.Body).Decode(&eventsBody); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(eventsBody.Events) == 0 {
		t.Fatalf("expected events")
	}

	snapshotsResp, err := http.Get(server.URL + "/v1/runs/" + createResp.Run.ID + "/snapshots")
	if err != nil {
		t.Fatalf("get snapshots: %v", err)
	}
	defer snapshotsResp.Body.Close()
	var snapshotsBody struct {
		Snapshots []*wfruntime.RunSnapshot `json:"snapshots"`
	}
	if err := json.NewDecoder(snapshotsResp.Body).Decode(&snapshotsBody); err != nil {
		t.Fatalf("decode snapshots: %v", err)
	}
	if len(snapshotsBody.Snapshots) == 0 {
		t.Fatalf("expected snapshots")
	}
}

func TestHTTPHandlerRejectsOversizedRunPayload(t *testing.T) {
	handler := NewHTTPHandler(&Service{})
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/runs",
		strings.NewReader(`{"run":{"input":"`+strings.Repeat("x", int(maxRunPayloadBytes))+`"}}`),
	)
	response := httptest.NewRecorder()

	handler.handleRunCreate(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHTTPHandlerRejectsTrailingRunJSON(t *testing.T) {
	handler := NewHTTPHandler(&Service{})
	request := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{} {}`))
	response := httptest.NewRecorder()

	handler.handleRunCreate(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHTTPHandler_SynchronousRunCreateIsIdempotent(t *testing.T) {
	store := wfruntime.NewMemoryStore()
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	requestBody, err := json.Marshal(RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-http-idempotent",
			EntryNodes: []string{"text"},
			Nodes: []definition.Node{{
				ID:       "text",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
				Params:   map[string]any{"text": "once"},
			}},
		},
		Run: &wfruntime.WorkflowRun{ID: "client-http-run-1"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	postRun := func() *wfruntime.WorkflowRun {
		t.Helper()
		response, postErr := http.Post(server.URL+"/v1/runs", "application/json", bytes.NewReader(requestBody))
		if postErr != nil {
			t.Fatalf("post run: %v", postErr)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("run status = %d", response.StatusCode)
		}
		var payload struct {
			Run *wfruntime.WorkflowRun `json:"run"`
		}
		if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr != nil {
			t.Fatalf("decode run: %v", decodeErr)
		}
		return payload.Run
	}

	first := postRun()
	duplicate := postRun()
	if first == nil || duplicate == nil || first.ID != duplicate.ID || duplicate.Status != wfruntime.StatusSuccess {
		t.Fatalf("idempotent responses differ: first=%#v duplicate=%#v", first, duplicate)
	}
	events, err := store.Events(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	started := 0
	for _, event := range events {
		if event.Type == wfruntime.EventRunStarted {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("synchronous run started %d times; events=%#v", started, events)
	}
}

func TestHTTPHandler_RunAsyncCancel(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()
	reqBody, err := json.Marshal(RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-http-async",
			EntryNodes: []string{"wait"},
			Nodes: []definition.Node{{
				ID:       "wait",
				Name:     "wait",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"},
				Input:    "3000",
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	started := time.Now()
	resp, err := http.Post(server.URL+"/v1/runs?wait=false", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post async run: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		resp.Body.Close()
		t.Fatalf("async start blocked for %s", elapsed)
	}
	if resp.StatusCode != http.StatusAccepted {
		resp.Body.Close()
		t.Fatalf("unexpected async status: %d", resp.StatusCode)
	}
	var accepted struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		resp.Body.Close()
		t.Fatalf("decode async response: %v", err)
	}
	resp.Body.Close()
	if accepted.Run == nil || accepted.Run.ID == "" || accepted.Run.Status != wfruntime.StatusPending {
		t.Fatalf("unexpected accepted run: %+v", accepted.Run)
	}
	if node := accepted.Run.NodeRuns["wait"]; node == nil || node.Status != wfruntime.StatusPending {
		t.Fatalf("missing pending node state: %+v", node)
	}

	loadRun := func() *wfruntime.WorkflowRun {
		t.Helper()
		loadResp, loadErr := http.Get(server.URL + "/v1/runs/" + accepted.Run.ID)
		if loadErr != nil {
			t.Fatalf("get async run: %v", loadErr)
		}
		defer loadResp.Body.Close()
		var body struct {
			Run *wfruntime.WorkflowRun `json:"run"`
		}
		if err := json.NewDecoder(loadResp.Body).Decode(&body); err != nil {
			t.Fatalf("decode async run: %v", err)
		}
		return body.Run
	}

	deadline := time.Now().Add(time.Second)
	for {
		run := loadRun()
		if run != nil && run.Status == wfruntime.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not enter running state: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancelResp, err := http.Post(server.URL+"/v1/runs/"+accepted.Run.ID+"/cancel", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("cancel async run: %v", err)
	}
	cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected cancel status: %d", cancelResp.StatusCode)
	}

	deadline = time.Now().Add(time.Second)
	for {
		run := loadRun()
		if run != nil && run.Status == wfruntime.StatusCancelled {
			if node := run.NodeRuns["wait"]; node == nil || node.Status != wfruntime.StatusCancelled {
				t.Fatalf("unexpected cancelled node: %+v", node)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not cancel promptly: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHTTPHandler_RunWithContextVariables(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	reqBody, err := json.Marshal(RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-runtime-form",
			Name:       "runtime-form",
			EntryNodes: []string{"feedback"},
			Nodes: []definition.Node{
				{
					ID:       "feedback",
					Name:     "feedback",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"},
					InputSpec: &definition.InputSpec{
						Mode: definition.InputModeObject,
						Bindings: []definition.InputBinding{
							{Source: definition.InputSourceVar, From: "name", As: "name", Required: true},
							{Source: definition.InputSourceVar, From: "category", As: "category", Required: true},
						},
					},
				},
			},
		},
		Run: &wfruntime.WorkflowRun{
			Context: wfruntime.RunContext{
				Variables: map[string]any{
					"name":     "Ada",
					"category": "Product feedback",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/runs", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}

	var body struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Run == nil || body.Run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run: %+v", body.Run)
	}
	result, ok := body.Run.Context.NodeResults["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected feedback result: %#v", body.Run.Context.NodeResults["feedback"])
	}
	if result["name"] != "Ada" || result["category"] != "Product feedback" {
		t.Fatalf("runtime variables were not materialized: %#v", result)
	}
}

func TestHTTPHandler_ValidateDefinition(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	reqBody, err := json.Marshal(RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-validate-1",
			Name:       "validate-demo",
			EntryNodes: []string{"n1"},
			Nodes: []definition.Node{
				{
					ID:       "n1",
					Name:     "log",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					Input:    "hello-validate",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(server.URL+"/v1/validate", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post validate: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected validate status: %d", resp.StatusCode)
	}

	var validateResp ValidateResponse
	if err := json.NewDecoder(resp.Body).Decode(&validateResp); err != nil {
		t.Fatalf("decode validate response: %v", err)
	}
	if !validateResp.OK {
		t.Fatalf("expected validation success")
	}
	if validateResp.Plan == nil || validateResp.Plan.PlanID == "" {
		t.Fatalf("missing execution plan in validation response")
	}
}

func TestHTTPHandler_WorkerRegistryEndpoints(t *testing.T) {
	workers := NewMemoryWorkerRegistry()
	svc, err := NewService(Options{
		EnableEmbeddedWorker: false,
		WorkerRegistry:       workers,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	registerBody, err := json.Marshal(workerproto.RegisterRequest{
		Worker: workerproto.WorkerDescriptor{
			ID:                     "worker-1",
			Endpoint:               "http://worker.example",
			Tenant:                 "tenant-a",
			Status:                 workerproto.StatusOnline,
			Weight:                 3,
			SupportedExecutorTypes: []string{definition.ExecutorTypeUnit},
			SupportedExecutorRefs:  []string{"LogUnit"},
			Labels:                 map[string]string{"region": "cn", "tier": "gpu"},
		},
	})
	if err != nil {
		t.Fatalf("marshal register: %v", err)
	}

	resp, err := http.Post(server.URL+workerproto.DefaultRegisterPath, "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected register status: %d", resp.StatusCode)
	}

	listResp, err := http.Get(server.URL + "/v1/workers")
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	defer listResp.Body.Close()
	var listBody struct {
		Workers []workerproto.WorkerDescriptor `json:"workers"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listBody); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(listBody.Workers) != 1 {
		t.Fatalf("unexpected worker count: %d", len(listBody.Workers))
	}
	if listBody.Workers[0].Tenant != "tenant-a" {
		t.Fatalf("unexpected tenant: %s", listBody.Workers[0].Tenant)
	}

	heartbeatBody, err := json.Marshal(workerproto.HeartbeatRequest{WorkerID: "worker-1"})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+workerproto.DefaultHeartbeatPath, bytes.NewReader(heartbeatBody))
	if err != nil {
		t.Fatalf("new heartbeat request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected heartbeat status: %d", resp.StatusCode)
	}

	filteredResp, err := http.Get(server.URL + "/v1/workers?tenant=tenant-a&status=online&labels=region:cn,tier:gpu")
	if err != nil {
		t.Fatalf("filtered workers: %v", err)
	}
	defer filteredResp.Body.Close()
	var filtered struct {
		Workers []workerproto.WorkerDescriptor `json:"workers"`
	}
	if err := json.NewDecoder(filteredResp.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered workers: %v", err)
	}
	if len(filtered.Workers) != 1 {
		t.Fatalf("unexpected filtered worker count: %d", len(filtered.Workers))
	}
}

func TestHTTPHandler_WorkersFilterStaleStatus(t *testing.T) {
	workers := NewMemoryWorkerRegistry()
	baseNow := time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC)
	workers.Now = func() time.Time { return baseNow }
	workers.HeartbeatTTL = time.Minute
	if err := workers.Register(context.Background(), workerproto.WorkerDescriptor{
		ID:                     "worker-stale",
		Endpoint:               "http://worker.example",
		Status:                 workerproto.StatusOnline,
		SupportedExecutorTypes: []string{definition.ExecutorTypeUnit},
	}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	workers.Now = func() time.Time { return baseNow.Add(2 * time.Minute) }

	svc, err := NewService(Options{
		EnableEmbeddedWorker: false,
		WorkerRegistry:       workers,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/workers?status=offline")
	if err != nil {
		t.Fatalf("get offline workers: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Workers []workerproto.WorkerDescriptor `json:"workers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode offline workers: %v", err)
	}
	if len(body.Workers) != 1 || body.Workers[0].ID != "worker-stale" {
		t.Fatalf("unexpected offline workers: %+v", body.Workers)
	}
}

func TestHTTPHandler_WorkflowVersionLifecycle(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	workflowBody, err := json.Marshal(definition.Workflow{
		ID:   "wf-api-1",
		Name: "workflow-api",
	})
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/workflows", "application/json", bytes.NewReader(workflowBody))
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("unexpected workflow status: %d", resp.StatusCode)
	}
	var workflowResponse struct {
		Workflow *definition.Workflow `json:"workflow"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&workflowResponse); err != nil {
		resp.Body.Close()
		t.Fatalf("decode workflow response: %v", err)
	}
	resp.Body.Close()
	if workflowResponse.Workflow == nil || workflowResponse.Workflow.CreatedAt.IsZero() || workflowResponse.Workflow.UpdatedAt.IsZero() {
		t.Fatalf("workflow response was not materialized: %#v", workflowResponse.Workflow)
	}

	versionBody, err := json.Marshal(map[string]any{
		"version": definition.WorkflowVersion{
			ID:         "wf-api-1:v1",
			WorkflowID: "wf-api-1",
			Version:    1,
			Status:     definition.VersionDraft,
			Definition: &definition.WorkflowDefinition{
				ID:         "wf-api-1",
				Name:       "workflow-api",
				EntryNodes: []string{"n1"},
				Nodes: []definition.Node{
					{
						ID:       "n1",
						Name:     "log",
						Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
						Input:    "hello-version",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal version: %v", err)
	}
	resp, err = http.Post(server.URL+"/v1/workflows/wf-api-1/versions", "application/json", bytes.NewReader(versionBody))
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected version status: %d", resp.StatusCode)
	}

	resp, err = http.Post(server.URL+"/v1/workflow-versions/wf-api-1:v1/publish", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("publish version: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected publish status: %d", resp.StatusCode)
	}

	resp, err = http.Post(server.URL+"/v1/workflow-versions/wf-api-1:v1/runs", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("run version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected run-by-version status: %d", resp.StatusCode)
	}
	var runResp struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		t.Fatalf("decode run-by-version: %v", err)
	}
	if runResp.Run == nil || runResp.Run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected run result: %+v", runResp.Run)
	}
}

func TestHTTPHandler_CreateVersionFromDefinitionAllocatesVersions(t *testing.T) {
	svc, err := NewService(Options{EnableEmbeddedWorker: true, Store: wfruntime.NewMemoryStore()})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	definitionBody, err := json.Marshal(map[string]any{"definition": definition.WorkflowDefinition{
		ID: "client-value", Name: "versioned", Nodes: []definition.Node{{
			ID: "n1", Name: "log", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
		}},
	}})
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}

	for expected := 1; expected <= 2; expected++ {
		resp, postErr := http.Post(server.URL+"/v1/workflows/wf-versioned/versions", "application/json", bytes.NewReader(definitionBody))
		if postErr != nil {
			t.Fatalf("create version %d: %v", expected, postErr)
		}
		var payload struct {
			Version *definition.WorkflowVersion `json:"version"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create version %d status: %d", expected, resp.StatusCode)
		}
		if decodeErr != nil {
			t.Fatalf("decode version %d: %v", expected, decodeErr)
		}
		if payload.Version == nil || payload.Version.ID != fmt.Sprintf("wf-versioned:v%d", expected) || payload.Version.Version != expected {
			t.Fatalf("unexpected version %d: %+v", expected, payload.Version)
		}
		if payload.Version.Definition.ID != "wf-versioned" {
			t.Fatalf("definition id = %q", payload.Version.Definition.ID)
		}
	}
}

func TestHTTPHandler_PublishedRoute(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{
		ID:   "wf-publish-1",
		Name: "publish",
	}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := svc.SaveVersion(context.Background(), &definition.WorkflowVersion{
		ID:         "wf-publish-1:v1",
		WorkflowID: "wf-publish-1",
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-publish-1",
			Name:       "publish",
			EntryNodes: []string{"n1"},
			PublishConfig: &definition.PublishConfig{
				Enabled: true,
				Route:   "/api/published/hello",
				Method:  http.MethodPost,
			},
			Nodes: []definition.Node{
				{
					ID:       "n1",
					Name:     "log",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					Input:    "published",
				},
			},
		},
	}); err != nil {
		t.Fatalf("save version: %v", err)
	}
	if _, err := svc.PublishVersion(context.Background(), "wf-publish-1:v1"); err != nil {
		t.Fatalf("publish version: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/published/hello", "application/json", bytes.NewReader([]byte(`{"hello":"world"}`)))
	if err != nil {
		t.Fatalf("call published route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected published status: %d", resp.StatusCode)
	}
	var body struct {
		Run    *wfruntime.WorkflowRun `json:"run"`
		Source string                 `json:"source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode published response: %v", err)
	}
	if body.Source != "publish" || body.Run == nil || body.Run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected published response: %+v", body)
	}
}

func TestHTTPHandler_PublishedRouteMapsBodyAndReturnsResult(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	const workflowID = "wf-publish-result"
	if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{ID: workflowID, Name: workflowID}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := svc.SaveVersion(context.Background(), &definition.WorkflowVersion{
		ID: workflowID + ":v1", WorkflowID: workflowID, Version: 1, Status: definition.VersionDraft,
		Definition: &definition.WorkflowDefinition{
			ID: workflowID, Name: workflowID, EntryNodes: []string{"return"},
			PublishConfig: &definition.PublishConfig{
				Enabled: true, Route: "/api/published/result", Method: http.MethodPost,
				InputMode: "body", ResponseMode: "result",
			},
			Metadata: map[string]any{"run_form": map[string]any{"fields": []any{
				map[string]any{"key": "name", "kind": "text", "label": publishedTestText("Name"), "required": true},
			}}},
			Nodes: []definition.Node{{
				ID: "return", Name: "return", Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TerminalUnit"},
				InputSpec: &definition.InputSpec{Mode: definition.InputModeReplace, Bindings: []definition.InputBinding{{
					Source: definition.InputSourceVar, From: "name", Required: true,
				}}},
				Params: map[string]any{"response_mode": "variables", "output_name": "greeting"},
			}},
		},
	}); err != nil {
		t.Fatalf("save version: %v", err)
	}
	if _, err := svc.PublishVersion(context.Background(), workflowID+":v1"); err != nil {
		t.Fatalf("publish version: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()
	contractResponse, err := http.Get(server.URL + "/v1/workflows/" + workflowID + "/contract")
	if err != nil {
		t.Fatalf("get published contract: %v", err)
	}
	var contractBody struct {
		Contract *PublishedAPIContract `json:"contract"`
	}
	contractDecodeErr := json.NewDecoder(contractResponse.Body).Decode(&contractBody)
	contractResponse.Body.Close()
	if contractResponse.StatusCode != http.StatusOK || contractDecodeErr != nil || contractBody.Contract == nil {
		t.Fatalf("contract status=%d decode=%v body=%+v", contractResponse.StatusCode, contractDecodeErr, contractBody)
	}
	if len(contractBody.Contract.Inputs) != 1 || contractBody.Contract.Inputs[0].Key != "name" || contractBody.Contract.Example["name"] != "string" {
		t.Fatalf("unexpected contract: %+v", contractBody.Contract)
	}
	if contractBody.Contract.URL != server.URL+"/api/published/result" {
		t.Fatalf("contract url = %q", contractBody.Contract.URL)
	}
	if contractBody.Contract.InvokeURL != server.URL+"/v1/workflows/"+workflowID+"/invoke" ||
		contractBody.Contract.AsyncURL != server.URL+"/v1/workflows/"+workflowID+"/invoke?wait=false" ||
		contractBody.Contract.StreamURL != server.URL+"/v1/runs/{run_id}/stream" {
		t.Fatalf("contract streaming URLs = %+v", contractBody.Contract)
	}

	resp, err := http.Post(server.URL+"/api/published/result", "application/json", bytes.NewReader([]byte(`{"name":"Grace"}`)))
	if err != nil {
		t.Fatalf("call published route: %v", err)
	}
	var body struct {
		Result map[string]any   `json:"result"`
		RunID  string           `json:"run_id"`
		Status wfruntime.Status `json:"status"`
		Source string           `json:"source"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || decodeErr != nil {
		t.Fatalf("published response status=%d decode=%v", resp.StatusCode, decodeErr)
	}
	if body.Result["greeting"] != "Grace" || body.RunID == "" || body.Status != wfruntime.StatusSuccess || body.Source != "publish" {
		t.Fatalf("unexpected published result: %+v", body)
	}

	invokeResponse, err := http.Post(
		server.URL+"/v1/workflows/"+workflowID+"/invoke",
		"application/json",
		bytes.NewReader([]byte(`{"input":{"name":"Lin"}}`)),
	)
	if err != nil {
		t.Fatalf("invoke published workflow: %v", err)
	}
	var invokeBody struct {
		Result map[string]any `json:"result"`
		RunID  string         `json:"run_id"`
	}
	invokeDecodeErr := json.NewDecoder(invokeResponse.Body).Decode(&invokeBody)
	invokeResponse.Body.Close()
	if invokeResponse.StatusCode != http.StatusOK || invokeDecodeErr != nil || invokeBody.Result["greeting"] != "Lin" || invokeBody.RunID == "" {
		t.Fatalf("invoke status=%d decode=%v body=%+v", invokeResponse.StatusCode, invokeDecodeErr, invokeBody)
	}

	asyncResponse, err := http.Post(
		server.URL+"/v1/workflows/"+workflowID+"/invoke?wait=false",
		"application/json",
		bytes.NewReader([]byte(`{"input":{"name":"Ada"}}`)),
	)
	if err != nil {
		t.Fatalf("start asynchronous published workflow: %v", err)
	}
	var accepted struct {
		RunID     string           `json:"run_id"`
		Status    wfruntime.Status `json:"status"`
		Source    string           `json:"source"`
		StreamURL string           `json:"stream_url"`
	}
	asyncDecodeErr := json.NewDecoder(asyncResponse.Body).Decode(&accepted)
	asyncResponse.Body.Close()
	if asyncResponse.StatusCode != http.StatusAccepted || asyncDecodeErr != nil || accepted.RunID == "" || accepted.Source != "publish" || accepted.StreamURL == "" {
		t.Fatalf("async invoke status=%d decode=%v body=%+v", asyncResponse.StatusCode, asyncDecodeErr, accepted)
	}

	streamResponse, err := http.Get(accepted.StreamURL)
	if err != nil {
		t.Fatalf("stream published workflow: %v", err)
	}
	streamBody, streamReadErr := io.ReadAll(streamResponse.Body)
	streamResponse.Body.Close()
	if streamReadErr != nil || streamResponse.StatusCode != http.StatusOK || !strings.HasPrefix(streamResponse.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream status=%d content-type=%q read=%v", streamResponse.StatusCode, streamResponse.Header.Get("Content-Type"), streamReadErr)
	}
	streamText := string(streamBody)
	if !strings.Contains(streamText, "event: ready") ||
		!strings.Contains(streamText, "event: result") ||
		!strings.Contains(streamText, "id: result") ||
		!strings.Contains(streamText, `"greeting":"Ada"`) ||
		!strings.Contains(streamText, `"run_id":"`+accepted.RunID+`"`) {
		t.Fatalf("unexpected SSE stream: %s", streamText)
	}

	reconnectRequest, err := http.NewRequest(http.MethodGet, accepted.StreamURL, nil)
	if err != nil {
		t.Fatalf("create stream reconnect request: %v", err)
	}
	reconnectRequest.Header.Set("Last-Event-ID", "result")
	reconnectResponse, err := http.DefaultClient.Do(reconnectRequest)
	if err != nil {
		t.Fatalf("reconnect completed stream: %v", err)
	}
	reconnectResponse.Body.Close()
	if reconnectResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("completed stream reconnect status=%d", reconnectResponse.StatusCode)
	}

	for name, payload := range map[string]string{
		"array":          `[]`,
		"empty required": `{"name":""}`,
		"invalid json":   `{`,
		"missing field":  `{}`,
		"reserved key":   `{"request":"override"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response, postErr := http.Post(server.URL+"/api/published/result", "application/json", bytes.NewReader([]byte(payload)))
			if postErr != nil {
				t.Fatalf("call published route: %v", postErr)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
}

func TestHTTPHandler_HTTPTriggerRoute(t *testing.T) {
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := svc.SaveWorkflow(context.Background(), &definition.Workflow{
		ID:   "wf-trigger-1",
		Name: "trigger",
	}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := svc.SaveVersion(context.Background(), &definition.WorkflowVersion{
		ID:         "wf-trigger-1:v1",
		WorkflowID: "wf-trigger-1",
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: &definition.WorkflowDefinition{
			ID:         "wf-trigger-1",
			Name:       "trigger",
			EntryNodes: []string{"n1"},
			Triggers: []definition.Trigger{
				{
					ID:      "http-1",
					Type:    definition.TriggerHTTP,
					Enabled: true,
					Config: map[string]any{
						"route":  "/hooks/incoming",
						"method": http.MethodPost,
					},
				},
			},
			Nodes: []definition.Node{
				{
					ID:       "n1",
					Name:     "log",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
					Input:    "triggered",
				},
			},
		},
	}); err != nil {
		t.Fatalf("save version: %v", err)
	}
	if _, err := svc.PublishVersion(context.Background(), "wf-trigger-1:v1"); err != nil {
		t.Fatalf("publish version: %v", err)
	}

	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	resp, err := http.Post(server.URL+"/hooks/incoming", "application/json", bytes.NewReader([]byte(`{"hello":"trigger"}`)))
	if err != nil {
		t.Fatalf("call trigger route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected trigger status: %d", resp.StatusCode)
	}
	var body struct {
		Run    *wfruntime.WorkflowRun `json:"run"`
		Source string                 `json:"source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode trigger response: %v", err)
	}
	if body.Source != "trigger" || body.Run == nil || body.Run.Status != wfruntime.StatusSuccess {
		t.Fatalf("unexpected trigger response: %+v", body)
	}
}

func TestHTTPHandler_CredentialLifecycleAndRunInjection(t *testing.T) {
	const (
		scope      = "workspace-test"
		name       = "PRIVATE_API_TOKEN"
		secret     = "credential-value-that-must-not-leak"
		workflowID = "wf-credential-1"
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	store := credential.NewMemoryStore()
	svc, err := NewService(Options{
		EnableEmbeddedWorker: true,
		Store:                wfruntime.NewMemoryStore(),
		Credentials:          store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(svc).Handler())
	defer server.Close()

	putRequest, err := http.NewRequest(
		http.MethodPut,
		server.URL+"/v1/credentials",
		strings.NewReader(`{"credentials":{"`+name+`":"`+secret+`"}}`),
	)
	if err != nil {
		t.Fatalf("create put request: %v", err)
	}
	putRequest.Header.Set("Content-Type", "application/json")
	putRequest.Header.Set(CredentialScopeHeader, scope)
	putResponse, err := http.DefaultClient.Do(putRequest)
	if err != nil {
		t.Fatalf("put credential: %v", err)
	}
	putBody, err := io.ReadAll(putResponse.Body)
	putResponse.Body.Close()
	if err != nil {
		t.Fatalf("read put response: %v", err)
	}
	if putResponse.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d, body=%s", putResponse.StatusCode, putBody)
	}
	if bytes.Contains(putBody, []byte(secret)) {
		t.Fatal("credential value leaked through put response")
	}

	listRequest, err := http.NewRequest(http.MethodGet, server.URL+"/v1/credentials", nil)
	if err != nil {
		t.Fatalf("create list request: %v", err)
	}
	listRequest.Header.Set(CredentialScopeHeader, scope)
	listResponse, err := http.DefaultClient.Do(listRequest)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	listBody, err := io.ReadAll(listResponse.Body)
	listResponse.Body.Close()
	if err != nil {
		t.Fatalf("read list response: %v", err)
	}
	if listResponse.StatusCode != http.StatusOK || !bytes.Contains(listBody, []byte(name)) {
		t.Fatalf("list status = %d, body=%s", listResponse.StatusCode, listBody)
	}
	if bytes.Contains(listBody, []byte(secret)) {
		t.Fatal("credential value leaked through list response")
	}

	runBody, err := json.Marshal(RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         workflowID,
			Name:       "credential-run",
			EntryNodes: []string{"request"},
			Nodes: []definition.Node{{
				ID:       "request",
				Name:     "request",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "HttpUnit"},
				Params: map[string]any{
					"url":                       target.URL,
					"method":                    http.MethodGet,
					"timeout_ms":                1000,
					"auth_type":                 "bearer",
					"credential_source":         "environment",
					"bearer_token_env":          name,
					"accepted_statuses":         []string{"200-299"},
					"response_mode":             "json",
					"include_response_metadata": false,
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal run request: %v", err)
	}
	runRequest, err := http.NewRequest(http.MethodPost, server.URL+"/v1/runs", bytes.NewReader(runBody))
	if err != nil {
		t.Fatalf("create run request: %v", err)
	}
	runRequest.Header.Set("Content-Type", "application/json")
	runRequest.Header.Set(CredentialScopeHeader, scope)
	runResponse, err := http.DefaultClient.Do(runRequest)
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	resultBody, err := io.ReadAll(runResponse.Body)
	runResponse.Body.Close()
	if err != nil {
		t.Fatalf("read run response: %v", err)
	}
	if runResponse.StatusCode != http.StatusOK {
		t.Fatalf("run status = %d, body=%s", runResponse.StatusCode, resultBody)
	}
	if bytes.Contains(resultBody, []byte(secret)) {
		t.Fatal("credential value leaked through run response")
	}
	var result struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if result.Run == nil || result.Run.Status != wfruntime.StatusSuccess || result.Run.CredentialScope != scope {
		t.Fatalf("unexpected credential run: %+v", result.Run)
	}

	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/credentials/"+name, nil)
	if err != nil {
		t.Fatalf("create delete request: %v", err)
	}
	deleteRequest.Header.Set(CredentialScopeHeader, scope)
	deleteResponse, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatalf("delete credential: %v", err)
	}
	deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", deleteResponse.StatusCode)
	}

	invalidRequest, err := http.NewRequest(
		http.MethodPut,
		server.URL+"/v1/credentials",
		strings.NewReader(`{"credentials":{"TRAILING_TOKEN":"value"}} {}`),
	)
	if err != nil {
		t.Fatalf("create invalid request: %v", err)
	}
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidRequest.Header.Set(CredentialScopeHeader, scope)
	invalidResponse, err := http.DefaultClient.Do(invalidRequest)
	if err != nil {
		t.Fatalf("put invalid credential: %v", err)
	}
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid payload status = %d", invalidResponse.StatusCode)
	}
	if _, err := store.ResolveCredential(context.Background(), scope, "TRAILING_TOKEN"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("invalid payload modified store: %v", err)
	}
}

func TestPublishedWorkflowUsesServerCredentialScope(t *testing.T) {
	const (
		trustedScope = "trusted-workspace"
		otherScope   = "other-workspace"
		name         = "PUBLISHED_API_TOKEN"
		trustedValue = "trusted-value"
		otherValue   = "other-value"
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+trustedValue {
			t.Errorf("published authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	store := credential.NewMemoryStore()
	if _, err := store.PutCredential(context.Background(), trustedScope, name, trustedValue); err != nil {
		t.Fatalf("put trusted credential: %v", err)
	}
	if _, err := store.PutCredential(context.Background(), otherScope, name, otherValue); err != nil {
		t.Fatalf("put other credential: %v", err)
	}
	svc, err := NewService(Options{
		EnableEmbeddedWorker:   true,
		Credentials:            store,
		DefaultCredentialScope: trustedScope,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	version := &definition.WorkflowVersion{
		ID:         "published-credential:v1",
		WorkflowID: "published-credential",
		Version:    1,
		Status:     definition.VersionPublished,
		Definition: &definition.WorkflowDefinition{
			ID:         "published-credential",
			Name:       "published-credential",
			EntryNodes: []string{"request"},
			PublishConfig: &definition.PublishConfig{
				Enabled: true,
				Route:   "/published-credential",
				Method:  http.MethodPost,
			},
			Nodes: []definition.Node{{
				ID:       "request",
				Name:     "request",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "HttpUnit"},
				Params: map[string]any{
					"url":               target.URL,
					"method":            http.MethodGet,
					"timeout_ms":        1000,
					"auth_type":         "bearer",
					"credential_source": "environment",
					"bearer_token_env":  name,
					"response_mode":     "json",
				},
			}},
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/published-credential", strings.NewReader(`{}`))
	request.Header.Set(CredentialScopeHeader, otherScope)
	run, err := svc.RunPublishedVersion(context.Background(), version, request)
	if err != nil {
		t.Fatalf("run published workflow: %v", err)
	}
	if run.Status != wfruntime.StatusSuccess || run.CredentialScope != trustedScope {
		t.Fatalf("published run = %+v", run)
	}

	handler := NewHTTPHandler(svc)
	listRequest := httptest.NewRequest(http.MethodGet, "/v1/credentials", nil)
	listRequest.Header.Set(CredentialScopeHeader, otherScope)
	listResponse := httptest.NewRecorder()
	handler.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusBadRequest {
		t.Fatalf("scope override status = %d, body=%s", listResponse.Code, listResponse.Body.String())
	}
}
