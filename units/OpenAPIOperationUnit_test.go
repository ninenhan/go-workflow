package units

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func TestOpenAPIOperationUnitBuildsMultipartRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(maxHTTPMultipartTotalBytes + 1024); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		if values := r.MultipartForm.Value["caption"]; len(values) != 1 || values[0] != "avatar" {
			t.Errorf("caption = %#v", values)
		}
		file, header, err := r.FormFile("photo")
		if err != nil {
			t.Errorf("photo: %v", err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if header.Filename != "avatar.txt" || header.Header.Get("Content-Type") != "text/plain" || string(data) != "avatar-data" {
			t.Errorf("file = %q %q %q", header.Filename, header.Header.Get("Content-Type"), data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uploaded":true}`))
	}))
	defer server.Close()

	action := NewOpenAPIOperationUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"base_url":     server.URL,
		"path":         "/upload",
		"method":       http.MethodPost,
		"content_type": "multipart/form-data",
		"timeout_ms":   30000,
		"body_fields": []any{
			map[string]any{"name": "caption", "key": "caption", "type": "string"},
			map[string]any{"name": "photo", "key": "photo", "type": "file", "required": true},
		},
		"argument:caption": "avatar",
		"argument:photo": map[string]any{
			"kind":        httpFileValueKind,
			"name":        "avatar.txt",
			"media_type":  "text/plain",
			"size":        float64(len("avatar-data")),
			"data_base64": base64.StdEncoding.EncodeToString([]byte("avatar-data")),
		},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Data.(map[string]any)["body"].(map[string]any)["uploaded"] != true {
		t.Fatalf("result = %#v", result.Data)
	}
}

func TestOpenAPIOperationUnitBuildsStructuredRequest(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/users/a%2Fb/messages" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.URL.Query().Get("limit") != "3" || r.Header.Get("X-Trace") != "trace-1" {
			t.Errorf("query/header = %s, %s", r.URL.RawQuery, r.Header.Get("X-Trace"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	action := NewOpenAPIOperationUnit()
	result, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"base_url": server.URL,
		"path":     "/users/{user_id}/messages",
		"method":   "POST",
		"parameters": []any{
			map[string]any{"name": "user_id", "in": "path", "key": "user-id", "required": true},
			map[string]any{"name": "limit", "in": "query", "key": "limit", "type": "integer", "minimum": 1, "maximum": 5},
			map[string]any{"name": "X-Trace", "in": "header", "key": "x-trace"},
		},
		"body_fields": []any{
			map[string]any{"name": "message", "key": "message", "required": true, "type": "string", "min_length": 3, "max_length": 20},
			map[string]any{"name": "urgent", "key": "urgent", "type": "boolean"},
			map[string]any{"name": "timezone", "key": "profile-timezone", "path": []any{"profile", "timezone"}, "type": "string"},
			map[string]any{
				"name":           "tags",
				"key":            "tags",
				"type":           "string[]",
				"required":       true,
				"min_items":      1,
				"max_items":      2,
				"allowed_values": []any{"news", "featured"},
			},
			map[string]any{"name": "scores", "key": "scores", "type": "integer[]"},
			map[string]any{"name": "flags", "key": "flags", "type": "boolean[]"},
			map[string]any{"name": "nickname", "key": "nickname", "type": "string", "required": true, "nullable": true},
			map[string]any{"name": "destination", "key": "email-destination", "type": "string", "required": true, "variant_key": "contact-variant", "variant_value": "email"},
			map[string]any{"name": "destination", "key": "sms-destination", "type": "string", "required": true, "variant_key": "contact-variant", "variant_value": "sms"},
		},
		"variant_groups": []any{
			map[string]any{
				"key":                "contact-variant",
				"required":           true,
				"values":             []any{"email", "sms"},
				"discriminator_path": []any{"kind"},
			},
		},
		"content_type":               "application/json",
		"timeout_ms":                 30000,
		"auth_type":                  "none",
		"response_mode":              "auto",
		"argument:user-id":           "a/b",
		"argument:limit":             3,
		"argument:x-trace":           "trace-1",
		"argument:message":           "hello",
		"argument:urgent":            "false",
		"argument:profile-timezone":  "Asia/Taipei",
		"argument:tags":              []any{"news", "featured"},
		"argument:scores":            []any{"7", "11"},
		"argument:flags":             []any{"true", false},
		"argument:nickname":          nil,
		"argument:contact-variant":   "email",
		"argument:email-destination": "hello@example.test",
		"argument:sms-destination":   "+10000000000",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if received["message"] != "hello" || received["urgent"] != false {
		t.Fatalf("body = %#v", received)
	}
	tags, ok := received["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "news" || tags[1] != "featured" {
		t.Fatalf("string array body = %#v", received)
	}
	scores, ok := received["scores"].([]any)
	if !ok || len(scores) != 2 || scores[0] != float64(7) || scores[1] != float64(11) {
		t.Fatalf("integer array body = %#v", received)
	}
	flags, ok := received["flags"].([]any)
	if !ok || len(flags) != 2 || flags[0] != true || flags[1] != false {
		t.Fatalf("boolean array body = %#v", received)
	}
	if nickname, exists := received["nickname"]; !exists || nickname != nil {
		t.Fatalf("nullable body = %#v", received)
	}
	if received["destination"] != "hello@example.test" {
		t.Fatalf("variant body = %#v", received)
	}
	if received["kind"] != "email" {
		t.Fatalf("discriminator body = %#v", received)
	}
	profile, ok := received["profile"].(map[string]any)
	if !ok || profile["timezone"] != "Asia/Taipei" {
		t.Fatalf("nested body = %#v", received)
	}
	body := result.Data.(map[string]any)["body"].(map[string]any)
	if body["ok"] != true {
		t.Fatalf("result = %#v", result.Data)
	}
}

func TestOpenAPIOperationUnitRejectsInvalidContracts(t *testing.T) {
	action := NewOpenAPIOperationUnit()
	for name, params := range map[string]map[string]any{
		"relative base": {
			"base_url": "/v1", "path": "/users", "method": "GET", "timeout_ms": 1000,
		},
		"missing path declaration": {
			"base_url": "https://example.test", "path": "/users/{id}", "method": "GET", "timeout_ms": 1000,
		},
		"optional path parameter": {
			"base_url": "https://example.test", "path": "/users/{id}", "method": "GET", "timeout_ms": 1000,
			"parameters": []any{map[string]any{"name": "id", "in": "path", "key": "id"}},
		},
		"body on get": {
			"base_url": "https://example.test", "path": "/users", "method": "GET", "timeout_ms": 1000,
			"body_fields": []any{map[string]any{"name": "name", "key": "name"}},
		},
		"static dot segment": {
			"base_url": "https://example.test/v1", "path": "/../users", "method": "GET", "timeout_ms": 1000,
		},
		"invalid header name": {
			"base_url": "https://example.test", "path": "/users", "method": "GET", "timeout_ms": 1000,
			"parameters": []any{map[string]any{"name": "Bad Header", "in": "header", "key": "bad-header"}},
		},
		"forbidden header name": {
			"base_url": "https://example.test", "path": "/users", "method": "GET", "timeout_ms": 1000,
			"parameters": []any{map[string]any{"name": "Host", "in": "header", "key": "host"}},
		},
		"conflicting body paths": {
			"base_url": "https://example.test", "path": "/users", "method": "POST", "timeout_ms": 1000,
			"body_fields": []any{
				map[string]any{"name": "profile", "key": "profile"},
				map[string]any{"name": "name", "key": "profile-name", "path": []any{"profile", "name"}},
			},
		},
		"unsupported body type": {
			"base_url": "https://example.test", "path": "/users", "method": "POST", "timeout_ms": 1000,
			"body_fields": []any{
				map[string]any{"name": "profile", "key": "profile", "type": "object"},
			},
		},
		"discriminator body conflict": {
			"base_url": "https://example.test", "path": "/users", "method": "POST", "timeout_ms": 1000,
			"variant_groups": []any{
				map[string]any{
					"key":                "kind",
					"values":             []any{"email", "sms"},
					"discriminator_path": []any{"contact", "kind"},
				},
			},
			"body_fields": []any{
				map[string]any{"name": "kind", "key": "contact-kind", "path": []any{"contact", "kind"}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := action.Execute(context.Background(), nil, &unit.Node{Params: params}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOpenAPIOperationUnitRejectsEmptyRequiredList(t *testing.T) {
	action := NewOpenAPIOperationUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"base_url": "https://example.test",
		"path":     "/users",
		"method":   "POST",
		"body_fields": []any{
			map[string]any{"name": "tags", "key": "tags", "type": "string[]", "required": true},
		},
		"argument:tags": []any{},
	}})
	if err == nil {
		t.Fatal("expected required list validation error")
	}
}

func TestOpenAPIOperationUnitRejectsNullForNonNullableField(t *testing.T) {
	action := NewOpenAPIOperationUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"base_url": "https://example.test",
		"path":     "/users",
		"method":   "POST",
		"body_fields": []any{
			map[string]any{"name": "name", "key": "name", "type": "string"},
		},
		"argument:name": nil,
	}})
	if err == nil {
		t.Fatal("expected non-nullable validation error")
	}
}

func TestOpenAPIOperationUnitRejectsUnknownVariant(t *testing.T) {
	action := NewOpenAPIOperationUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"base_url": "https://example.test",
		"path":     "/messages",
		"method":   "POST",
		"variant_groups": []any{
			map[string]any{"key": "contact-variant", "required": true, "values": []any{"email", "sms"}},
		},
		"body_fields": []any{
			map[string]any{"name": "email", "key": "email", "variant_key": "contact-variant", "variant_value": "email"},
		},
		"argument:contact-variant": "fax",
	}})
	if err == nil {
		t.Fatal("expected unknown variant validation error")
	}
}

func TestOpenAPIOperationUnitRejectsValuesOutsideImportedContract(t *testing.T) {
	for name, test := range map[string]struct {
		params map[string]any
		want   string
	}{
		"short text": {
			params: map[string]any{
				"base_url": "https://example.test", "path": "/messages", "method": "POST",
				"body_fields":      []any{map[string]any{"name": "message", "key": "message", "type": "string", "min_length": 3}},
				"argument:message": "no",
			},
			want: "requires at least 3 characters",
		},
		"number below minimum": {
			params: map[string]any{
				"base_url": "https://example.test", "path": "/messages", "method": "GET",
				"parameters":     []any{map[string]any{"name": "limit", "in": "query", "key": "limit", "type": "integer", "minimum": 1}},
				"argument:limit": 0,
			},
			want: "below its minimum",
		},
		"value outside enum": {
			params: map[string]any{
				"base_url": "https://example.test", "path": "/messages", "method": "POST",
				"body_fields": []any{
					map[string]any{"name": "kind", "key": "kind", "type": "string", "allowed_values": []any{"email", "sms"}},
				},
				"argument:kind": "fax",
			},
			want: "unsupported value",
		},
		"too many items": {
			params: map[string]any{
				"base_url": "https://example.test", "path": "/messages", "method": "POST",
				"body_fields": []any{
					map[string]any{"name": "tags", "key": "tags", "type": "string[]", "max_items": 2},
				},
				"argument:tags": []any{"one", "two", "three"},
			},
			want: "allows at most 2 items",
		},
	} {
		t.Run(name, func(t *testing.T) {
			config, err := decodeOpenAPIOperationParams(test.params)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, err := config.httpParams(test.params); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestOpenAPIOperationUnitPreservesExactAllowedStringValues(t *testing.T) {
	params := map[string]any{
		"base_url": "https://example.test", "path": "/messages", "method": "POST",
		"body_fields": []any{
			map[string]any{
				"name":           "label",
				"key":            "label",
				"type":           "string",
				"allowed_values": []any{" spaced "},
			},
		},
		"argument:label": " spaced ",
	}
	config, err := decodeOpenAPIOperationParams(params)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	request, err := config.httpParams(params)
	if err != nil {
		t.Fatalf("http params: %v", err)
	}
	body, ok := request.Body.(map[string]any)
	if !ok || body["label"] != " spaced " {
		t.Fatalf("body = %#v", request.Body)
	}
}

func TestOpenAPIOperationUnitRejectsHeaderLineBreak(t *testing.T) {
	action := NewOpenAPIOperationUnit()
	_, err := action.Execute(context.Background(), nil, &unit.Node{Params: map[string]any{
		"base_url": "https://example.test",
		"path":     "/users",
		"method":   "GET",
		"parameters": []any{
			map[string]any{"name": "X-Trace", "in": "header", "key": "trace"},
		},
		"argument:trace": "safe\r\nX-Injected: yes",
	}})
	if err == nil {
		t.Fatal("expected header line break validation error")
	}
}

func TestEscapeOpenAPIPathArgumentProtectsDotSegments(t *testing.T) {
	for input, expected := range map[string]string{
		".":   "%2E",
		"..":  "%2E%2E",
		"a/b": "a%2Fb",
	} {
		if actual := escapeOpenAPIPathArgument(input); actual != expected {
			t.Fatalf("escapeOpenAPIPathArgument(%q) = %q, want %q", input, actual, expected)
		}
	}
}
