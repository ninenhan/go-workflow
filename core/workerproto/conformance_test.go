package workerproto

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

func TestExecuteRequestFixture(t *testing.T) {
	var req ExecuteRequest
	readFixture(t, "execute_request.json", &req)
	if req.Task.DispatchID != "dispatch-01" || req.Task.ExecutorRef != "SendEmail" {
		t.Fatalf("unexpected task identity: %+v", req.Task)
	}
	if req.Task.Timeout != 1500*time.Millisecond {
		t.Fatalf("duration is not nanoseconds: %s", req.Task.Timeout)
	}
	if req.Task.PollInterval != 250*time.Millisecond || req.Task.HeartbeatFreq != 2*time.Second {
		t.Fatalf("unexpected polling durations: %s %s", req.Task.PollInterval, req.Task.HeartbeatFreq)
	}
	expected := time.Date(2026, 8, 28, 12, 34, 56, 123456789, time.UTC)
	if !req.Task.Deadline.Equal(expected) {
		t.Fatalf("timestamp is not RFC3339Nano: %s", req.Task.Deadline)
	}
	roundTripFixture(t, req)
}

func TestExecuteResponseFixture(t *testing.T) {
	var resp ExecuteResponse
	readFixture(t, "execute_response.json", &resp)
	if resp.Result.Status != "retryable" || resp.Result.RetryAfter != 5*time.Second {
		t.Fatalf("unexpected retry result: %+v", resp.Result)
	}
	roundTripFixture(t, resp)
}

func TestPullFixtures(t *testing.T) {
	var pull PullResponse
	readFixture(t, "pull_response.json", &pull)
	if pull.Command == nil || pull.Command.Delivery != 2 || pull.Command.Task.DispatchID != "dispatch-01" {
		t.Fatalf("unexpected pull command: %+v", pull.Command)
	}
	var complete CompleteRequest
	readFixture(t, "complete_request.json", &complete)
	if complete.Result == nil || complete.Result.Status != "succeeded" {
		t.Fatalf("unexpected completion: %+v", complete)
	}
	roundTripFixture(t, pull)
	roundTripFixture(t, complete)
}

func TestProtocolErrorFixture(t *testing.T) {
	var protocolError ErrorResponse
	readFixture(t, "error_response.json", &protocolError)
	if protocolError.Code != ErrorExecutorNotFound || protocolError.Retryable {
		t.Fatalf("unexpected protocol error: %+v", protocolError)
	}
}

func TestProtocolArtifactsAreValidJSON(t *testing.T) {
	schema := readArtifact(t, "worker-protocol.schema.json")
	openAPI := readArtifact(t, "worker-protocol.openapi.json")
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok || len(definitions) == 0 {
		t.Fatal("worker protocol schema has no $defs")
	}
	walkReferences(t, openAPI, definitions)
}

func TestCurrentProtocolInfo(t *testing.T) {
	info := CurrentProtocolInfo()
	if info.Version != ProtocolVersion || info.DurationUnit != "nanosecond" || info.TimestampFormat != "RFC3339Nano" {
		t.Fatalf("unexpected protocol info: %+v", info)
	}
	if len(info.Transports) != 2 || len(info.Operations) != 3 {
		t.Fatalf("incomplete protocol capabilities: %+v", info)
	}
}

func TestZeroTimestampsAreOmitted(t *testing.T) {
	raw, err := json.Marshal(ExecuteRequest{Task: executor.ExecuteTask{
		RunID: "run-1", NodeID: "node-1", ExecutorType: "unit",
	}})
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	if bytes.Contains(raw, []byte(`"deadline"`)) {
		t.Fatalf("zero deadline leaked onto wire: %s", raw)
	}
}

func readFixture(t *testing.T, name string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
}

func roundTripFixture(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("round-trip fixture: %v", err)
	}
}

func readArtifact(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return document
}

func walkReferences(t *testing.T, value any, definitions map[string]any) {
	t.Helper()
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "$ref" {
				ref, _ := child.(string)
				const prefix = "./worker-protocol.schema.json#/$defs/"
				if strings.HasPrefix(ref, prefix) {
					name := strings.TrimPrefix(ref, prefix)
					if definitions[name] == nil {
						t.Fatalf("OpenAPI reference %s does not exist", ref)
					}
				}
			}
			walkReferences(t, child, definitions)
		}
	case []any:
		for _, child := range current {
			walkReferences(t, child, definitions)
		}
	}
}
