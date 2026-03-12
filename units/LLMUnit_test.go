package units

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestLLMUnitExecute_Success(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %s", auth)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["model"] != "gpt-4o-mini" {
			t.Fatalf("unexpected model: %#v", req["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{"content": "hello from llm"},
				},
			},
			"usage": map[string]any{"total_tokens": 12},
		})
	}))
	defer srv.Close()

	u := &LLMUnit{}
	u.UnitName = u.GetUnitName()

	res, err := u.Execute(context.Background(), nil, &unit.Node{
		ID: "llm1",
		Input: &unit.Input{
			Data: "say hello",
		},
		Params: map[string]any{
			"model":      "gpt-4o-mini",
			"base_url":   srv.URL,
			"timeout_ms": 2000,
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res == nil {
		t.Fatalf("nil result")
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", res.Data)
	}
	if got, _ := data["text"].(string); got != "hello from llm" {
		t.Fatalf("unexpected llm text: %q", got)
	}
}

func TestLLMUnitExecute_MissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	u := &LLMUnit{}
	u.UnitName = u.GetUnitName()

	_, err := u.Execute(context.Background(), nil, &unit.Node{
		ID: "llm1",
		Input: &unit.Input{
			Data: "hello",
		},
		Params: map[string]any{
			"model": "gpt-4o-mini",
		},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLLMUnitExtractAssistantText_ArrayContent(t *testing.T) {
	raw := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "hello "},
						map[string]any{"type": "text", "text": "world"},
					},
				},
			},
		},
	}
	if got := extractAssistantText(raw); got != "hello world" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestLLMUnitExecute_Stream(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"stream\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	u := &LLMUnit{}
	u.UnitName = u.GetUnitName()

	res, err := u.Execute(context.Background(), nil, &unit.Node{
		ID: "llm-stream-1",
		Input: &unit.Input{
			Data: "say hello",
		},
		Params: map[string]any{
			"model":      "gpt-4o-mini",
			"base_url":   srv.URL,
			"timeout_ms": 2000,
			"stream":     true,
		},
	})
	if err != nil {
		t.Fatalf("execute stream failed: %v", err)
	}
	if res == nil {
		t.Fatalf("nil result")
	}
	if !res.Stream {
		t.Fatalf("expected stream=true")
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result data type: %T", res.Data)
	}
	if got, _ := data["text"].(string); got != "hello stream" {
		t.Fatalf("unexpected streamed text: %q", got)
	}
	chunks, ok := data["chunks"].([]string)
	if ok && len(chunks) != 2 {
		t.Fatalf("unexpected chunks len: %d", len(chunks))
	}
}
