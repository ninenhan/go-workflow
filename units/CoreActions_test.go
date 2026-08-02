package units

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	coreexecutor "github.com/ninenhan/go-workflow/core/executor"
	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestHttpUnitExecuteBuildsBoundedMultipartBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(maxHTTPMultipartTotalBytes + 1024); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		if values := r.MultipartForm.Value["caption"]; len(values) != 1 || values[0] != "summer" {
			t.Errorf("caption = %#v", values)
		}
		file, header, err := r.FormFile("photo")
		if err != nil {
			t.Errorf("photo: %v", err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if header.Filename != "photo.txt" || header.Header.Get("Content-Type") != "text/plain" || string(data) != "hello file" {
			t.Errorf("file = %q %q %q", header.Filename, header.Header.Get("Content-Type"), data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	action := NewHttpUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"url":        server.URL,
		"method":     http.MethodPost,
		"body_mode":  httpBodyModeMultipart,
		"timeout_ms": 30000,
		"headers":    map[string]any{"Content-Type": "application/json"},
		"body": map[string]any{
			"caption": "summer",
			"photo": map[string]any{
				"kind":        httpFileValueKind,
				"name":        "photo.txt",
				"media_type":  "text/plain",
				"size":        float64(len("hello file")),
				"data_base64": base64.StdEncoding.EncodeToString([]byte("hello file")),
			},
		},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Data.(map[string]any)["body"].(map[string]any)["ok"] != true {
		t.Fatalf("result = %#v", result.Data)
	}
}

func TestHTTPUnitMultipartRejectsInvalidFileDescriptors(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{
			"kind":        httpFileValueKind,
			"name":        "safe.txt",
			"media_type":  "text/plain",
			"size":        float64(1),
			"data_base64": base64.StdEncoding.EncodeToString([]byte("x")),
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"unsafe name":    func(file map[string]any) { file["name"] = "../secret.txt" },
		"invalid base64": func(file map[string]any) { file["data_base64"] = "%%%" },
		"size mismatch":  func(file map[string]any) { file["size"] = float64(2) },
		"unknown field":  func(file map[string]any) { file["path"] = "/tmp/file" },
	} {
		t.Run(name, func(t *testing.T) {
			file := valid()
			mutate(file)
			if _, _, err := httpUnitMultipartBody(map[string]any{"file": file}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestHTTPUnitMultipartRejectsOversizedTextField(t *testing.T) {
	_, _, err := httpUnitMultipartBody(map[string]any{
		"caption": strings.Repeat("x", maxHTTPMultipartFieldBytes+1),
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func TestHTTPUnitMultipartAcceptsEmptyFile(t *testing.T) {
	_, contentType, err := httpUnitMultipartBody(map[string]any{
		"file": map[string]any{
			"kind":        httpFileValueKind,
			"name":        "empty.txt",
			"media_type":  "text/plain",
			"size":        float64(0),
			"data_base64": "",
		},
	})
	if err != nil {
		t.Fatalf("multipart body: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q", contentType)
	}
}

func TestHttpUnitExecuteUsesVisualParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("X-Trace-ID") != "trace-1" {
			t.Errorf("unexpected trace header: %q", r.Header.Get("X-Trace-ID"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body["message"] != "hello" {
			t.Errorf("unexpected request body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true})
	}))
	defer server.Close()

	action := NewHttpUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input: &unit.Input{Data: map[string]any{"message": "hello"}},
		Params: map[string]any{
			"url":         server.URL,
			"method":      http.MethodPost,
			"headers":     map[string]any{"X-Trace-ID": "trace-1"},
			"body_source": "fields",
			"body_fields": []any{map[string]any{"key": "message", "type": "text", "value": "hello"}},
			"timeout_ms":  1000,
		},
	})
	if err != nil {
		t.Fatalf("execute HttpUnit: %v", err)
	}
	output, ok := result.Data.(map[string]any)
	if !ok || output["status"] != http.StatusOK {
		t.Fatalf("unexpected HTTP output: %#v", result.Data)
	}
	body, ok := output["body"].(map[string]any)
	if !ok || body["accepted"] != true {
		t.Fatalf("unexpected response body: %#v", output["body"])
	}
}

func TestHttpUnitExecuteUsesDeclarativeServiceConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "25" {
			t.Errorf("unexpected limit query: %q", got)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"users":[{"id":"user-1"}],"cursor":"next-page"}`))
	}))
	defer server.Close()

	action := NewHttpUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"url":          server.URL,
		"method":       http.MethodGet,
		"timeout_ms":   1000,
		"auth_type":    "bearer",
		"bearer_token": "secret-token",
		"query_params": []any{map[string]any{"key": "limit", "type": "number", "value": "25"}},
		"accepted_statuses": []any{
			"202",
		},
		"response_mode": "json",
		"response_fields": []any{
			map[string]any{"key": "first_user_id", "value": "users.0.id"},
			map[string]any{"key": "next_cursor", "value": "body.cursor"},
		},
	}})
	if err != nil {
		t.Fatalf("execute declarative HttpUnit: %v", err)
	}
	output, ok := result.Data.(map[string]any)
	if !ok || output["status"] != http.StatusAccepted {
		t.Fatalf("unexpected HTTP output: %#v", result.Data)
	}
	if output["first_user_id"] != "user-1" || output["next_cursor"] != "next-page" {
		t.Fatalf("unexpected extracted response fields: %#v", output)
	}
}

func TestHttpUnitExecuteResolvesCredentialFromEnvironment(t *testing.T) {
	t.Setenv("WORKFLOW_HTTP_TEST_TOKEN", "environment-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer environment-token" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	action := NewHttpUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"url":               server.URL,
		"method":            http.MethodGet,
		"timeout_ms":        1000,
		"auth_type":         "bearer",
		"credential_source": "environment",
		"bearer_token_env":  "WORKFLOW_HTTP_TEST_TOKEN",
	}})
	if err != nil {
		t.Fatalf("execute HttpUnit with environment credential: %v", err)
	}
}

func TestHttpUnitExecuteHonorsRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	action := NewHttpUnit()
	startedAt := time.Now()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"url":        server.URL,
		"method":     http.MethodGet,
		"timeout_ms": 20,
		"retry_mode": httpRetrySafe,
	}})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected request timeout error, got %v", err)
	}
	retryable, _, classified := coreexecutor.ClassifyFailure(err)
	if !classified || !retryable {
		t.Fatalf("expected retryable timeout classification, got %T: %v", err, err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("request timeout took too long: %s", elapsed)
	}
}

func TestHttpUnitExecuteClassifiesRetryableRequestsWithoutRepeatingUnsafeMethods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	tests := []struct {
		name          string
		method        string
		retryMode     string
		wantRetryable bool
		wantAfter     time.Duration
	}{
		{name: "safe GET", method: http.MethodGet, retryMode: httpRetrySafe, wantRetryable: true, wantAfter: 2 * time.Second},
		{name: "safe POST remains permanent", method: http.MethodPost, retryMode: httpRetrySafe},
		{name: "explicit POST retry", method: http.MethodPost, retryMode: httpRetryAll, wantRetryable: true, wantAfter: 2 * time.Second},
		{name: "retry disabled", method: http.MethodGet, retryMode: httpRetryNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := NewHttpUnit()
			_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
				"url":         server.URL,
				"method":      test.method,
				"timeout_ms":  1000,
				"retry_mode":  test.retryMode,
				"body_source": "raw",
				"body":        "{}",
			}})
			if err == nil {
				t.Fatal("expected HTTP 503 failure")
			}
			retryable, retryAfter, classified := coreexecutor.ClassifyFailure(err)
			if !classified || retryable != test.wantRetryable || retryAfter != test.wantAfter {
				t.Fatalf(
					"unexpected classification: classified=%t retryable=%t retry_after=%s error=%v",
					classified,
					retryable,
					retryAfter,
					err,
				)
			}
		})
	}
}

func TestHTTPUnitRetryAfterParsesDateAndRejectsInvalidValues(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	if got := httpUnitRetryAfter("3", now); got != 3*time.Second {
		t.Fatalf("seconds retry-after = %s", got)
	}
	if got := httpUnitRetryAfter(now.Add(4*time.Second).Format(http.TimeFormat), now); got != 4*time.Second {
		t.Fatalf("date retry-after = %s", got)
	}
	for _, value := range []string{"", "invalid", "-1", now.Add(-time.Second).Format(http.TimeFormat)} {
		if got := httpUnitRetryAfter(value, now); got != 0 {
			t.Errorf("retry-after %q = %s, want 0", value, got)
		}
	}
}

func TestHttpUnitExecuteSeparatesTLSAndProxyTransportFailures(t *testing.T) {
	t.Run("TLS certificate failure is permanent", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		action := NewHttpUnit()
		_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
			"url":        server.URL,
			"method":     http.MethodGet,
			"timeout_ms": 1000,
			"retry_mode": httpRetrySafe,
		}})
		if err == nil {
			t.Fatal("expected TLS certificate failure")
		}
		retryable, _, classified := coreexecutor.ClassifyFailure(err)
		if !classified || retryable || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
			t.Fatalf("unexpected TLS classification: retryable=%t classified=%t error=%v", retryable, classified, err)
		}
	})

	t.Run("proxy connection failure is transient for safe requests", func(t *testing.T) {
		action := NewHttpUnit()
		action.client = &http.Client{Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return &url.URL{Scheme: "http", Host: "127.0.0.1:1"}, nil
			},
		}}
		_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
			"url":        "http://127.0.0.1:2/resource",
			"method":     http.MethodGet,
			"timeout_ms": 1000,
			"retry_mode": httpRetrySafe,
		}})
		if err == nil {
			t.Fatal("expected proxy connection failure")
		}
		retryable, _, classified := coreexecutor.ClassifyFailure(err)
		if !classified || !retryable || !strings.Contains(strings.ToLower(err.Error()), "proxyconnect") {
			t.Fatalf("unexpected proxy classification: retryable=%t classified=%t error=%v", retryable, classified, err)
		}
	})
}

func TestResolveHTTPUnitCredentialsRejectsEmptyEnvironmentValue(t *testing.T) {
	t.Setenv("WORKFLOW_HTTP_EMPTY_TOKEN", "")
	params := &httpUnitParams{
		AuthType:         "bearer",
		CredentialSource: httpCredentialEnvironment,
		BearerTokenEnv:   "WORKFLOW_HTTP_EMPTY_TOKEN",
	}
	if err := resolveHTTPUnitCredentials(context.Background(), params); err == nil || !strings.Contains(err.Error(), "credential not found") {
		t.Fatalf("expected missing environment credential error, got %v", err)
	}
}

func TestResolveHTTPUnitCredentialsTargetsActiveAuthenticationType(t *testing.T) {
	tests := []struct {
		name   string
		params httpUnitParams
		value  func(httpUnitParams) string
	}{
		{
			name:   "bearer",
			params: httpUnitParams{AuthType: "bearer", CredentialSource: httpCredentialEnvironment, BearerTokenEnv: "WORKFLOW_HTTP_BEARER"},
			value:  func(params httpUnitParams) string { return params.BearerToken },
		},
		{
			name:   "basic password",
			params: httpUnitParams{AuthType: "basic", CredentialSource: httpCredentialEnvironment, BasicPasswordEnv: "WORKFLOW_HTTP_BASIC"},
			value:  func(params httpUnitParams) string { return params.BasicPassword },
		},
		{
			name:   "api key",
			params: httpUnitParams{AuthType: "api_key", CredentialSource: httpCredentialEnvironment, APIKeyValueEnv: "WORKFLOW_HTTP_API_KEY"},
			value:  func(params httpUnitParams) string { return params.APIKeyValue },
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := fmt.Sprintf("credential-%d", index)
			var environmentName string
			switch test.params.AuthType {
			case "bearer":
				environmentName = test.params.BearerTokenEnv
			case "basic":
				environmentName = test.params.BasicPasswordEnv
			case "api_key":
				environmentName = test.params.APIKeyValueEnv
			}
			t.Setenv(environmentName, value)
			if err := resolveHTTPUnitCredentials(context.Background(), &test.params); err != nil {
				t.Fatalf("resolve environment credential: %v", err)
			}
			if got := test.value(test.params); got != value {
				t.Fatalf("unexpected resolved credential: got %q want %q", got, value)
			}
		})
	}
}

func TestDecodeHTTPUnitParamsRejectsInvalidServiceConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		errText string
	}{
		{
			name: "missing bearer token",
			params: map[string]any{
				"url": "https://example.com", "method": http.MethodGet, "timeout_ms": 1000, "auth_type": "bearer",
			},
			errText: "bearer_token is required",
		},
		{
			name: "invalid credential source",
			params: map[string]any{
				"url": "https://example.com", "method": http.MethodGet, "timeout_ms": 1000, "credential_source": "vault",
			},
			errText: "unsupported credential_source",
		},
		{
			name: "invalid credential environment variable",
			params: map[string]any{
				"url": "https://example.com", "method": http.MethodGet, "timeout_ms": 1000,
				"auth_type": "bearer", "credential_source": "environment", "bearer_token_env": "1 invalid",
			},
			errText: "valid environment variable name",
		},
		{
			name: "invalid accepted range",
			params: map[string]any{
				"url": "https://example.com", "method": http.MethodGet, "timeout_ms": 1000, "accepted_statuses": []any{"299-200"},
			},
			errText: "invalid accepted status range",
		},
		{
			name: "invalid response mode",
			params: map[string]any{
				"url": "https://example.com", "method": http.MethodGet, "timeout_ms": 1000, "response_mode": "yaml",
			},
			errText: "unsupported response_mode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeHTTPUnitParams(test.params)
			if err == nil || !strings.Contains(err.Error(), test.errText) {
				t.Fatalf("expected error containing %q, got %v", test.errText, err)
			}
		})
	}
}

func TestResolveHTTPUnitBodyModes(t *testing.T) {
	input := map[string]any{"source": "input"}
	tests := []struct {
		name    string
		params  *httpUnitParams
		want    any
		errText string
	}{
		{name: "previous result", params: &httpUnitParams{Method: http.MethodPost, BodySource: "input"}, want: input},
		{name: "get omits previous result", params: &httpUnitParams{Method: http.MethodGet, BodySource: "input"}},
		{name: "typed fields", params: &httpUnitParams{Method: http.MethodPost, BodySource: "fields", BodyFields: []any{
			map[string]any{"key": "score", "type": "number", "value": "42.5"},
			map[string]any{"key": "active", "type": "boolean", "value": true},
		}}, want: map[string]any{"score": 42.5, "active": true}},
		{name: "raw text", params: &httpUnitParams{Method: http.MethodPost, BodySource: "raw", Body: "hello"}, want: "hello"},
		{name: "invalid source", params: &httpUnitParams{Method: http.MethodPost, BodySource: "json"}, errText: "unsupported body_source"},
		{name: "invalid raw body", params: &httpUnitParams{Method: http.MethodPost, BodySource: "raw", Body: map[string]any{}}, errText: "body must be text"},
		{name: "duplicate field", params: &httpUnitParams{Method: http.MethodPost, BodySource: "fields", BodyFields: []any{
			map[string]any{"key": "id", "value": "1"},
			map[string]any{"key": "id", "value": "2"},
		}}, errText: "duplicate key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveHTTPUnitBody(test.params, input)
			if test.errText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errText) {
					t.Fatalf("expected error containing %q, got %v", test.errText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve body: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("unexpected body: got %#v want %#v", got, test.want)
			}
		})
	}
}

func TestSetEnvUnitVariableOperations(t *testing.T) {
	action := NewSetEnvUnit()
	setResult, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: map[string]any{"payload": map[string]any{"id": 7}}},
		Params: map[string]any{"variable_name": "latest", "value_path": "payload", "mode": "set"},
	})
	if err != nil {
		t.Fatalf("set variable: %v", err)
	}
	latest, ok := setResult.Variables["latest"].(map[string]any)
	if !ok || latest["id"] != 7 {
		t.Fatalf("unexpected set result: %#v", setResult.Variables)
	}

	mergeResult, err := action.Execute(context.Background(), unit.ContextMap{
		"latest": {Data: map[string]any{"name": "Ada", "id": 1}},
	}, &unit.Node{
		Input:  &unit.Input{Data: map[string]any{"id": 2}},
		Params: map[string]any{"variable_name": "latest", "mode": "merge"},
	})
	if err != nil {
		t.Fatalf("merge variable: %v", err)
	}
	merged := mergeResult.Variables["latest"].(map[string]any)
	if merged["name"] != "Ada" || merged["id"] != 2 {
		t.Fatalf("unexpected merge result: %#v", merged)
	}

	deleteResult, err := action.Execute(context.Background(), nil, &unit.Node{
		Params: map[string]any{"variable_name": "latest", "mode": "delete"},
	})
	if err != nil {
		t.Fatalf("delete variable: %v", err)
	}
	if len(deleteResult.DeleteVariables) != 1 || deleteResult.DeleteVariables[0] != "latest" {
		t.Fatalf("unexpected delete result: %#v", deleteResult.DeleteVariables)
	}
	if _, err := action.Execute(context.Background(), nil, &unit.Node{
		Params: map[string]any{"variable_name": "__internal", "mode": "set"},
	}); err == nil {
		t.Fatal("expected reserved legacy variable name to fail")
	}
}

func TestSetEnvUnitMultiVariableAssignments(t *testing.T) {
	action := NewSetEnvUnit()
	input := map[string]any{
		"saved_summary":  "Ready",
		"saved_category": "General",
	}
	result, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: input},
		Params: map[string]any{"mode": "set"},
	})
	if err != nil {
		t.Fatalf("set multiple variables: %v", err)
	}
	if !reflect.DeepEqual(result.Variables, input) {
		t.Fatalf("unexpected multi-variable result: %#v", result.Variables)
	}
	if !reflect.DeepEqual(result.Data, input) {
		t.Fatalf("multi-variable output did not preserve resolved input: %#v", result.Data)
	}

	tests := []struct {
		name   string
		input  any
		params map[string]any
	}{
		{name: "non-object", input: "value", params: map[string]any{"mode": "set"}},
		{name: "empty", input: map[string]any{}, params: map[string]any{"mode": "set"}},
		{name: "merge", input: input, params: map[string]any{"mode": "merge"}},
		{name: "value path", input: input, params: map[string]any{"mode": "set", "value_path": "saved_summary"}},
		{name: "reserved", input: map[string]any{"__internal": true}, params: map[string]any{"mode": "set"}},
		{name: "whitespace", input: map[string]any{" saved": true}, params: map[string]any{"mode": "set"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := action.Execute(context.Background(), nil, &unit.Node{
				Input:  &unit.Input{Data: test.input},
				Params: test.params,
			}); err == nil {
				t.Fatal("expected invalid multi-variable assignment to fail")
			}
		})
	}
}

func TestTerminalUnitResponseModes(t *testing.T) {
	action := NewTerminalUnit()
	variableResult, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: "hello"},
		Params: map[string]any{"response_mode": "variables", "output_name": "answer"},
	})
	if err != nil {
		t.Fatalf("return variables: %v", err)
	}
	if output, ok := variableResult.Data.(map[string]any); !ok || output["answer"] != "hello" {
		t.Fatalf("unexpected variables output: %#v", variableResult.Data)
	}

	textResult, err := action.Execute(context.Background(), nil, &unit.Node{
		Input:  &unit.Input{Data: map[string]any{"name": "Ada"}},
		Params: map[string]any{"response_mode": "text", "text_template": "Hello {{.name}}"},
	})
	if err != nil {
		t.Fatalf("return text: %v", err)
	}
	if textResult.Data != "Hello Ada" {
		t.Fatalf("unexpected text output: %#v", textResult.Data)
	}
}

func TestPassThroughUnitsPreserveRegisteredRuntimeName(t *testing.T) {
	executor := unit.NewExecutor(nil)
	for _, name := range []string{"IfUnit", "LogicUnit", "LogUnit", "RemarkUnit"} {
		t.Run(name, func(t *testing.T) {
			result, err := executor.Execute(context.Background(), coreexecutor.ExecuteTask{
				RunID:        "run-pass-through",
				NodeID:       name,
				ExecutorType: string(coreexecutor.TypeUnit),
				ExecutorRef:  name,
				Input:        map[string]any{"value": name},
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			output, ok := result.Output.(map[string]any)
			if !ok || output["value"] != name {
				t.Fatalf("output = %#v", result.Output)
			}
			if result.Metadata["node_name"] != name {
				t.Fatalf("node name = %#v", result.Metadata["node_name"])
			}
		})
	}
}

var userVisibleRuntimeUnitNames = []string{
	"AdjustDateUnit",
	"AskForInputUnit",
	"CalculateUnit",
	"ChangeCaseUnit",
	"ChooseFromMenuUnit",
	"CombineTextUnit",
	"CountUnit",
	"DateUnit",
	"DictionaryUnit",
	"FormatDateUnit",
	"GetDictionaryValueUnit",
	"GetListItemUnit",
	"HttpUnit",
	"IfUnit",
	"LLMUnit",
	"ListUnit",
	"LogUnit",
	"ReadableUnit",
	"RemarkUnit",
	"ReplaceTextUnit",
	"ScriptUnit",
	"SetEnvUnit",
	"SplitTextUnit",
	"TerminalUnit",
	"TextUnit",
	"TimeoutUnit",
}

func TestUserVisibleRuntimeUnitsAreRegistered(t *testing.T) {
	for _, name := range userVisibleRuntimeUnitNames {
		if _, registered := unit.Find(name); !registered {
			t.Errorf("%s is not registered", name)
		}
	}
}
