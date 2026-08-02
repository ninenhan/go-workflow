package units

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/ninenhan/go-workflow/core/credential"
	unit "github.com/ninenhan/go-workflow/worker/unit"
)

const (
	defaultLLMBaseURL   = "https://api.openai.com/v1/chat/completions"
	defaultLLMAPIKeyEnv = "OPENAI_API_KEY"
	defaultLLMTimeoutMS = 30000
)

// LLMUnit executes a single OpenAI-compatible chat completion request.
type LLMUnit struct {
	unit.Unit
	client *http.Client
}

var _ unit.ExecutableUnit = (*LLMUnit)(nil)

type llmParams struct {
	BaseURL     string   `json:"base_url,omitempty"`
	Model       string   `json:"model"`
	System      string   `json:"system,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	APIKeyEnv   string   `json:"api_key_env,omitempty"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
}

type llmChatRequest struct {
	Model       string           `json:"model"`
	Messages    []llmChatMessage `json:"messages"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

type llmChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (u *LLMUnit) GetUnitName() string {
	return reflect.TypeOf(LLMUnit{}).Name()
}

func (u *LLMUnit) Execute(ctx context.Context, state unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, errors.New("LLMUnit: missing node")
	}
	if self.Input == nil {
		return nil, errors.New("LLMUnit: missing input")
	}

	params, err := decodeLLMParams(self.Params)
	if err != nil {
		return nil, fmt.Errorf("LLMUnit: invalid params: %w", err)
	}
	if strings.TrimSpace(params.Model) == "" {
		return nil, errors.New("LLMUnit: params.model is required")
	}

	apiKey, err := credential.Resolve(ctx, params.APIKeyEnv)
	if err != nil {
		return nil, fmt.Errorf("LLMUnit: resolve credential %s: %w", params.APIKeyEnv, err)
	}

	prompt, err := normalizePrompt(self.Input.Data)
	if err != nil {
		return nil, fmt.Errorf("LLMUnit: normalize input: %w", err)
	}

	payload := llmChatRequest{
		Model:       params.Model,
		Messages:    buildMessages(params.System, prompt),
		Temperature: params.Temperature,
		MaxTokens:   params.MaxTokens,
		Stream:      params.Stream,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpClient := u.client
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(params.TimeoutMS)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(runCtx, http.MethodPost, params.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	if params.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawBytes, err := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("LLMUnit: http %d: %s", resp.StatusCode, limitBody(string(rawBytes), 1024))
	}
	if err != nil {
		return nil, err
	}

	if params.Stream {
		text, chunks, usage, rawEvents, err := parseSSEChatCompletion(bytes.NewReader(rawBytes))
		if err != nil {
			return nil, err
		}
		return &unit.ExecutionResult{
			NodeName: u.UnitName,
			Data: map[string]any{
				"text":       text,
				"chunks":     chunks,
				"usage":      usage,
				"raw_events": rawEvents,
			},
			Stream: true,
			Raw:    rawEvents,
		}, nil
	}

	var raw map[string]any
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return nil, err
	}
	text := extractAssistantText(raw)

	return &unit.ExecutionResult{
		NodeName: u.UnitName,
		Data: map[string]any{
			"text":  text,
			"usage": raw["usage"],
			"raw":   raw,
		},
		Raw: raw,
	}, nil
}

func (u *LLMUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func decodeLLMParams(raw map[string]any) (*llmParams, error) {
	params := &llmParams{
		BaseURL:   defaultLLMBaseURL,
		APIKeyEnv: defaultLLMAPIKeyEnv,
		TimeoutMS: defaultLLMTimeoutMS,
	}
	if len(raw) == 0 {
		return params, nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(buf, params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.BaseURL) == "" {
		params.BaseURL = defaultLLMBaseURL
	}
	if strings.TrimSpace(params.APIKeyEnv) == "" {
		params.APIKeyEnv = defaultLLMAPIKeyEnv
	}
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = defaultLLMTimeoutMS
	}
	return params, nil
}

func normalizePrompt(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []byte:
		return string(x), nil
	default:
		buf, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
}

func buildMessages(system, prompt string) []llmChatMessage {
	msgs := make([]llmChatMessage, 0, 2)
	system = strings.TrimSpace(system)
	if system != "" {
		msgs = append(msgs, llmChatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, llmChatMessage{Role: "user", Content: prompt})
	return msgs
}

func extractAssistantText(raw map[string]any) string {
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}
	if text, ok := first["text"].(string); ok && text != "" {
		return text
	}
	msg, ok := first["message"].(map[string]any)
	if !ok {
		return ""
	}
	content, exists := msg["content"]
	if !exists {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := content.([]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		obj, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := obj["type"].(string); t != "" && t != "text" {
			continue
		}
		if text, _ := obj["text"].(string); text != "" {
			sb.WriteString(text)
		}
	}
	return sb.String()
}

func extractDeltaText(raw map[string]any) string {
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		return ""
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return ""
	}
	delta, ok := first["delta"].(map[string]any)
	if !ok {
		return ""
	}
	content, exists := delta["content"]
	if !exists {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := content.([]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		obj, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := obj["type"].(string); t != "" && t != "text" {
			continue
		}
		if text, _ := obj["text"].(string); text != "" {
			sb.WriteString(text)
		}
	}
	return sb.String()
}

func parseSSEChatCompletion(r io.Reader) (string, []string, any, []map[string]any, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		dataLines []string
		chunks    []string
		rawEvents []map[string]any
		usage     any
		text      strings.Builder
	)

	processEvent := func(lines []string) (done bool, err error) {
		if len(lines) == 0 {
			return false, nil
		}
		payload := strings.TrimSpace(strings.Join(lines, "\n"))
		if payload == "" {
			return false, nil
		}
		if payload == "[DONE]" {
			return true, nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			return false, fmt.Errorf("LLMUnit: parse stream event: %w", err)
		}
		rawEvents = append(rawEvents, raw)
		if u, ok := raw["usage"]; ok {
			usage = u
		}
		chunk := extractDeltaText(raw)
		if chunk != "" {
			chunks = append(chunks, chunk)
			text.WriteString(chunk)
		}
		return false, nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			done, err := processEvent(dataLines)
			if err != nil {
				return "", nil, nil, nil, err
			}
			dataLines = dataLines[:0]
			if done {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, nil, nil, err
	}
	if len(dataLines) > 0 {
		if _, err := processEvent(dataLines); err != nil {
			return "", nil, nil, nil, err
		}
	}
	return text.String(), chunks, usage, rawEvents, nil
}

func limitBody(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}

func init() {
	unit.RegisterUnitFactory("LLMUnit", func() unit.ExecutableUnit {
		u := &LLMUnit{}
		u.UnitName = u.GetUnitName()
		return u
	})
}
