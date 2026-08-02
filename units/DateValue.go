package units

import (
	"fmt"
	"strings"
	"time"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

func workflowDateInput(action string, self *unit.Node) (time.Time, error) {
	if self == nil || self.Input == nil {
		return time.Time{}, fmt.Errorf("%s: missing date input", action)
	}
	switch value := self.Input.Data.(type) {
	case time.Time:
		return value, nil
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return time.Time{}, fmt.Errorf("%s: date input must be non-empty RFC 3339 text", action)
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s: date input must be RFC 3339: %w", action, err)
		}
		return parsed, nil
	default:
		return time.Time{}, fmt.Errorf("%s: date input must be RFC 3339 text, got %T", action, self.Input.Data)
	}
}

func workflowDateTimezone(action string, params map[string]any, defaultTimezone string) (string, error) {
	return workflowDateChoice(action, params, "timezone", defaultTimezone)
}

func workflowDateChoice(action string, params map[string]any, key, defaultValue string) (string, error) {
	configured, exists := params[key]
	if !exists {
		return defaultValue, nil
	}
	text, ok := configured.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s: %s must be non-empty text", action, key)
	}
	return strings.TrimSpace(text), nil
}

func workflowDateLocation(action string, timezone string) (*time.Location, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("%s: load timezone %q: %w", action, timezone, err)
	}
	return location, nil
}

func workflowDateResult(action string, value time.Time) (string, error) {
	if value.Year() < 0 || value.Year() > 9999 {
		return "", fmt.Errorf("%s: adjusted date is outside the RFC 3339 year range", action)
	}
	return value.Format(time.RFC3339), nil
}
