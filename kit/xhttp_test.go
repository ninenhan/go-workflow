package xhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func collectMessages(ch <-chan any) []any {
	var messages []any
	for message := range ch {
		messages = append(messages, message)
	}
	return messages
}

func TestHandlerHttpWithChannel_UsesConfiguredMethod(t *testing.T) {
	var seenMethod string
	var seenBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record the inbound request so the test can prove the transport no
		// longer hardcodes POST for every HttpUnit execution.
		seenMethod = r.Method
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		seenBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "method": r.Method})
	}))
	defer server.Close()

	// The handler publishes results onto the provided channel before returning,
	// so the test uses a small buffer to avoid self-deadlocking.
	ch := make(chan any, 1)
	err := HandlerHttpWithChannel(XRequest{
		Url:    server.URL,
		Method: http.MethodGet,
	}, false, ch)
	if err != nil {
		t.Fatalf("HandlerHttpWithChannel returned error: %v", err)
	}

	messages := collectMessages(ch)
	if seenMethod != http.MethodGet {
		t.Fatalf("expected method %s, got %s", http.MethodGet, seenMethod)
	}
	if seenBody != "" {
		t.Fatalf("expected GET request without body, got %q", seenBody)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one response message, got %d", len(messages))
	}
}

func TestHandlerHttpWithChannel_DefaultsToPost(t *testing.T) {
	var seenMethod string
	var seenContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The default keeps backward compatibility for existing workflow JSON
		// that omitted the method field before GET support was added.
		seenMethod = r.Method
		seenContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// The handler publishes results onto the provided channel before returning,
	// so the test uses a small buffer to avoid self-deadlocking.
	ch := make(chan any, 1)
	err := HandlerHttpWithChannel(XRequest{
		Url:  server.URL,
		Body: map[string]any{"q": "news"},
	}, false, ch)
	if err != nil {
		t.Fatalf("HandlerHttpWithChannel returned error: %v", err)
	}

	_ = collectMessages(ch)
	if seenMethod != http.MethodPost {
		t.Fatalf("expected default method %s, got %s", http.MethodPost, seenMethod)
	}
	if seenContentType != "application/json" {
		t.Fatalf("expected JSON content type, got %q", seenContentType)
	}
}
