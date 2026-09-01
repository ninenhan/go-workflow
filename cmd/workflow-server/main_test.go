package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/scheduler"
)

const workflowServerHelperProcess = "GO_WORKFLOW_SERVER_HELPER_PROCESS"

func TestWorkflowServerHelperProcess(t *testing.T) {
	if os.Getenv(workflowServerHelperProcess) == "1" {
		os.Args = []string{os.Args[0]}
		main()
	}
}

func TestWorkflowServerRuntimeUnitManifestIsDeterministic(t *testing.T) {
	var first bytes.Buffer
	if err := writeRuntimeUnits(&first); err != nil {
		t.Fatalf("write first runtime unit manifest: %v", err)
	}
	var second bytes.Buffer
	if err := writeRuntimeUnits(&second); err != nil {
		t.Fatalf("write second runtime unit manifest: %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("runtime unit manifest is not deterministic:\n%s\n%s", first.String(), second.String())
	}

	var names []string
	if err := json.Unmarshal(first.Bytes(), &names); err != nil {
		t.Fatalf("decode runtime unit manifest: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("runtime unit manifest is empty")
	}
	for index := 1; index < len(names); index++ {
		if names[index-1] >= names[index] {
			t.Fatalf("runtime unit manifest is not strictly sorted: %#v", names)
		}
	}
	if !slices.Contains(names, "TextUnit") || !slices.Contains(names, "ReadableUnit") {
		t.Fatalf("runtime unit manifest omits visible actions: %#v", names)
	}
	if err := writeRuntimeUnits(nil); err == nil {
		t.Fatal("nil runtime unit manifest destination must fail")
	}
}

func TestHostOptionsUseConfigEnvironmentAndCLIOrder(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(configPath, []byte(`version: 1
runtime:
  enabled: false
  listen_host: 127.0.0.2
  port: 58080
  data_directory: file-data
  embedded_worker: false
  automations: false
  automation_period: 4s
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	environment := map[string]string{
		"WORKFLOW_ADDR":              "127.0.0.3:58081",
		"WORKFLOW_ENABLED":           "true",
		"WORKFLOW_AUTOMATION_PERIOD": "5s",
		"WORKFLOW_AUTOMATIONS":       "true",
	}
	config, err := parseHostOptions(
		[]string{
			"--config=" + configPath,
			"--addr=127.0.0.4:58082",
			"--embedded-worker=true",
		},
		func(name string) string { return environment[name] },
		io.Discard,
	)
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if config.Address != "127.0.0.4:58082" {
		t.Fatalf("address = %q", config.Address)
	}
	if !config.Enabled {
		t.Fatal("environment did not override config enabled")
	}
	if config.DisableEmbeddedWorker {
		t.Fatal("CLI did not override config embedded_worker")
	}
	if config.DisableAutomations {
		t.Fatal("environment did not override config automations")
	}
	if config.AutomationPeriod != 5*time.Second {
		t.Fatalf("automation period = %s", config.AutomationPeriod)
	}
	if !filepath.IsAbs(config.DataDirectory) || !strings.HasSuffix(config.DataDirectory, "file-data") {
		t.Fatalf("data directory = %q", config.DataDirectory)
	}
}

func TestHostOptionsRequireExplicitConfigAndStrictEnvironment(t *testing.T) {
	defaults, err := parseHostOptions(nil, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatalf("parse default options: %v", err)
	}
	if !defaults.Enabled {
		t.Fatal("workflow-server must explicitly enable its canonical host")
	}
	if _, err := parseHostOptions(
		[]string{"--config=" + filepath.Join(t.TempDir(), "missing.yml")},
		os.Getenv,
		io.Discard,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit config error = %v", err)
	}
	if _, err := parseHostOptions(
		nil,
		func(name string) string {
			if name == "WORKFLOW_AUTOMATIONS" {
				return "sometimes"
			}
			return ""
		},
		io.Discard,
	); err == nil || !strings.Contains(err.Error(), "WORKFLOW_AUTOMATIONS") {
		t.Fatalf("invalid environment error = %v", err)
	}
	if _, err := parseHostOptions(
		nil,
		func(name string) string {
			if name == "WORKFLOW_ENABLED" {
				return "sometimes"
			}
			return ""
		},
		io.Discard,
	); err == nil || !strings.Contains(err.Error(), "WORKFLOW_ENABLED") {
		t.Fatalf("invalid enabled environment error = %v", err)
	}
}

func TestWorkflowServerHandlerServesWebWithoutInterceptingAPIRoutes(t *testing.T) {
	webDirectory := t.TempDir()
	if err := os.Mkdir(filepath.Join(webDirectory, "assets"), 0o700); err != nil {
		t.Fatalf("create web assets: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(webDirectory, "index.html"),
		[]byte("<!doctype html><title>Workflow V0</title>"),
		0o600,
	); err != nil {
		t.Fatalf("write web entrypoint: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(webDirectory, "assets", "app.js"),
		[]byte("globalThis.workflowV0 = true;"),
		0o600,
	); err != nil {
		t.Fatalf("write web asset: %v", err)
	}

	apiRequests := make([]string, 0, 3)
	api := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		apiRequests = append(apiRequests, request.Method+" "+request.URL.Path)
		response.WriteHeader(http.StatusTeapot)
	})
	handler, err := workflowServerHandler(api, webDirectory)
	if err != nil {
		t.Fatalf("create workflow server handler: %v", err)
	}

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "Workflow V0") {
		t.Fatalf("web root status = %d, body = %q", root.Code, root.Body.String())
	}
	if root.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("web root cache policy = %q", root.Header().Get("Cache-Control"))
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "workflowV0") {
		t.Fatalf("web asset status = %d, body = %q", asset.Code, asset.Body.String())
	}
	if !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("web asset cache policy = %q", asset.Header().Get("Cache-Control"))
	}

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/runs", nil),
		httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/published/customer-api", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusTeapot {
			t.Fatalf("%s %s status = %d", request.Method, request.URL.Path, response.Code)
		}
	}
	wantAPIRequests := []string{
		"GET /v1/runs",
		"POST /",
		"GET /published/customer-api",
	}
	if !reflect.DeepEqual(apiRequests, wantAPIRequests) {
		t.Fatalf("API requests = %#v, want %#v", apiRequests, wantAPIRequests)
	}
}

func TestWorkflowServerHandlerRejectsIncompleteWebBuild(t *testing.T) {
	webDirectory := t.TempDir()
	if _, err := workflowServerHandler(http.NotFoundHandler(), webDirectory); err == nil ||
		!strings.Contains(err.Error(), "web entrypoint") {
		t.Fatalf("missing entrypoint error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDirectory, "index.html"), []byte("index"), 0o600); err != nil {
		t.Fatalf("write web entrypoint: %v", err)
	}
	if _, err := workflowServerHandler(http.NotFoundHandler(), webDirectory); err == nil ||
		!strings.Contains(err.Error(), "web assets") {
		t.Fatalf("missing assets error = %v", err)
	}
}

func TestWorkflowServerCrashRecovery(t *testing.T) {
	address := availableAddress(t)
	dataDirectory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDirectory, 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}

	firstServer, firstOutput := startWorkflowServerProcess(t, address, dataDirectory)
	waitForWorkflowServer(t, address, firstServer, firstOutput)

	longRequest := scheduler.RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "crash-recovery",
			EntryNodes: []string{"wait"},
			Nodes: []definition.Node{{
				ID:       "wait",
				Name:     "Wait",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"},
				Input:    "10000",
			}},
		},
		Run: &wfruntime.WorkflowRun{ID: "client-crash-run"},
	}
	accepted := postRunRequest(t, address, longRequest)
	if accepted.ID != longRequest.Run.ID {
		t.Fatalf("accepted run id = %q", accepted.ID)
	}
	waitForRunStatus(t, address, accepted.ID, wfruntime.StatusRunning)
	waitForNodeStatus(t, address, accepted.ID, "wait", wfruntime.StatusRunning)

	if err := firstServer.Process.Kill(); err != nil {
		t.Fatalf("kill workflow server: %v", err)
	}
	if err := firstServer.Wait(); err == nil {
		t.Fatal("hard-killed workflow server exited successfully")
	}

	secondServer, secondOutput := startWorkflowServerProcess(t, address, dataDirectory)
	defer stopWorkflowServerProcess(t, secondServer, secondOutput)
	waitForWorkflowServer(t, address, secondServer, secondOutput)

	recovered := waitForRunStatus(t, address, accepted.ID, wfruntime.StatusFailed)
	if recovered.RequestFingerprint == "" {
		t.Fatal("recovered run lost its request fingerprint")
	}
	if !strings.Contains(runFailureText(recovered), wfruntime.InterruptedRunMessage) {
		t.Fatalf("recovered run does not explain the interruption: %#v", recovered)
	}

	duplicate := postRunRequest(t, address, longRequest)
	if duplicate.ID != recovered.ID || duplicate.Status != wfruntime.StatusFailed {
		t.Fatalf("duplicate request did not return the recovered run: %#v", duplicate)
	}
	events := loadRunEvents(t, address, recovered.ID)
	started := 0
	for _, event := range events {
		if event.Type == wfruntime.EventRunStarted {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("recovered run started %d times: %#v", started, events)
	}

	freshRequest := scheduler.RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "after-crash",
			EntryNodes: []string{"text"},
			Nodes: []definition.Node{{
				ID:       "text",
				Name:     "Text",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
				Params:   map[string]any{"text": "service recovered"},
			}},
		},
		Run: &wfruntime.WorkflowRun{ID: "client-after-crash"},
	}
	fresh := postRunRequest(t, address, freshRequest)
	completed := waitForRunStatus(t, address, fresh.ID, wfruntime.StatusSuccess)
	if node := completed.NodeRuns["text"]; node == nil || node.Status != wfruntime.StatusSuccess {
		t.Fatalf("fresh run did not execute after restart: %#v", completed)
	}
}

func TestWorkflowServerConcurrentRunSoak(t *testing.T) {
	const (
		runCount        = 64
		submissionsEach = 2
	)

	address := availableAddress(t)
	dataDirectory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDirectory, 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}

	firstServer, firstOutput := startWorkflowServerProcess(t, address, dataDirectory)
	waitForWorkflowServer(t, address, firstServer, firstOutput)

	type submissionResult struct {
		expectedID string
		run        *wfruntime.WorkflowRun
		err        error
	}
	results := make(chan submissionResult, runCount*submissionsEach)
	var submissions sync.WaitGroup
	for index := 0; index < runCount; index++ {
		runID := fmt.Sprintf("client-soak-run-%03d", index)
		for duplicate := 0; duplicate < submissionsEach; duplicate++ {
			submissions.Add(1)
			go func() {
				defer submissions.Done()
				run, err := postRun(address, soakRunRequest(runID))
				results <- submissionResult{expectedID: runID, run: run, err: err}
			}()
		}
	}
	submissions.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			stopWorkflowServerProcess(t, firstServer, firstOutput)
			t.Fatalf("submit %s: %v", result.expectedID, result.err)
		}
		if result.run.ID != result.expectedID {
			stopWorkflowServerProcess(t, firstServer, firstOutput)
			t.Fatalf("submitted run id = %q, want %q", result.run.ID, result.expectedID)
		}
	}

	runIDs := make([]string, 0, runCount)
	for index := 0; index < runCount; index++ {
		runID := fmt.Sprintf("client-soak-run-%03d", index)
		runIDs = append(runIDs, runID)
		assertSuccessfulSoakRun(t, waitForRunStatus(t, address, runID, wfruntime.StatusSuccess))
		assertRunStartedOnce(t, address, runID)
	}

	stopWorkflowServerProcess(t, firstServer, firstOutput)

	secondServer, secondOutput := startWorkflowServerProcess(t, address, dataDirectory)
	defer stopWorkflowServerProcess(t, secondServer, secondOutput)
	waitForWorkflowServer(t, address, secondServer, secondOutput)

	for _, runID := range runIDs {
		run, err := loadRun(address, runID)
		if err != nil {
			t.Fatalf("load persisted run %s: %v", runID, err)
		}
		assertSuccessfulSoakRun(t, run)
		assertRunStartedOnce(t, address, runID)
	}
}

func TestWorkflowServerLocalActionCompositionMatrix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"profile": map[string]any{
				"name":   "Ada",
				"active": true,
			},
		})
	}))
	defer upstream.Close()

	address := availableAddress(t)
	dataDirectory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDirectory, 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}

	firstServer, firstOutput := startWorkflowServerProcess(t, address, dataDirectory)
	waitForWorkflowServer(t, address, firstServer, firstOutput)

	binding := func(from, path string) *definition.InputSpec {
		return &definition.InputSpec{
			Mode: definition.InputModeReplace,
			Bindings: []definition.InputBinding{{
				Source:   definition.InputSourceNode,
				From:     from,
				Path:     path,
				Required: true,
			}},
		}
	}
	linearEdges := func(nodes []definition.Node) []definition.Edge {
		edges := make([]definition.Edge, 0, len(nodes)-1)
		for index := 1; index < len(nodes); index++ {
			edges = append(edges, definition.Edge{
				From: nodes[index-1].ID,
				To:   nodes[index].ID,
			})
		}
		return edges
	}
	terminal := func(id, from, path, outputName string) definition.Node {
		return definition.Node{
			ID:        id,
			Name:      "Return " + outputName,
			Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TerminalUnit"},
			InputSpec: binding(from, path),
			Params: map[string]any{
				"response_mode": "variables",
				"output_name":   outputName,
			},
		}
	}

	type compositionCase struct {
		name     string
		nodes    []definition.Node
		edges    []definition.Edge
		expected any
	}
	cases := []compositionCase{
		{
			name: "text-list-text",
			nodes: []definition.Node{
				{
					ID:       "text",
					Name:     "Text",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "hello workflow"},
				},
				{
					ID:        "upper",
					Name:      "Uppercase",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ChangeCaseUnit"},
					InputSpec: binding("text", ""),
					Params:    map[string]any{"case": "upper"},
				},
				{
					ID:        "replace",
					Name:      "Replace",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ReplaceTextUnit"},
					InputSpec: binding("upper", ""),
					Params: map[string]any{
						"find":        "WORKFLOW",
						"replacement": "SHORTCUTS",
					},
				},
				{
					ID:        "split",
					Name:      "Split",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "SplitTextUnit"},
					InputSpec: binding("replace", ""),
					Params:    map[string]any{"separator_mode": "spaces"},
				},
				{
					ID:        "combine",
					Name:      "Combine",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "CombineTextUnit"},
					InputSpec: binding("split", ""),
					Params: map[string]any{
						"separator_mode":   "custom",
						"custom_separator": " · ",
					},
				},
				terminal("return", "combine", "", "result"),
			},
			expected: map[string]any{"result": "HELLO · SHORTCUTS"},
		},
		{
			name: "list-selection",
			nodes: []definition.Node{
				{
					ID:       "list",
					Name:     "List",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ListUnit"},
					Params:   map[string]any{"items": []any{"alpha", "beta", "gamma"}},
				},
				{
					ID:        "last",
					Name:      "Last Item",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "GetListItemUnit"},
					InputSpec: binding("list", ""),
					Params:    map[string]any{"operation": "last"},
				},
				{
					ID:        "upper",
					Name:      "Uppercase",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ChangeCaseUnit"},
					InputSpec: binding("last", ""),
					Params:    map[string]any{"case": "upper"},
				},
				terminal("return", "upper", "", "selected"),
			},
			expected: map[string]any{"selected": "GAMMA"},
		},
		{
			name: "dictionary-selection",
			nodes: []definition.Node{
				{
					ID:       "dictionary",
					Name:     "Dictionary",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "DictionaryUnit"},
					Params: map[string]any{"entries": []any{
						map[string]any{"key": "name", "type": "text", "value": "Ada"},
						map[string]any{"key": "score", "type": "number", "value": 7},
					}},
				},
				{
					ID:        "name",
					Name:      "Get Name",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "GetDictionaryValueUnit"},
					InputSpec: binding("dictionary", ""),
					Params:    map[string]any{"key": "name"},
				},
				{
					ID:        "replace",
					Name:      "Replace Name",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ReplaceTextUnit"},
					InputSpec: binding("name", ""),
					Params: map[string]any{
						"find":        "Ada",
						"replacement": "Grace",
					},
				},
				terminal("return", "replace", "", "name"),
			},
			expected: map[string]any{"name": "Grace"},
		},
		{
			name: "date-transform",
			nodes: []definition.Node{
				{
					ID:       "date",
					Name:     "Date",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "DateUnit"},
					Params: map[string]any{
						"mode":     "specified",
						"date":     "2026-07-28",
						"time":     "09:30",
						"timezone": "UTC",
					},
				},
				{
					ID:        "adjust",
					Name:      "Add Days",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "AdjustDateUnit"},
					InputSpec: binding("date", ""),
					Params: map[string]any{
						"operation": "add",
						"amount":    2,
						"unit":      "day",
						"timezone":  "UTC",
					},
				},
				{
					ID:        "format",
					Name:      "Format Date",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "FormatDateUnit"},
					InputSpec: binding("adjust", ""),
					Params:    map[string]any{"format": "date"},
				},
				terminal("return", "format", "", "date"),
			},
			expected: map[string]any{"date": "2026-07-30"},
		},
		{
			name: "script-path",
			nodes: []definition.Node{
				{
					ID:       "calculate",
					Name:     "Calculate",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "CalculateUnit"},
					Params: map[string]any{
						"left":      6,
						"operation": "multiply",
						"right":     7,
					},
				},
				{
					ID:        "script",
					Name:      "Double",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ScriptUnit"},
					InputSpec: binding("calculate", ""),
					Params: map[string]any{
						"language": "javascript",
						"script":   "input * 2",
					},
				},
				terminal("return", "script", "$$", "answer"),
			},
			expected: map[string]any{"answer": float64(84)},
		},
		{
			name: "magic-parameter",
			nodes: []definition.Node{
				{
					ID:       "left",
					Name:     "Left Number",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "6"},
				},
				{
					ID:       "calculate",
					Name:     "Calculate",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "CalculateUnit"},
					ParamBindings: map[string]definition.InputBinding{
						"left": {
							Source:   definition.InputSourceNode,
							From:     "left",
							Required: true,
						},
					},
					Params: map[string]any{
						"operation": "multiply",
						"right":     7,
					},
				},
				terminal("return", "calculate", "", "answer"),
			},
			expected: map[string]any{"answer": float64(42)},
		},
		{
			name: "api-body-path",
			nodes: []definition.Node{
				{
					ID:       "api",
					Name:     "Call API",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "HttpUnit"},
					Params: map[string]any{
						"url":           upstream.URL,
						"method":        http.MethodGet,
						"timeout_ms":    1000,
						"response_mode": "json",
					},
				},
				{
					ID:        "profile",
					Name:      "Get Profile",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "GetDictionaryValueUnit"},
					InputSpec: binding("api", "body"),
					Params:    map[string]any{"key": "profile"},
				},
				{
					ID:        "name",
					Name:      "Get Name",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "GetDictionaryValueUnit"},
					InputSpec: binding("profile", ""),
					Params:    map[string]any{"key": "name"},
				},
				{
					ID:        "upper",
					Name:      "Uppercase",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ChangeCaseUnit"},
					InputSpec: binding("name", ""),
					Params:    map[string]any{"case": "upper"},
				},
				terminal("return", "upper", "", "name"),
			},
			expected: map[string]any{"name": "ADA"},
		},
		{
			name: "api-discovered-output",
			nodes: []definition.Node{
				{
					ID:       "api",
					Name:     "Call API",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "HttpUnit"},
					Params: map[string]any{
						"url":           upstream.URL,
						"method":        http.MethodGet,
						"timeout_ms":    1000,
						"response_mode": "json",
						"response_fields": []any{
							map[string]any{"key": "name", "value": "profile.name", "type": "text"},
						},
					},
				},
				{
					ID:        "upper",
					Name:      "Uppercase",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ChangeCaseUnit"},
					InputSpec: binding("api", "name"),
					Params:    map[string]any{"case": "upper"},
				},
				terminal("return", "upper", "", "name"),
			},
			expected: map[string]any{"name": "ADA"},
		},
		{
			name: "workflow-variable",
			nodes: []definition.Node{
				{
					ID:       "text",
					Name:     "Number",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "6"},
				},
				{
					ID:        "save",
					Name:      "Save Number",
					Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "SetEnvUnit"},
					InputSpec: binding("text", ""),
					Params: map[string]any{
						"variable_name": "left",
						"mode":          "set",
					},
				},
				{
					ID:       "calculate",
					Name:     "Calculate",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "CalculateUnit"},
					ParamBindings: map[string]definition.InputBinding{
						"left": {
							Source:    definition.InputSourceVar,
							From:      "left",
							Required:  true,
							Transform: "float(Value)",
						},
					},
					Params: map[string]any{
						"operation": "multiply",
						"right":     7,
					},
				},
				terminal("return", "calculate", "", "answer"),
			},
			expected: map[string]any{"answer": float64(42)},
		},
		{
			name: "combined-magic-input",
			nodes: []definition.Node{
				{
					ID:       "first",
					Name:     "First Name",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "Ada"},
				},
				{
					ID:       "last",
					Name:     "Last Name",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "Lovelace"},
				},
				{
					ID:       "combine",
					Name:     "Combine Names",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "ScriptUnit"},
					InputSpec: &definition.InputSpec{
						Mode: definition.InputModeObject,
						Bindings: []definition.InputBinding{
							{
								Source:   definition.InputSourceNode,
								From:     "first",
								As:       "first",
								Required: true,
							},
							{
								Source:   definition.InputSourceNode,
								From:     "last",
								As:       "last",
								Required: true,
							},
						},
					},
					Params: map[string]any{
						"language": "javascript",
						"script":   "input.first + ' ' + input.last",
					},
				},
				terminal("return", "combine", "$$", "name"),
			},
			edges: []definition.Edge{
				{From: "first", To: "last"},
				{From: "first", To: "combine"},
				{From: "last", To: "combine"},
				{From: "combine", To: "return"},
			},
			expected: map[string]any{"name": "Ada Lovelace"},
		},
	}

	versionIDs := make(map[string]string, len(cases))
	for index := range cases {
		testCase := &cases[index]
		testCaseID := "composition-" + testCase.name
		edges := testCase.edges
		if len(edges) == 0 {
			edges = linearEdges(testCase.nodes)
		}
		workflowDefinition := &definition.WorkflowDefinition{
			ID:         testCaseID,
			Name:       "Composition " + testCase.name,
			EntryNodes: []string{testCase.nodes[0].ID},
			Nodes:      testCase.nodes,
			Edges:      edges,
			PublishConfig: &definition.PublishConfig{
				Enabled:      true,
				Route:        "/api/composition/" + testCase.name,
				Method:       http.MethodPost,
				InputMode:    "body",
				ResponseMode: "result",
			},
		}
		versionIDs[testCase.name] = persistPublishedWorkflowDefinition(
			t,
			address,
			workflowDefinition,
		)
		accepted := postRunRequest(t, address, scheduler.RunRequest{
			Definition: workflowDefinition,
			Run:        &wfruntime.WorkflowRun{ID: testCaseID},
		})
		if accepted.ID != testCaseID {
			t.Fatalf("%s accepted run id = %q", testCase.name, accepted.ID)
		}
	}

	assertCompositionRun := func(
		t *testing.T,
		address string,
		testCase *compositionCase,
		runID string,
	) {
		t.Helper()
		completed := waitForRunStatus(t, address, runID, wfruntime.StatusSuccess)
		lastNode := testCase.nodes[len(testCase.nodes)-1]
		if !reflect.DeepEqual(completed.NodeRuns[lastNode.ID].Result, testCase.expected) {
			t.Fatalf(
				"%s final result = %#v, want %#v",
				testCase.name,
				completed.NodeRuns[lastNode.ID].Result,
				testCase.expected,
			)
		}
		for _, node := range testCase.nodes {
			if completed.NodeRuns[node.ID] == nil || completed.NodeRuns[node.ID].Status != wfruntime.StatusSuccess {
				t.Fatalf("%s node %s = %#v", testCase.name, node.ID, completed.NodeRuns[node.ID])
			}
		}
		assertRunStartedOnce(t, address, runID)
	}
	assertMatrix := func(t *testing.T, address string) {
		t.Helper()
		for index := range cases {
			testCase := &cases[index]
			assertCompositionRun(t, address, testCase, "composition-"+testCase.name)
		}
	}
	assertMatrix(t, address)
	stopWorkflowServerProcess(t, firstServer, firstOutput)

	secondServer, secondOutput := startWorkflowServerProcess(t, address, dataDirectory)
	defer stopWorkflowServerProcess(t, secondServer, secondOutput)
	waitForWorkflowServer(t, address, secondServer, secondOutput)
	assertMatrix(t, address)

	const persistedReplayCount = 3
	for replay := 1; replay <= persistedReplayCount; replay++ {
		for index := range cases {
			testCase := &cases[index]
			runID := fmt.Sprintf("persisted-%s-%d", testCase.name, replay)
			accepted := postRunRequest(t, address, scheduler.RunRequest{
				VersionID: versionIDs[testCase.name],
				Run:       &wfruntime.WorkflowRun{ID: runID},
			})
			if accepted.ID != runID {
				t.Fatalf("%s accepted persisted run id = %q", testCase.name, accepted.ID)
			}
		}
	}
	for replay := 1; replay <= persistedReplayCount; replay++ {
		for index := range cases {
			testCase := &cases[index]
			assertCompositionRun(
				t,
				address,
				testCase,
				fmt.Sprintf("persisted-%s-%d", testCase.name, replay),
			)
		}
	}
	for index := range cases {
		testCase := &cases[index]
		var invoked struct {
			Result any              `json:"result"`
			RunID  string           `json:"run_id"`
			Status wfruntime.Status `json:"status"`
			Source string           `json:"source"`
		}
		postJSONRequest(
			t,
			address,
			"/v1/workflows/composition-"+testCase.name+"/invoke",
			map[string]any{"input": map[string]any{}},
			&invoked,
		)
		if invoked.RunID == "" ||
			invoked.Status != wfruntime.StatusSuccess ||
			invoked.Source != "publish" ||
			!reflect.DeepEqual(invoked.Result, testCase.expected) {
			t.Fatalf("%s published invocation = %#v, want result %#v", testCase.name, invoked, testCase.expected)
		}
		assertCompositionRun(t, address, testCase, invoked.RunID)
	}
}

func TestWorkflowServerGeneratedLongChainCorpus(t *testing.T) {
	address := availableAddress(t)
	dataDirectory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDirectory, 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	server, output := startWorkflowServerProcess(t, address, dataDirectory)
	defer stopWorkflowServerProcess(t, server, output)
	waitForWorkflowServer(t, address, server, output)

	binding := func(from string) *definition.InputSpec {
		return &definition.InputSpec{
			Mode: definition.InputModeReplace,
			Bindings: []definition.InputBinding{{
				Source:   definition.InputSourceNode,
				From:     from,
				Required: true,
			}},
		}
	}
	node := func(id, unit string, input *definition.InputSpec, params map[string]any) definition.Node {
		return definition.Node{
			ID:        id,
			Name:      id,
			Executor:  definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: unit},
			InputSpec: input,
			Params:    params,
		}
	}
	corpus := []struct {
		name  string
		value string
	}{
		{name: "simplified-chinese", value: "中文|流程"},
		{name: "traditional-chinese", value: "繁體|流程"},
		{name: "english", value: "English|Workflow"},
		{name: "japanese", value: "日本語|ショートカット"},
		{name: "spanish", value: "Español|Flujo"},
		{name: "tibetan", value: "བོད་ཡིག|ལས་རྒྱུན"},
	}
	type generatedCase struct {
		runID    string
		nodes    []definition.Node
		expected map[string]any
	}
	generated := make([]generatedCase, 0, len(corpus)*4)
	for _, sample := range corpus {
		parts := strings.Split(sample.value, "|")
		for _, operation := range []string{"first", "last"} {
			for _, firstCase := range []string{"upper", "lower"} {
				selectedIndex := 0
				if operation == "last" {
					selectedIndex = len(parts) - 1
				}
				selected := parts[selectedIndex]
				secondCase := "lower"
				if firstCase == "lower" {
					secondCase = "upper"
				}
				if firstCase == "upper" {
					selected = strings.ToUpper(selected)
				} else {
					selected = strings.ToLower(selected)
				}
				finalValue := strings.ToLower("picked")
				if secondCase == "upper" {
					finalValue = strings.ToUpper("picked")
				}
				runID := fmt.Sprintf("generated-%s-%s-%s", sample.name, operation, firstCase)
				nodes := []definition.Node{
					node("text", "TextUnit", nil, map[string]any{"text": sample.value}),
					node("split-initial", "SplitTextUnit", binding("text"), map[string]any{
						"separator_mode": "custom", "custom_separator": "|",
					}),
					node("combine-marker", "CombineTextUnit", binding("split-initial"), map[string]any{
						"separator_mode": "custom", "custom_separator": "~",
					}),
					node("restore-separator", "ReplaceTextUnit", binding("combine-marker"), map[string]any{
						"find": "~", "replacement": "|",
					}),
					node("first-case", "ChangeCaseUnit", binding("restore-separator"), map[string]any{
						"case": firstCase,
					}),
					node("split-restored", "SplitTextUnit", binding("first-case"), map[string]any{
						"separator_mode": "custom", "custom_separator": "|",
					}),
					node("select-item", "GetListItemUnit", binding("split-restored"), map[string]any{
						"operation": operation,
					}),
					node("replace-item", "ReplaceTextUnit", binding("select-item"), map[string]any{
						"find": selected, "replacement": "picked",
					}),
					node("split-word", "SplitTextUnit", binding("replace-item"), map[string]any{
						"separator_mode": "spaces",
					}),
					node("combine-word", "CombineTextUnit", binding("split-word"), map[string]any{
						"separator_mode": "custom", "custom_separator": ":",
					}),
					node("second-case", "ChangeCaseUnit", binding("combine-word"), map[string]any{
						"case": secondCase,
					}),
					node("return", "TerminalUnit", binding("second-case"), map[string]any{
						"response_mode": "variables", "output_name": "result",
					}),
				}
				edges := make([]definition.Edge, 0, len(nodes)-1)
				for index := 1; index < len(nodes); index++ {
					edges = append(edges, definition.Edge{From: nodes[index-1].ID, To: nodes[index].ID})
				}
				accepted := postRunRequest(t, address, scheduler.RunRequest{
					Definition: &definition.WorkflowDefinition{
						ID:         runID,
						Name:       runID,
						EntryNodes: []string{"text"},
						Nodes:      nodes,
						Edges:      edges,
					},
					Run: &wfruntime.WorkflowRun{ID: runID},
				})
				if accepted.ID != runID {
					t.Fatalf("%s accepted run id = %q", runID, accepted.ID)
				}
				generated = append(generated, generatedCase{
					runID:    runID,
					nodes:    nodes,
					expected: map[string]any{"result": finalValue},
				})
			}
		}
	}

	for _, testCase := range generated {
		completed := waitForRunStatus(t, address, testCase.runID, wfruntime.StatusSuccess)
		for _, workflowNode := range testCase.nodes {
			nodeRun := completed.NodeRuns[workflowNode.ID]
			if nodeRun == nil || nodeRun.Status != wfruntime.StatusSuccess {
				t.Fatalf("%s node %s = %#v", testCase.runID, workflowNode.ID, nodeRun)
			}
		}
		if result := completed.NodeRuns["return"].Result; !reflect.DeepEqual(result, testCase.expected) {
			t.Fatalf("%s result = %#v, want %#v", testCase.runID, result, testCase.expected)
		}
		assertRunStartedOnce(t, address, testCase.runID)
	}
}

func TestWorkflowServerRejectsInvalidDefinitionsWithoutPersistingRuns(t *testing.T) {
	address := availableAddress(t)
	dataDirectory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDirectory, 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	server, output := startWorkflowServerProcess(t, address, dataDirectory)
	defer stopWorkflowServerProcess(t, server, output)
	waitForWorkflowServer(t, address, server, output)

	unitNode := func(id string) definition.Node {
		return definition.Node{
			ID:       id,
			Name:     id,
			Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "LogUnit"},
		}
	}
	nodeBinding := func(from string) *definition.InputSpec {
		return &definition.InputSpec{
			Mode: definition.InputModeReplace,
			Bindings: []definition.InputBinding{{
				Source: definition.InputSourceNode, From: from, Required: true,
			}},
		}
	}
	tests := []struct {
		name       string
		definition *definition.WorkflowDefinition
		want       string
	}{
		{
			name: "duplicate-node",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{unitNode("same"), unitNode("same")},
			},
			want: "duplicated node id",
		},
		{
			name: "dag-cycle",
			definition: &definition.WorkflowDefinition{
				EntryNodes: []string{"a"},
				Nodes:      []definition.Node{unitNode("a"), unitNode("b")},
				Edges:      []definition.Edge{{From: "a", To: "b"}, {From: "b", To: "a"}},
			},
			want: "workflow contains cycle",
		},
		{
			name: "missing-executor",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{{ID: "missing", Name: "missing"}},
			},
			want: "missing executor type",
		},
		{
			name: "unknown-input-source",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{
					unitNode("source"),
					func() definition.Node {
						value := unitNode("target")
						value.InputSpec = nodeBinding("unknown")
						return value
					}(),
				},
				Edges: []definition.Edge{{From: "source", To: "target"}},
			},
			want: "references unknown node unknown",
		},
		{
			name: "indirect-input-source",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{
					unitNode("source"),
					unitNode("middle"),
					func() definition.Node {
						value := unitNode("target")
						value.InputSpec = nodeBinding("source")
						return value
					}(),
				},
				Edges: []definition.Edge{{From: "source", To: "middle"}, {From: "middle", To: "target"}},
			},
			want: "is not a direct dependency",
		},
		{
			name: "unsupported-input-mode",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{
					unitNode("source"),
					func() definition.Node {
						value := unitNode("target")
						value.InputSpec = nodeBinding("source")
						value.InputSpec.Mode = definition.InputMode("merge")
						return value
					}(),
				},
				Edges: []definition.Edge{{From: "source", To: "target"}},
			},
			want: "unsupported input mode",
		},
		{
			name: "parameter-binding-template-conflict",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{
					unitNode("source"),
					func() definition.Node {
						value := unitNode("target")
						value.ParamBindings = map[string]definition.InputBinding{
							"message": {Source: definition.InputSourceNode, From: "source"},
						}
						value.ParamTemplates = map[string]definition.ParamTemplate{
							"message": {Segments: []definition.ParamTemplateSegment{{Type: "text", Value: "fixed"}}},
						}
						return value
					}(),
				},
				Edges: []definition.Edge{{From: "source", To: "target"}},
			},
			want: "cannot use both a binding and a template",
		},
		{
			name: "each-loop-count-binding",
			definition: &definition.WorkflowDefinition{
				Nodes: []definition.Node{func() definition.Node {
					value := unitNode("repeat")
					value.Loop = &definition.LoopPolicy{
						Mode:          "each",
						MaxIterations: 2,
						CountBinding: &definition.InputBinding{
							Source: definition.InputSourceVar, From: "count",
						},
					}
					return value
				}()},
			},
			want: "each-item loop cannot define count_binding",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.definition.ID = "invalid-" + testCase.name
			testCase.definition.Name = testCase.definition.ID
			runID := testCase.definition.ID + "-run"
			request := scheduler.RunRequest{
				Definition: testCase.definition,
				Run:        &wfruntime.WorkflowRun{ID: runID},
			}
			for _, path := range []string{"/v1/validate", "/v1/runs?wait=false"} {
				errorText := postJSONError(t, address, path, request)
				if !strings.Contains(errorText, testCase.want) {
					t.Fatalf("%s error = %q, want %q", path, errorText, testCase.want)
				}
			}
			if _, err := loadRun(address, runID); err == nil {
				t.Fatalf("invalid definition persisted run %s", runID)
			}
		})
	}
	runs, err := loadRuns(address)
	if err != nil {
		t.Fatalf("list runs after invalid definitions: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("invalid definitions polluted run history: %#v", runs)
	}
	validRunID := "valid-after-invalid-definitions"
	postRunRequest(t, address, scheduler.RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "valid-after-invalid",
			Name:       "Valid after invalid",
			EntryNodes: []string{"text"},
			Nodes: []definition.Node{{
				ID:       "text",
				Name:     "Text",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
				Params:   map[string]any{"text": "service remains healthy"},
			}},
		},
		Run: &wfruntime.WorkflowRun{ID: validRunID},
	})
	completed := waitForRunStatus(t, address, validRunID, wfruntime.StatusSuccess)
	if nodeRun := completed.NodeRuns["text"]; nodeRun == nil ||
		nodeRun.Status != wfruntime.StatusSuccess ||
		nodeRun.Result != "service remains healthy" {
		t.Fatalf("valid run after invalid definitions = %#v", completed)
	}
	assertRunStartedOnce(t, address, validRunID)
}

func TestWorkflowServerExternalServiceFailureIsolation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/unauthorized":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"error":"unauthorized"}`))
		case "/disconnect":
			connection, _, err := response.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
		case "/slow":
			select {
			case <-time.After(500 * time.Millisecond):
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(`{"late":true}`))
			case <-request.Context().Done():
			}
		case "/large":
			response.Header().Set("Content-Type", "application/octet-stream")
			_, _ = response.Write(bytes.Repeat([]byte("x"), (10<<20)+1))
		case "/healthy":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"healthy":true}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer upstream.Close()

	address := availableAddress(t)
	dataDirectory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDirectory, 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	server, output := startWorkflowServerProcess(t, address, dataDirectory)
	defer stopWorkflowServerProcess(t, server, output)
	waitForWorkflowServer(t, address, server, output)

	failures := []struct {
		name      string
		path      string
		timeoutMS int
		errorText string
	}{
		{name: "unauthorized", path: "/unauthorized", timeoutMS: 1000, errorText: "HttpUnit: http 401"},
		{name: "disconnect", path: "/disconnect", timeoutMS: 1000, errorText: "HttpUnit: send request"},
		{name: "timeout", path: "/slow", timeoutMS: 20, errorText: "context deadline exceeded"},
		{name: "large response", path: "/large", timeoutMS: 1000, errorText: "HttpUnit: response exceeds 10485760 bytes"},
	}
	for index, failure := range failures {
		runID := fmt.Sprintf("external-failure-%d", index)
		accepted := postRunRequest(t, address, httpRunRequest(runID, upstream.URL+failure.path, failure.timeoutMS))
		failed := waitForRunStatus(t, address, accepted.ID, wfruntime.StatusFailed)
		node := failed.NodeRuns["request"]
		if node == nil || !strings.Contains(node.Error, failure.errorText) {
			t.Fatalf("%s error = %#v", failure.name, node)
		}
		if node.Result != nil {
			t.Fatalf("%s retained a failed response result: %#v", failure.name, node.Result)
		}
		assertRunStartedOnce(t, address, runID)
	}

	healthy := postRunRequest(t, address, httpRunRequest(
		"external-failure-recovery",
		upstream.URL+"/healthy",
		1000,
	))
	completed := waitForRunStatus(t, address, healthy.ID, wfruntime.StatusSuccess)
	node := completed.NodeRuns["request"]
	if node == nil || node.Status != wfruntime.StatusSuccess {
		t.Fatalf("healthy request did not run after upstream failures: %#v", completed)
	}
	result, ok := node.Result.(map[string]any)
	if !ok || result["status"] != float64(http.StatusOK) && result["status"] != http.StatusOK {
		t.Fatalf("healthy response result = %#v", node.Result)
	}
	assertRunStartedOnce(t, address, healthy.ID)
}

func httpRunRequest(runID, targetURL string, timeoutMS int) scheduler.RunRequest {
	return scheduler.RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "external-service-failure-isolation",
			Name:       "External service failure isolation",
			EntryNodes: []string{"request"},
			Nodes: []definition.Node{{
				ID:       "request",
				Name:     "Request",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "HttpUnit"},
				Params: map[string]any{
					"url":           targetURL,
					"method":        http.MethodGet,
					"timeout_ms":    timeoutMS,
					"response_mode": "auto",
				},
			}},
		},
		Run: &wfruntime.WorkflowRun{ID: runID},
	}
}

func soakRunRequest(runID string) scheduler.RunRequest {
	return scheduler.RunRequest{
		Definition: &definition.WorkflowDefinition{
			ID:         "concurrent-soak",
			Name:       "Concurrent soak",
			EntryNodes: []string{"start"},
			Nodes: []definition.Node{
				{
					ID:       "start",
					Name:     "Start",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "ready"},
				},
				{
					ID:       "pause",
					Name:     "Pause",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TimeoutUnit"},
					Params:   map[string]any{"duration_ms": 2},
				},
				{
					ID:       "finish",
					Name:     "Finish",
					Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "TextUnit"},
					Params:   map[string]any{"text": "complete"},
				},
			},
			Edges: []definition.Edge{
				{From: "start", To: "pause"},
				{From: "pause", To: "finish"},
			},
		},
		Run: &wfruntime.WorkflowRun{ID: runID},
	}
}

func assertSuccessfulSoakRun(t *testing.T, run *wfruntime.WorkflowRun) {
	t.Helper()
	if run == nil || run.Status != wfruntime.StatusSuccess {
		t.Fatalf("soak run did not succeed: %#v", run)
	}
	if run.RequestFingerprint == "" {
		t.Fatalf("soak run %s lost its request fingerprint", run.ID)
	}
	for _, nodeID := range []string{"start", "pause", "finish"} {
		node := run.NodeRuns[nodeID]
		if node == nil || node.Status != wfruntime.StatusSuccess {
			t.Fatalf("soak run %s node %s = %#v", run.ID, nodeID, node)
		}
	}
}

func assertRunStartedOnce(t *testing.T, address, runID string) {
	t.Helper()
	started := 0
	for _, event := range loadRunEvents(t, address, runID) {
		if event.Type == wfruntime.EventRunStarted {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("run %s started %d times", runID, started)
	}
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve workflow server address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release workflow server address: %v", err)
	}
	return address
}

func startWorkflowServerProcess(t *testing.T, address, dataDirectory string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	command := exec.Command(os.Args[0], "-test.run=^TestWorkflowServerHelperProcess$")
	command.Env = append(os.Environ(),
		workflowServerHelperProcess+"=1",
		"WORKFLOW_MODE=headless",
		"WORKFLOW_ADDR="+address,
		"WORKFLOW_DATA_DIR="+dataDirectory,
		"WORKFLOW_WEB_DIR=",
		"WORKFLOW_DESKTOP_TOKEN=",
	)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatalf("start workflow server: %v", err)
	}
	return command, output
}

func waitForWorkflowServer(t *testing.T, address string, command *exec.Cmd, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get("http://" + address + "/v1/runs")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if command.ProcessState != nil {
			t.Fatalf("workflow server exited during startup: %s", output.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("workflow server did not start: %s", output.String())
}

func stopWorkflowServerProcess(t *testing.T, command *exec.Cmd, output *bytes.Buffer) {
	t.Helper()
	if command == nil || command.Process == nil || command.ProcessState != nil {
		return
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt workflow server: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("workflow server shutdown failed: %v\n%s", err, output.String())
		}
		if strings.Contains(output.String(), "shutdown workflow API:") ||
			strings.Contains(output.String(), "shutdown workflow scheduler:") {
			t.Fatalf("workflow server exceeded an internal shutdown deadline:\n%s", output.String())
		}
	case <-time.After(15 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Fatalf("workflow server did not stop cleanly: %s", output.String())
	}
}

func persistPublishedWorkflowDefinition(
	t *testing.T,
	address string,
	workflowDefinition *definition.WorkflowDefinition,
) string {
	t.Helper()
	if workflowDefinition == nil {
		t.Fatal("persist workflow definition is nil")
	}
	postJSONRequest(t, address, "/v1/workflows", definition.Workflow{
		ID:   workflowDefinition.ID,
		Name: workflowDefinition.Name,
	}, nil)

	var created struct {
		Version *definition.WorkflowVersion `json:"version"`
	}
	postJSONRequest(
		t,
		address,
		"/v1/workflows/"+workflowDefinition.ID+"/versions",
		map[string]any{"definition": workflowDefinition},
		&created,
	)
	if created.Version == nil || created.Version.ID == "" {
		t.Fatalf("create version for %s returned no version", workflowDefinition.ID)
	}
	postJSONRequest(
		t,
		address,
		"/v1/workflow-versions/"+created.Version.ID+"/publish",
		struct{}{},
		nil,
	)
	return created.Version.ID
}

func postJSONRequest(t *testing.T, address, path string, request, response any) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal POST %s: %v", path, err)
	}
	httpResponse, err := http.Post(
		"http://"+address+path,
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"POST %s status = %d: %s",
			path,
			httpResponse.StatusCode,
			readBody(httpResponse.Body),
		)
	}
	if response != nil {
		if err := json.NewDecoder(httpResponse.Body).Decode(response); err != nil {
			t.Fatalf("decode POST %s: %v", path, err)
		}
	}
}

func postJSONError(t *testing.T, address, path string, request any) string {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal invalid POST %s: %v", path, err)
	}
	httpResponse, err := http.Post(
		"http://"+address+path,
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("invalid POST %s: %v", path, err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"invalid POST %s status = %d: %s",
			path,
			httpResponse.StatusCode,
			readBody(httpResponse.Body),
		)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(httpResponse.Body).Decode(&payload); err != nil {
		t.Fatalf("decode invalid POST %s: %v", path, err)
	}
	if payload.Error == "" {
		t.Fatalf("invalid POST %s returned no error", path)
	}
	return payload.Error
}

func postRunRequest(t *testing.T, address string, request scheduler.RunRequest) *wfruntime.WorkflowRun {
	t.Helper()
	run, err := postRun(address, request)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func postRun(address string, request scheduler.RunRequest) (*wfruntime.WorkflowRun, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal run request: %w", err)
	}
	response, err := http.Post(
		"http://"+address+"/v1/runs?wait=false",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("post run request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("run request status = %d: %s", response.StatusCode, readBody(response.Body))
	}
	var payload struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode run response: %w", err)
	}
	if payload.Run == nil {
		return nil, fmt.Errorf("run response is empty")
	}
	return payload.Run, nil
}

func waitForRunStatus(t *testing.T, address, runID string, status wfruntime.Status) *wfruntime.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var latest *wfruntime.WorkflowRun
	for time.Now().Before(deadline) {
		run, err := loadRun(address, runID)
		if err == nil {
			latest = run
			if run.Status == status {
				return run
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach %s: %#v", runID, status, latest)
	return nil
}

func waitForNodeStatus(t *testing.T, address, runID, nodeID string, status wfruntime.Status) *wfruntime.WorkflowRun {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var latest *wfruntime.WorkflowRun
	for time.Now().Before(deadline) {
		run, err := loadRun(address, runID)
		if err == nil {
			latest = run
			if node := run.NodeRuns[nodeID]; node != nil && node.Status == status {
				return run
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("node %s in run %s did not reach %s: %#v", nodeID, runID, status, latest)
	return nil
}

func loadRun(address, runID string) (*wfruntime.WorkflowRun, error) {
	response, err := http.Get("http://" + address + "/v1/runs/" + runID)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("load run status = %d: %s", response.StatusCode, readBody(response.Body))
	}
	var payload struct {
		Run *wfruntime.WorkflowRun `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Run == nil {
		return nil, fmt.Errorf("run response is empty")
	}
	return payload.Run, nil
}

func loadRuns(address string) ([]*wfruntime.WorkflowRun, error) {
	response, err := http.Get("http://" + address + "/v1/runs")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list runs status = %d: %s", response.StatusCode, readBody(response.Body))
	}
	var payload struct {
		Runs []*wfruntime.WorkflowRun `json:"runs"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Runs, nil
}

func loadRunEvents(t *testing.T, address, runID string) []wfruntime.RunEvent {
	t.Helper()
	response, err := http.Get("http://" + address + "/v1/runs/" + runID + "/events")
	if err != nil {
		t.Fatalf("load run events: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("load run events status = %d: %s", response.StatusCode, readBody(response.Body))
	}
	var payload struct {
		Events []wfruntime.RunEvent `json:"events"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode run events: %v", err)
	}
	return payload.Events
}

func runFailureText(run *wfruntime.WorkflowRun) string {
	if run == nil {
		return ""
	}
	messages := make([]string, 0, len(run.NodeRuns))
	for _, node := range run.NodeRuns {
		if node != nil && node.Error != "" {
			messages = append(messages, node.Error)
		}
	}
	return strings.Join(messages, "\n")
}

func readBody(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 4<<10))
	return string(data)
}
