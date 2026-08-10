package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
)

var (
	publishedDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	publishedTimePattern = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)
)

var publishedContractLocales = [...]string{"zh-CN", "zh-TW", "en", "ja", "es", "bo"}

type PublishedAPIContract struct {
	WorkflowID   string                   `json:"workflow_id"`
	VersionID    string                   `json:"version_id"`
	Version      int                      `json:"version"`
	Name         string                   `json:"name"`
	Description  string                   `json:"description,omitempty"`
	Route        string                   `json:"route"`
	URL          string                   `json:"url,omitempty"`
	InvokeURL    string                   `json:"invoke_url,omitempty"`
	AsyncURL     string                   `json:"async_url,omitempty"`
	StreamURL    string                   `json:"stream_url_template,omitempty"`
	Method       string                   `json:"method"`
	InputMode    string                   `json:"input_mode"`
	ResponseMode string                   `json:"response_mode"`
	TimeoutMS    int64                    `json:"timeout"`
	Inputs       []PublishedAPIInputField `json:"inputs,omitempty"`
	Example      map[string]any           `json:"request_example,omitempty"`
}

type PublishedAPIInputField struct {
	Key         string                       `json:"key"`
	Label       map[string]string            `json:"label"`
	Description map[string]string            `json:"description,omitempty"`
	Kind        string                       `json:"kind"`
	Required    bool                         `json:"required,omitempty"`
	Default     any                          `json:"default,omitempty"`
	Options     []PublishedAPIInputOption    `json:"options,omitempty"`
	VisibleWhen *PublishedAPIInputVisibility `json:"visible_when,omitempty"`
	hasDefault  bool
}

type PublishedAPIInputOption struct {
	Value string            `json:"value"`
	Label map[string]string `json:"label"`
}

type PublishedAPIInputVisibility struct {
	Key       string `json:"key"`
	Equals    any    `json:"equals,omitempty"`
	NotEquals any    `json:"not_equals,omitempty"`
	hasEquals bool
}

func parsePublishedAPIInputs(def *definition.WorkflowDefinition) ([]PublishedAPIInputField, error) {
	if def == nil || def.Metadata == nil || def.Metadata["run_form"] == nil {
		return nil, nil
	}
	runForm, ok := def.Metadata["run_form"].(map[string]any)
	if !ok {
		return nil, errors.New("metadata.run_form must be an object")
	}
	rawFields, ok := runForm["fields"].([]any)
	if !ok || len(rawFields) == 0 {
		return nil, errors.New("metadata.run_form.fields must be a non-empty array")
	}
	fields := make([]PublishedAPIInputField, 0, len(rawFields))
	seen := make(map[string]struct{}, len(rawFields))
	for index, raw := range rawFields {
		field, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("metadata.run_form.fields[%d] must be an object", index)
		}
		parsed, err := parsePublishedAPIInput(field, index, seen)
		if err != nil {
			return nil, err
		}
		seen[parsed.Key] = struct{}{}
		fields = append(fields, parsed)
	}
	return fields, nil
}

func parsePublishedAPIInput(raw map[string]any, index int, previous map[string]struct{}) (PublishedAPIInputField, error) {
	path := fmt.Sprintf("metadata.run_form.fields[%d]", index)
	key, _ := raw["key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return PublishedAPIInputField{}, fmt.Errorf("%s.key is required", path)
	}
	if key == "request" {
		return PublishedAPIInputField{}, fmt.Errorf("%s.key request is reserved", path)
	}
	if _, duplicate := previous[key]; duplicate {
		return PublishedAPIInputField{}, fmt.Errorf("%s.key %s is duplicated", path, key)
	}
	kind, _ := raw["kind"].(string)
	switch kind {
	case "text", "textarea", "select", "number", "date", "time", "switch":
	default:
		return PublishedAPIInputField{}, fmt.Errorf("%s.kind %q is not supported", path, kind)
	}
	label, err := parsePublishedLocalizedText(raw["label"], path+".label", true)
	if err != nil {
		return PublishedAPIInputField{}, err
	}
	description, err := parsePublishedLocalizedText(raw["description"], path+".description", false)
	if err != nil {
		return PublishedAPIInputField{}, err
	}
	required := false
	if value, exists := raw["required"]; exists {
		var valid bool
		required, valid = value.(bool)
		if !valid {
			return PublishedAPIInputField{}, fmt.Errorf("%s.required must be a boolean", path)
		}
	}
	parsed := PublishedAPIInputField{Key: key, Label: label, Description: description, Kind: kind, Required: required}
	if value, exists := raw["default"]; exists {
		parsed.Default, err = normalizePublishedInputValue(kind, value, nil, "body")
		if err != nil {
			return PublishedAPIInputField{}, fmt.Errorf("%s.default: %w", path, err)
		}
		parsed.hasDefault = true
		if parsed.Required && publishedInputValueEmpty(kind, parsed.Default) {
			return PublishedAPIInputField{}, fmt.Errorf("%s.default must not be empty for a required field", path)
		}
	}
	if kind == "select" {
		options, optionErr := parsePublishedAPIOptions(raw["options"], path+".options")
		if optionErr != nil {
			return PublishedAPIInputField{}, optionErr
		}
		parsed.Options = options
		if parsed.hasDefault && !publishedOptionContains(options, parsed.Default) {
			return PublishedAPIInputField{}, fmt.Errorf("%s.default must match an option value", path)
		}
	} else if raw["options"] != nil {
		return PublishedAPIInputField{}, fmt.Errorf("%s.options is only valid for select fields", path)
	}
	if raw["visible_when"] != nil {
		visibility, visibilityErr := parsePublishedVisibility(raw["visible_when"], path+".visible_when", previous)
		if visibilityErr != nil {
			return PublishedAPIInputField{}, visibilityErr
		}
		parsed.VisibleWhen = visibility
	}
	return parsed, nil
}

func parsePublishedLocalizedText(value any, path string, required bool) (map[string]string, error) {
	if value == nil && !required {
		return nil, nil
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must contain all six locale strings", path)
	}
	result := make(map[string]string, len(publishedContractLocales))
	for _, locale := range publishedContractLocales {
		text, ok := raw[locale].(string)
		text = strings.TrimSpace(text)
		if !ok || text == "" {
			return nil, fmt.Errorf("%s.%s must be a non-empty string", path, locale)
		}
		result[locale] = text
	}
	return result, nil
}

func parsePublishedAPIOptions(value any, path string) ([]PublishedAPIInputOption, error) {
	rawOptions, ok := value.([]any)
	if !ok || len(rawOptions) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty array", path)
	}
	options := make([]PublishedAPIInputOption, 0, len(rawOptions))
	seen := make(map[string]struct{}, len(rawOptions))
	for index, raw := range rawOptions {
		option, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be an object", path, index)
		}
		value, _ := option["value"].(string)
		if _, duplicate := seen[value]; value == "" || duplicate {
			return nil, fmt.Errorf("%s[%d].value must be non-empty and unique", path, index)
		}
		label, err := parsePublishedLocalizedText(option["label"], fmt.Sprintf("%s[%d].label", path, index), true)
		if err != nil {
			return nil, err
		}
		seen[value] = struct{}{}
		options = append(options, PublishedAPIInputOption{Value: value, Label: label})
	}
	return options, nil
}

func parsePublishedVisibility(value any, path string, previous map[string]struct{}) (*PublishedAPIInputVisibility, error) {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	key, _ := raw["key"].(string)
	if _, exists := previous[key]; !exists {
		return nil, fmt.Errorf("%s.key must reference an earlier field", path)
	}
	equals, hasEquals := raw["equals"]
	notEquals, hasNotEquals := raw["not_equals"]
	if hasEquals == hasNotEquals {
		return nil, fmt.Errorf("%s must contain exactly one of equals or not_equals", path)
	}
	return &PublishedAPIInputVisibility{Key: key, Equals: equals, NotEquals: notEquals, hasEquals: hasEquals}, nil
}

func applyPublishedInputContract(def *definition.WorkflowDefinition, variables map[string]any, inputMode string) error {
	if inputMode != "body" && inputMode != "query" {
		return nil
	}
	fields, err := parsePublishedAPIInputs(def)
	if err != nil {
		return err
	}
	for _, field := range fields {
		if !publishedInputVisible(field, variables) {
			continue
		}
		value, exists := variables[field.Key]
		if !exists {
			if field.hasDefault {
				variables[field.Key] = field.Default
				continue
			}
			if field.Required {
				return fmt.Errorf("published API input %s is required", field.Key)
			}
			continue
		}
		normalized, normalizeErr := normalizePublishedInputValue(field.Kind, value, field.Options, inputMode)
		if normalizeErr != nil {
			return fmt.Errorf("published API input %s: %w", field.Key, normalizeErr)
		}
		if field.Required && publishedInputValueEmpty(field.Kind, normalized) {
			return fmt.Errorf("published API input %s is required", field.Key)
		}
		variables[field.Key] = normalized
	}
	return nil
}

func publishedInputValueEmpty(kind string, value any) bool {
	if kind != "text" && kind != "textarea" && kind != "date" && kind != "time" && kind != "select" {
		return false
	}
	text, ok := value.(string)
	return !ok || strings.TrimSpace(text) == ""
}

func publishedInputVisible(field PublishedAPIInputField, variables map[string]any) bool {
	if field.VisibleWhen == nil {
		return true
	}
	value, exists := variables[field.VisibleWhen.Key]
	if !exists {
		return false
	}
	if field.VisibleWhen.hasEquals {
		return reflect.DeepEqual(value, field.VisibleWhen.Equals)
	}
	return !reflect.DeepEqual(value, field.VisibleWhen.NotEquals)
}

func normalizePublishedInputValue(kind string, value any, options []PublishedAPIInputOption, inputMode string) (any, error) {
	switch kind {
	case "number":
		switch number := value.(type) {
		case json.Number:
			parsed, err := number.Float64()
			if err != nil {
				return nil, errors.New("must be a number")
			}
			return parsed, nil
		case float64:
			return number, nil
		case float32:
			return float64(number), nil
		case int:
			return float64(number), nil
		case int64:
			return float64(number), nil
		case string:
			if inputMode != "query" {
				return nil, errors.New("must be a number")
			}
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return nil, errors.New("must be a number")
			}
			return parsed, nil
		default:
			return nil, errors.New("must be a number")
		}
	case "switch":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		if text, ok := value.(string); ok && inputMode == "query" {
			boolean, err := strconv.ParseBool(text)
			if err == nil {
				return boolean, nil
			}
		}
		return nil, errors.New("must be a boolean")
	default:
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("must be a string")
		}
		if kind == "date" && !publishedDatePattern.MatchString(text) {
			return nil, errors.New("must use YYYY-MM-DD")
		}
		if kind == "time" && !publishedTimePattern.MatchString(text) {
			return nil, errors.New("must use HH:MM")
		}
		if kind == "select" && len(options) > 0 && !publishedOptionContains(options, text) {
			return nil, errors.New("must match a configured option")
		}
		return text, nil
	}
}

func publishedOptionContains(options []PublishedAPIInputOption, value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	for _, option := range options {
		if option.Value == text {
			return true
		}
	}
	return false
}

func buildPublishedAPIContract(version *definition.WorkflowVersion) (*PublishedAPIContract, error) {
	if version == nil || version.Definition == nil || version.Definition.PublishConfig == nil || !version.Definition.PublishConfig.Enabled {
		return nil, errors.New("workflow is not published as an API")
	}
	config := version.Definition.PublishConfig
	if err := validatePublishAdapter(version.Definition, config); err != nil {
		return nil, err
	}
	inputs, err := parsePublishedAPIInputs(version.Definition)
	if err != nil {
		return nil, err
	}
	inputMode := config.InputMode
	if inputMode == "" {
		inputMode = "request"
	}
	responseMode := config.ResponseMode
	if responseMode == "" {
		responseMode = "run"
	}
	contract := &PublishedAPIContract{
		WorkflowID: version.WorkflowID, VersionID: version.ID, Version: version.Version,
		Name: version.Definition.Name, Description: version.Definition.Description,
		Route: strings.TrimSpace(config.Route), Method: strings.ToUpper(strings.TrimSpace(config.Method)),
		InputMode: inputMode, ResponseMode: responseMode, TimeoutMS: config.TimeoutMS,
		Inputs: inputs,
	}
	if inputMode == "body" || inputMode == "query" {
		contract.Example = publishedAPIRequestExample(inputs)
	}
	return contract, nil
}

func publishedAPIRequestExample(fields []PublishedAPIInputField) map[string]any {
	example := make(map[string]any, len(fields))
	for _, field := range fields {
		if !publishedInputVisible(field, example) {
			continue
		}
		if field.hasDefault {
			example[field.Key] = field.Default
			continue
		}
		switch field.Kind {
		case "number":
			example[field.Key] = 0
		case "switch":
			example[field.Key] = false
		case "date":
			example[field.Key] = "2026-01-01"
		case "time":
			example[field.Key] = "09:00"
		case "select":
			if len(field.Options) > 0 {
				example[field.Key] = field.Options[0].Value
			}
		default:
			example[field.Key] = "string"
		}
	}
	return example
}

func buildPublishedInvokeRequest(ctx context.Context, version *definition.WorkflowVersion, input map[string]any) (*http.Request, error) {
	if version == nil || version.Definition == nil || version.Definition.PublishConfig == nil {
		return nil, errors.New("published workflow configuration is unavailable")
	}
	config := version.Definition.PublishConfig
	target := config.Route
	var body *bytes.Reader
	switch config.InputMode {
	case "query":
		query := url.Values{}
		for key, value := range input {
			switch values := value.(type) {
			case []any:
				for _, item := range values {
					query.Add(key, fmt.Sprint(item))
				}
			case []string:
				for _, item := range values {
					query.Add(key, item)
				}
			default:
				query.Set(key, fmt.Sprint(value))
			}
		}
		if encoded := query.Encode(); encoded != "" {
			target += "?" + encoded
		}
		body = bytes.NewReader(nil)
	case "", "request", "body":
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode published API test input: %w", err)
		}
		body = bytes.NewReader(encoded)
	default:
		return nil, fmt.Errorf("unsupported published API input_mode %q", config.InputMode)
	}
	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(config.Method), target, body)
	if err != nil {
		return nil, fmt.Errorf("build published API test request: %w", err)
	}
	if config.InputMode != "query" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func publishedWorkflowPayload(def *definition.WorkflowDefinition, run *wfruntime.WorkflowRun) (map[string]any, error) {
	if def == nil || def.PublishConfig == nil || run == nil {
		return nil, errors.New("published workflow response is unavailable")
	}
	if def.PublishConfig.ResponseMode == "result" {
		result, err := publishedWorkflowResult(def, run)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"result": result,
			"run_id": run.ID,
			"status": run.Status,
			"source": "publish",
		}, nil
	}
	return map[string]any{"run": run, "source": "publish"}, nil
}
