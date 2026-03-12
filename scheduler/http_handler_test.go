package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected workflow status: %d", resp.StatusCode)
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
