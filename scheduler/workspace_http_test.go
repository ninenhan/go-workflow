package scheduler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ninenhan/go-workflow/core/definition"
)

func TestHTTPHandlerWorkspaceLifecycleAndStrictContract(t *testing.T) {
	service, err := NewService(Options{Workspace: definition.NewMemoryWorkspaceRepository()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHTTPHandler(service).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected missing workspace, got %d", response.StatusCode)
	}
	_ = response.Body.Close()

	body := []byte(`{"schema_version":1,"revision":0,"activeId":"wf-one","folders":[],"entries":[{"id":"wf-one","savedAt":"2026-08-10T08:00:00Z","document":{"definition":{"id":"wf-one","name":"One","nodes":[]}}}],"serviceActions":[]}`)
	request, err := http.NewRequest(http.MethodPut, server.URL+"/v1/workspace", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected workspace creation, got %d", response.StatusCode)
	}
	var payload struct {
		Workspace definition.Workspace `json:"workspace"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if payload.Workspace.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", payload.Workspace.Revision)
	}

	request, _ = http.NewRequest(http.MethodPut, server.URL+"/v1/workspace", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected stale revision conflict, got %d", response.StatusCode)
	}
	_ = response.Body.Close()

	unknown := bytes.Replace(body, []byte(`"revision":0`), []byte(`"revision":0,"frontend_only":true`), 1)
	request, _ = http.NewRequest(http.MethodPut, server.URL+"/v1/workspace", bytes.NewReader(unknown))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected unknown field rejection, got %d", response.StatusCode)
	}
	_ = response.Body.Close()
}

func TestHTTPHandlerExposesWorkflowContract(t *testing.T) {
	service, err := NewService(Options{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/schema/workflow-definition", nil)
	response := httptest.NewRecorder()
	NewHTTPHandler(service).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["contract_version"] != float64(definition.WorkflowContractVersion) {
		t.Fatalf("unexpected contract payload: %#v", payload)
	}
}
