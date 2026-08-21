package units

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ninenhan/go-workflow/core/credential"
	coreexecutor "github.com/ninenhan/go-workflow/core/executor"
	unit "github.com/ninenhan/go-workflow/worker/unit"
)

const (
	maxHTTPUnitTimeout         = 10 * time.Minute
	maxHTTPUnitResponseBytes   = 10 << 20
	httpCredentialInline       = "inline"
	httpCredentialEnvironment  = "environment"
	httpRetryNone              = "none"
	httpRetrySafe              = "safe"
	httpRetryAll               = "all"
	httpBodyModeAuto           = "auto"
	httpBodyModeMultipart      = "multipart"
	httpFileValueKind          = "file"
	maxHTTPMultipartFields     = 50
	maxHTTPMultipartFileBytes  = 4 << 20
	maxHTTPMultipartTotalBytes = 8 << 20
	maxHTTPMultipartFieldBytes = 1 << 20
	maxHTTPMultipartBodyBytes  = 12 << 20
)

type HttpUnit struct {
	unit.Unit
	client *http.Client
}

var _ unit.ExecutableUnit = (*HttpUnit)(nil)

type httpUnitParams struct {
	URL              string            `json:"url"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers,omitempty"`
	QueryParams      any               `json:"query_params,omitempty"`
	AuthType         string            `json:"auth_type,omitempty"`
	CredentialSource string            `json:"credential_source,omitempty"`
	BearerToken      string            `json:"bearer_token,omitempty"`
	BearerTokenEnv   string            `json:"bearer_token_env,omitempty"`
	BasicUsername    string            `json:"basic_username,omitempty"`
	BasicPassword    string            `json:"basic_password,omitempty"`
	BasicPasswordEnv string            `json:"basic_password_env,omitempty"`
	APIKeyName       string            `json:"api_key_name,omitempty"`
	APIKeyValue      string            `json:"api_key_value,omitempty"`
	APIKeyValueEnv   string            `json:"api_key_value_env,omitempty"`
	APIKeyLocation   string            `json:"api_key_location,omitempty"`
	BodySource       string            `json:"body_source,omitempty"`
	BodyMode         string            `json:"body_mode,omitempty"`
	BodyFields       any               `json:"body_fields,omitempty"`
	Body             any               `json:"body,omitempty"`
	AcceptedStatuses []string          `json:"accepted_statuses,omitempty"`
	ResponseMode     string            `json:"response_mode,omitempty"`
	ResponseFields   any               `json:"response_fields,omitempty"`
	TimeoutMS        int               `json:"timeout_ms"`
	RetryMode        string            `json:"retry_mode,omitempty"`
}

func (t *HttpUnit) GetUnitName() string {
	return reflect.TypeOf(HttpUnit{}).Name()
}

func (t *HttpUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, errors.New("HttpUnit: missing node")
	}
	params, err := decodeHTTPUnitParams(self.Params)
	if err != nil {
		return nil, coreexecutor.PermanentFailure(err)
	}
	var input any
	if self.Input != nil {
		input = self.Input.Data
	}
	data, err := executeHTTPUnitRequest(ctx, params, input, t.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		if _, _, classified := coreexecutor.ClassifyFailure(err); classified {
			return nil, err
		}
		return nil, coreexecutor.PermanentFailure(err)
	}
	return &unit.ExecutionResult{
		NodeName: t.UnitName,
		Data:     data,
	}, nil
}

func executeHTTPUnitRequest(
	ctx context.Context,
	params *httpUnitParams,
	input any,
	client *http.Client,
) (map[string]any, error) {
	if err := resolveHTTPUnitCredentials(ctx, params); err != nil {
		return nil, err
	}
	body, err := resolveHTTPUnitBody(params, input)
	if err != nil {
		return nil, err
	}
	bodyReader, bodyContentType, err := httpUnitBody(params.BodyMode, body)
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(params.TimeoutMS)*time.Millisecond)
	defer cancel()
	requestURL, err := httpUnitRequestURL(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(runCtx, params.Method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("HttpUnit: create request: %w", err)
	}
	for key, value := range params.Headers {
		req.Header.Set(key, value)
	}
	if err := applyHTTPUnitAuthentication(req, params); err != nil {
		return nil, err
	}
	if bodyContentType != "" {
		req.Header.Set("Content-Type", bodyContentType)
	}

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		failure := fmt.Errorf("HttpUnit: send request: %w", err)
		if ctx.Err() != nil {
			return nil, failure
		}
		if isHTTPUnitTLSFailure(err) {
			return nil, coreexecutor.PermanentFailure(failure)
		}
		return nil, classifyHTTPUnitTransientFailure(params, failure, 0)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPUnitResponseBytes+1))
	if err != nil {
		failure := fmt.Errorf("HttpUnit: read response: %w", err)
		if ctx.Err() != nil {
			return nil, failure
		}
		return nil, classifyHTTPUnitTransientFailure(params, failure, 0)
	}
	if len(raw) > maxHTTPUnitResponseBytes {
		return nil, fmt.Errorf("HttpUnit: response exceeds %d bytes", maxHTTPUnitResponseBytes)
	}
	if !httpUnitStatusAccepted(resp.StatusCode, params.AcceptedStatuses) {
		failure := fmt.Errorf("HttpUnit: http %d: %s", resp.StatusCode, limitBody(string(raw), 1024))
		if httpUnitStatusRetryable(resp.StatusCode) {
			return nil, classifyHTTPUnitTransientFailure(
				params,
				failure,
				httpUnitRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			)
		}
		return nil, coreexecutor.PermanentFailure(failure)
	}
	responseBody, err := decodeHTTPUnitResponse(raw, resp.Header.Get("Content-Type"), params.ResponseMode)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"status":  resp.StatusCode,
		"headers": map[string][]string(resp.Header),
		"body":    responseBody,
	}
	if err := appendHTTPUnitResponseFields(data, responseBody, params.ResponseFields); err != nil {
		return nil, err
	}
	return data, nil
}

func httpUnitRequestURL(params *httpUnitParams) (string, error) {
	parsed, err := url.Parse(params.URL)
	if err != nil {
		return "", fmt.Errorf("HttpUnit: parse url: %w", err)
	}
	values := parsed.Query()
	if params.QueryParams != nil {
		entries, err := configuredTypedEntries("HttpUnit", "query_params", params.QueryParams)
		if err != nil {
			return "", err
		}
		for key, value := range entries {
			values.Set(key, fmt.Sprint(value))
		}
	}
	if params.AuthType == "api_key" && params.APIKeyLocation == "query" {
		values.Set(params.APIKeyName, params.APIKeyValue)
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func applyHTTPUnitAuthentication(req *http.Request, params *httpUnitParams) error {
	switch params.AuthType {
	case "none":
		return nil
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+params.BearerToken)
		return nil
	case "basic":
		req.SetBasicAuth(params.BasicUsername, params.BasicPassword)
		return nil
	case "api_key":
		if params.APIKeyLocation == "header" {
			req.Header.Set(params.APIKeyName, params.APIKeyValue)
		}
		return nil
	default:
		return fmt.Errorf("HttpUnit: unsupported auth_type %q", params.AuthType)
	}
}

func httpUnitStatusAccepted(status int, configured []string) bool {
	if len(configured) == 0 {
		return status >= http.StatusOK && status < http.StatusMultipleChoices
	}
	for _, item := range configured {
		parts := strings.Split(strings.TrimSpace(item), "-")
		if len(parts) == 1 {
			value, _ := strconv.Atoi(parts[0])
			if status == value {
				return true
			}
			continue
		}
		if len(parts) == 2 {
			from, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
			to, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			if status >= from && status <= to {
				return true
			}
		}
	}
	return false
}

func decodeHTTPUnitResponse(raw []byte, contentType, mode string) (any, error) {
	decodeJSON := mode == "json" || (mode == "auto" && strings.Contains(strings.ToLower(contentType), "json"))
	if !decodeJSON {
		return string(raw), nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("HttpUnit: decode JSON response: %w", err)
	}
	return value, nil
}

func appendHTTPUnitResponseFields(output map[string]any, body, configured any) error {
	if configured == nil {
		return nil
	}
	entries, ok := configured.([]any)
	if !ok {
		return fmt.Errorf("HttpUnit: response_fields must be an array")
	}
	seen := make(map[string]struct{}, len(entries))
	for index, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("HttpUnit: response_fields[%d] must be an object", index)
		}
		keyValue, keyOK := entry["key"].(string)
		pathValue, pathOK := entry["value"].(string)
		key := strings.TrimSpace(keyValue)
		path := strings.TrimSpace(pathValue)
		if !keyOK || !pathOK || key == "" || path == "" {
			return fmt.Errorf("HttpUnit: response_fields[%d] requires key and path", index)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("HttpUnit: response_fields contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		value, err := httpUnitResponsePath(body, path)
		if err != nil {
			return fmt.Errorf("HttpUnit: response field %q: %w", key, err)
		}
		valueType, _ := entry["type"].(string)
		value, err = coerceHTTPUnitResponseField(value, strings.TrimSpace(valueType))
		if err != nil {
			return fmt.Errorf("HttpUnit: response field %q: %w", key, err)
		}
		output[key] = value
	}
	return nil
}

func coerceHTTPUnitResponseField(value any, valueType string) (any, error) {
	switch valueType {
	case "":
		return value, nil
	case "text":
		if text, ok := value.(string); ok {
			return text, nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode text value: %w", err)
		}
		return string(encoded), nil
	case "number":
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case float32:
			return float64(typed), nil
		case int:
			return float64(typed), nil
		case int64:
			return float64(typed), nil
		case string:
			number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil {
				return number, nil
			}
		}
		return nil, fmt.Errorf("cannot convert %T to number", value)
	case "boolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		if text, ok := value.(string); ok {
			boolean, err := strconv.ParseBool(strings.TrimSpace(text))
			if err == nil {
				return boolean, nil
			}
		}
		return nil, fmt.Errorf("cannot convert %T to boolean", value)
	default:
		return nil, fmt.Errorf("unsupported value type %q", valueType)
	}
}

func httpUnitResponsePath(value any, path string) (any, error) {
	current := value
	for _, segment := range strings.Split(strings.TrimPrefix(strings.TrimPrefix(path, "$."), "body."), ".") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, fmt.Errorf("invalid empty path segment in %q", path)
		}
		switch typed := current.(type) {
		case map[string]any:
			next, exists := typed[segment]
			if !exists {
				return nil, fmt.Errorf("path %q does not contain %q", path, segment)
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("path %q has invalid array index %q", path, segment)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("path %q cannot descend through %T", path, current)
		}
	}
	return current, nil
}

func resolveHTTPUnitBody(params *httpUnitParams, input any) (any, error) {
	switch params.BodySource {
	case "":
		if params.Body != nil {
			return params.Body, nil
		}
		if params.Method == http.MethodGet || params.Method == http.MethodHead {
			return nil, nil
		}
		return input, nil
	case "input":
		if params.Method == http.MethodGet || params.Method == http.MethodHead {
			return nil, nil
		}
		return input, nil
	case "fields":
		return configuredTypedEntries("HttpUnit", "body_fields", params.BodyFields)
	case "raw":
		if params.Body == nil {
			return "", nil
		}
		body, ok := params.Body.(string)
		if !ok {
			return nil, fmt.Errorf("HttpUnit: body must be text when body_source is raw, got %T", params.Body)
		}
		return body, nil
	default:
		return nil, fmt.Errorf("HttpUnit: unsupported body_source %q", params.BodySource)
	}
}

func decodeHTTPUnitParams(raw map[string]any) (*httpUnitParams, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("HttpUnit: encode params: %w", err)
	}
	params := &httpUnitParams{}
	if err := json.Unmarshal(encoded, params); err != nil {
		return nil, fmt.Errorf("HttpUnit: decode params: %w", err)
	}
	params.URL = strings.TrimSpace(params.URL)
	if params.URL == "" {
		return nil, errors.New("HttpUnit: params.url is required")
	}
	params.Method = strings.ToUpper(strings.TrimSpace(params.Method))
	if params.Method == "" {
		return nil, errors.New("HttpUnit: params.method is required")
	}
	params.BodySource = strings.ToLower(strings.TrimSpace(params.BodySource))
	params.BodyMode = strings.ToLower(strings.TrimSpace(params.BodyMode))
	if params.BodyMode == "" {
		params.BodyMode = httpBodyModeAuto
	}
	params.AuthType = strings.ToLower(strings.TrimSpace(params.AuthType))
	if params.AuthType == "" {
		params.AuthType = "none"
	}
	params.CredentialSource = strings.ToLower(strings.TrimSpace(params.CredentialSource))
	if params.CredentialSource == "" {
		// Omitted source preserves the original HttpUnit contract for existing definitions.
		params.CredentialSource = httpCredentialInline
	}
	params.BearerTokenEnv = strings.TrimSpace(params.BearerTokenEnv)
	params.BasicPasswordEnv = strings.TrimSpace(params.BasicPasswordEnv)
	params.APIKeyValueEnv = strings.TrimSpace(params.APIKeyValueEnv)
	params.APIKeyLocation = strings.ToLower(strings.TrimSpace(params.APIKeyLocation))
	params.ResponseMode = strings.ToLower(strings.TrimSpace(params.ResponseMode))
	if params.ResponseMode == "" {
		params.ResponseMode = "auto"
	}
	params.RetryMode = strings.ToLower(strings.TrimSpace(params.RetryMode))
	if params.RetryMode == "" {
		params.RetryMode = httpRetryNone
	}
	if err := validateHTTPUnitConfiguration(params); err != nil {
		return nil, err
	}
	timeout := time.Duration(params.TimeoutMS) * time.Millisecond
	if params.TimeoutMS <= 0 || timeout > maxHTTPUnitTimeout {
		return nil, fmt.Errorf("HttpUnit: params.timeout_ms must be between 1 and %d", maxHTTPUnitTimeout.Milliseconds())
	}
	return params, nil
}

func validateHTTPUnitConfiguration(params *httpUnitParams) error {
	if params.BodyMode != httpBodyModeAuto && params.BodyMode != httpBodyModeMultipart {
		return fmt.Errorf("HttpUnit: unsupported body_mode %q", params.BodyMode)
	}
	if params.CredentialSource != httpCredentialInline && params.CredentialSource != httpCredentialEnvironment {
		return fmt.Errorf("HttpUnit: unsupported credential_source %q", params.CredentialSource)
	}
	switch params.AuthType {
	case "none":
	case "bearer":
		if err := validateHTTPUnitCredential(params.CredentialSource, params.BearerToken, params.BearerTokenEnv, "bearer_token", "bearer_token_env"); err != nil {
			return err
		}
	case "basic":
		if strings.TrimSpace(params.BasicUsername) == "" {
			return errors.New("HttpUnit: params.basic_username is required for basic authentication")
		}
		if err := validateHTTPUnitCredential(params.CredentialSource, params.BasicPassword, params.BasicPasswordEnv, "basic_password", "basic_password_env"); err != nil {
			return err
		}
	case "api_key":
		params.APIKeyName = strings.TrimSpace(params.APIKeyName)
		if params.APIKeyName == "" {
			return errors.New("HttpUnit: params.api_key_name is required for api key authentication")
		}
		if err := validateHTTPUnitCredential(params.CredentialSource, params.APIKeyValue, params.APIKeyValueEnv, "api_key_value", "api_key_value_env"); err != nil {
			return err
		}
		if params.APIKeyLocation != "header" && params.APIKeyLocation != "query" {
			return errors.New("HttpUnit: params.api_key_location must be header or query")
		}
	default:
		return fmt.Errorf("HttpUnit: unsupported auth_type %q", params.AuthType)
	}
	if params.ResponseMode != "auto" && params.ResponseMode != "json" && params.ResponseMode != "text" {
		return fmt.Errorf("HttpUnit: unsupported response_mode %q", params.ResponseMode)
	}
	if params.RetryMode != httpRetryNone && params.RetryMode != httpRetrySafe && params.RetryMode != httpRetryAll {
		return fmt.Errorf("HttpUnit: unsupported retry_mode %q", params.RetryMode)
	}
	for _, item := range params.AcceptedStatuses {
		parts := strings.Split(strings.TrimSpace(item), "-")
		if len(parts) < 1 || len(parts) > 2 {
			return fmt.Errorf("HttpUnit: invalid accepted status %q", item)
		}
		from, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || from < 100 || from > 599 {
			return fmt.Errorf("HttpUnit: invalid accepted status %q", item)
		}
		if len(parts) == 2 {
			to, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || to < from || to > 599 {
				return fmt.Errorf("HttpUnit: invalid accepted status range %q", item)
			}
		}
	}
	return nil
}

func classifyHTTPUnitTransientFailure(params *httpUnitParams, err error, retryAfter time.Duration) error {
	if httpUnitRetryAllowed(params) {
		return coreexecutor.RetryableFailure(err, retryAfter)
	}
	return coreexecutor.PermanentFailure(err)
}

func httpUnitRetryAllowed(params *httpUnitParams) bool {
	if params == nil {
		return false
	}
	if params.RetryMode == httpRetryAll {
		return true
	}
	if params.RetryMode != httpRetrySafe {
		return false
	}
	switch params.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func httpUnitStatusRetryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func httpUnitRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func isHTTPUnitTLSFailure(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknownAuthority) ||
		errors.As(err, &certificateInvalid) ||
		errors.As(err, &hostname) {
		return true
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "tls:") || strings.Contains(value, "certificate")
}

func validateHTTPUnitCredential(source, inlineValue, environmentName, inlineKey, environmentKey string) error {
	if source == httpCredentialInline {
		if strings.TrimSpace(inlineValue) == "" {
			return fmt.Errorf("HttpUnit: params.%s is required for inline authentication", inlineKey)
		}
		return nil
	}
	if !validEnvironmentVariableName(environmentName) {
		return fmt.Errorf("HttpUnit: params.%s must be a valid environment variable name", environmentKey)
	}
	return nil
}

func validEnvironmentVariableName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func resolveHTTPUnitCredentials(ctx context.Context, params *httpUnitParams) error {
	if params.AuthType == "none" || params.CredentialSource == httpCredentialInline {
		return nil
	}
	var name string
	var destination *string
	switch params.AuthType {
	case "bearer":
		name, destination = params.BearerTokenEnv, &params.BearerToken
	case "basic":
		name, destination = params.BasicPasswordEnv, &params.BasicPassword
	case "api_key":
		name, destination = params.APIKeyValueEnv, &params.APIKeyValue
	default:
		return fmt.Errorf("HttpUnit: unsupported auth_type %q", params.AuthType)
	}
	value, err := credential.Resolve(ctx, name)
	if err != nil {
		return fmt.Errorf("HttpUnit: resolve credential %q: %w", name, err)
	}
	*destination = value
	return nil
}

func httpUnitBody(mode string, value any) (io.Reader, string, error) {
	if mode == httpBodyModeMultipart {
		return httpUnitMultipartBody(value)
	}
	switch typed := value.(type) {
	case nil:
		return nil, "", nil
	case string:
		return strings.NewReader(typed), "", nil
	case []byte:
		return bytes.NewReader(typed), "", nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, "", fmt.Errorf("HttpUnit: encode body: %w", err)
		}
		return bytes.NewReader(encoded), "application/json", nil
	}
}

type httpUnitFileValue struct {
	Name      string
	MediaType string
	Data      []byte
}

func httpUnitMultipartBody(value any) (io.Reader, string, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("HttpUnit: multipart body must be an object, got %T", value)
	}
	if len(fields) == 0 {
		return nil, "", errors.New("HttpUnit: multipart body requires at least one field")
	}
	if len(fields) > maxHTTPMultipartFields {
		return nil, "", fmt.Errorf("HttpUnit: multipart body exceeds %d fields", maxHTTPMultipartFields)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
			return nil, "", fmt.Errorf("HttpUnit: multipart field name %q is invalid", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	totalFileBytes := 0
	for _, key := range keys {
		value := fields[key]
		if raw, isFile := value.(map[string]any); isFile && raw["kind"] == httpFileValueKind {
			file, err := decodeHTTPUnitFileValue(raw)
			if err != nil {
				return nil, "", fmt.Errorf("HttpUnit: multipart field %q: %w", key, err)
			}
			totalFileBytes += len(file.Data)
			if totalFileBytes > maxHTTPMultipartTotalBytes {
				return nil, "", fmt.Errorf("HttpUnit: multipart files exceed %d bytes", maxHTTPMultipartTotalBytes)
			}
			header := make(textproto.MIMEHeader)
			disposition := mime.FormatMediaType("form-data", map[string]string{"name": key, "filename": file.Name})
			if disposition == "" {
				return nil, "", fmt.Errorf("HttpUnit: multipart field %q has invalid metadata", key)
			}
			header.Set("Content-Disposition", disposition)
			header.Set("Content-Type", file.MediaType)
			part, err := writer.CreatePart(header)
			if err != nil {
				return nil, "", fmt.Errorf("HttpUnit: create multipart file field %q: %w", key, err)
			}
			if _, err := part.Write(file.Data); err != nil {
				return nil, "", fmt.Errorf("HttpUnit: write multipart file field %q: %w", key, err)
			}
			continue
		}
		text, err := httpUnitMultipartText(value)
		if err != nil {
			return nil, "", fmt.Errorf("HttpUnit: multipart field %q: %w", key, err)
		}
		if len(text) > maxHTTPMultipartFieldBytes {
			return nil, "", fmt.Errorf("HttpUnit: multipart field %q exceeds %d bytes", key, maxHTTPMultipartFieldBytes)
		}
		if err := writer.WriteField(key, text); err != nil {
			return nil, "", fmt.Errorf("HttpUnit: write multipart field %q: %w", key, err)
		}
		if body.Len() > maxHTTPMultipartBodyBytes {
			return nil, "", fmt.Errorf("HttpUnit: multipart body exceeds %d bytes", maxHTTPMultipartBodyBytes)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("HttpUnit: close multipart body: %w", err)
	}
	if body.Len() > maxHTTPMultipartBodyBytes {
		return nil, "", fmt.Errorf("HttpUnit: multipart body exceeds %d bytes", maxHTTPMultipartBodyBytes)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType(), nil
}

func decodeHTTPUnitFileValue(raw map[string]any) (*httpUnitFileValue, error) {
	for key := range raw {
		switch key {
		case "kind", "name", "media_type", "size", "data_base64":
		default:
			return nil, fmt.Errorf("file value contains unsupported field %q", key)
		}
	}
	name, nameOK := raw["name"].(string)
	name = strings.TrimSpace(name)
	if !nameOK || name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\\\r\n\x00") || utf8.RuneCountInString(name) > 255 {
		return nil, errors.New("file name must be a safe basename of at most 255 characters")
	}
	mediaType, mediaTypeOK := raw["media_type"].(string)
	mediaType = strings.TrimSpace(mediaType)
	if !mediaTypeOK || mediaType == "" {
		mediaType = "application/octet-stream"
	}
	parsedMediaType, _, err := mime.ParseMediaType(mediaType)
	if err != nil || !strings.Contains(parsedMediaType, "/") {
		return nil, errors.New("file media_type must be a valid MIME type")
	}
	dataBase64, dataOK := raw["data_base64"].(string)
	if !dataOK {
		return nil, errors.New("file data_base64 is required")
	}
	if len(dataBase64) > base64.StdEncoding.EncodedLen(maxHTTPMultipartFileBytes) {
		return nil, fmt.Errorf("file exceeds %d bytes", maxHTTPMultipartFileBytes)
	}
	data, err := base64.StdEncoding.Strict().DecodeString(dataBase64)
	if err != nil {
		return nil, errors.New("file data_base64 is invalid")
	}
	if len(data) > maxHTTPMultipartFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxHTTPMultipartFileBytes)
	}
	size, sizeOK := raw["size"].(float64)
	if !sizeOK || size < 0 || size != float64(int64(size)) || int64(size) != int64(len(data)) {
		return nil, errors.New("file size does not match decoded data")
	}
	return &httpUnitFileValue{Name: name, MediaType: parsedMediaType, Data: data}, nil
}

func httpUnitMultipartText(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float64:
		if !isFiniteNumber(typed) {
			return "", errors.New("number must be finite")
		}
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("value must be text, number, boolean, null, or a file; got %T", value)
	}
}

func isFiniteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (t *HttpUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewHttpUnit() HttpUnit {
	unit := HttpUnit{}
	unit.UnitName = unit.GetUnitName()
	return unit
}
