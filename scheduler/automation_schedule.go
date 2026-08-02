package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
)

type AutomationFrequency string

const (
	AutomationDaily    AutomationFrequency = "daily"
	AutomationWeekdays AutomationFrequency = "weekdays"
	AutomationWeekly   AutomationFrequency = "weekly"
	AutomationInterval AutomationFrequency = "interval"
)

type AutomationScheduleConfig struct {
	Frequency       AutomationFrequency `json:"frequency"`
	Time            string              `json:"time,omitempty"`
	Weekdays        []int               `json:"weekdays,omitempty"`
	IntervalMinutes int                 `json:"interval_minutes,omitempty"`
	Timezone        string              `json:"timezone"`
}

var supportedAutomationIntervals = map[int]struct{}{
	5: {}, 15: {}, 30: {}, 60: {}, 180: {}, 360: {}, 720: {},
}

func ParseAutomationSchedule(trigger definition.Trigger) (AutomationScheduleConfig, error) {
	if trigger.Type != definition.TriggerCron {
		return AutomationScheduleConfig{}, errors.New("automation trigger must use type cron")
	}
	if strings.TrimSpace(trigger.ID) == "" {
		return AutomationScheduleConfig{}, errors.New("automation trigger id is required")
	}
	config := AutomationScheduleConfig{
		Frequency: AutomationFrequency(configString(trigger.Config, "frequency")),
		Time:      configString(trigger.Config, "time"),
		Timezone:  configString(trigger.Config, "timezone"),
	}
	if config.Timezone == "" {
		return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s timezone is required", trigger.ID)
	}
	if _, err := time.LoadLocation(config.Timezone); err != nil {
		return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s timezone is invalid", trigger.ID)
	}
	switch config.Frequency {
	case AutomationDaily, AutomationWeekdays:
		if _, _, err := parseClockTime(config.Time); err != nil {
			return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s: %w", trigger.ID, err)
		}
	case AutomationWeekly:
		if _, _, err := parseClockTime(config.Time); err != nil {
			return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s: %w", trigger.ID, err)
		}
		weekdays, err := configWeekdays(trigger.Config["weekdays"])
		if err != nil {
			return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s: %w", trigger.ID, err)
		}
		config.Weekdays = weekdays
	case AutomationInterval:
		interval, err := configInteger(trigger.Config["interval_minutes"])
		if err != nil {
			return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s interval is invalid", trigger.ID)
		}
		if _, supported := supportedAutomationIntervals[interval]; !supported {
			return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s interval is not supported", trigger.ID)
		}
		config.IntervalMinutes = interval
	default:
		return AutomationScheduleConfig{}, fmt.Errorf("automation trigger %s frequency is not supported", trigger.ID)
	}
	return config, nil
}

func NextAutomationOccurrence(config AutomationScheduleConfig, after time.Time) (time.Time, error) {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return time.Time{}, errors.New("automation timezone is invalid")
	}
	if config.Frequency == AutomationInterval {
		if _, supported := supportedAutomationIntervals[config.IntervalMinutes]; !supported {
			return time.Time{}, errors.New("automation interval is not supported")
		}
		return after.Add(time.Duration(config.IntervalMinutes) * time.Minute).UTC(), nil
	}
	hour, minute, err := parseClockTime(config.Time)
	if err != nil {
		return time.Time{}, err
	}
	weekdays := make(map[time.Weekday]bool)
	switch config.Frequency {
	case AutomationDaily:
		for weekday := time.Sunday; weekday <= time.Saturday; weekday++ {
			weekdays[weekday] = true
		}
	case AutomationWeekdays:
		for weekday := time.Monday; weekday <= time.Friday; weekday++ {
			weekdays[weekday] = true
		}
	case AutomationWeekly:
		if len(config.Weekdays) == 0 {
			return time.Time{}, errors.New("automation weekdays are required")
		}
		for _, weekday := range config.Weekdays {
			parsed, parseErr := weekdayNumber(weekday)
			if parseErr != nil {
				return time.Time{}, parseErr
			}
			weekdays[parsed] = true
		}
	default:
		return time.Time{}, errors.New("automation frequency is not supported")
	}

	localAfter := after.In(location)
	for dayOffset := 0; dayOffset <= 8; dayOffset++ {
		day := localAfter.AddDate(0, 0, dayOffset)
		if !weekdays[day.Weekday()] {
			continue
		}
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, location)
		// A skipped DST wall-clock time is omitted instead of silently running
		// at a different local hour.
		if candidate.Hour() != hour || candidate.Minute() != minute {
			continue
		}
		if candidate.After(after) {
			return candidate.UTC(), nil
		}
	}
	return time.Time{}, errors.New("automation next occurrence could not be calculated")
}

func automationScheduleFingerprint(versionID string, trigger definition.Trigger, config AutomationScheduleConfig) (string, error) {
	payload, err := json.Marshal(struct {
		VersionID string                   `json:"version_id"`
		TriggerID string                   `json:"trigger_id"`
		Config    AutomationScheduleConfig `json:"config"`
	}{
		VersionID: versionID,
		TriggerID: trigger.ID,
		Config:    config,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func automationScheduleKey(workflowID, triggerID string) string {
	sum := sha256.Sum256([]byte(workflowID + "\x00" + triggerID))
	return hex.EncodeToString(sum[:16])
}

func automationRunID(scheduleKey string, scheduledAt time.Time) string {
	sum := sha256.Sum256([]byte(scheduleKey + "\x00" + scheduledAt.UTC().Format(time.RFC3339Nano)))
	return "auto-" + hex.EncodeToString(sum[:12])
}

func validateAutomationTriggers(triggers []definition.Trigger) error {
	ids := make(map[string]struct{}, len(triggers))
	for _, trigger := range triggers {
		trigger.ID = strings.TrimSpace(trigger.ID)
		if trigger.ID == "" {
			return errors.New("workflow trigger id is required")
		}
		if _, exists := ids[trigger.ID]; exists {
			return fmt.Errorf("workflow trigger id is duplicated: %s", trigger.ID)
		}
		ids[trigger.ID] = struct{}{}
		switch trigger.Type {
		case definition.TriggerManual, definition.TriggerHTTP:
		case definition.TriggerCron:
			if trigger.Enabled {
				if _, err := ParseAutomationSchedule(trigger); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("workflow trigger type is not supported: %s", trigger.Type)
		}
	}
	return nil
}

func configString(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func configInteger(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int32:
		return int(typed), nil
	case int64:
		return int(typed), nil
	case float64:
		if typed != float64(int(typed)) {
			return 0, errors.New("integer is required")
		}
		return int(typed), nil
	case json.Number:
		parsed, err := strconv.Atoi(string(typed))
		if err != nil {
			return 0, errors.New("integer is required")
		}
		return parsed, nil
	default:
		return 0, errors.New("integer is required")
	}
}

func configWeekdays(value any) ([]int, error) {
	var source []any
	switch typed := value.(type) {
	case []any:
		source = typed
	case []int:
		source = make([]any, len(typed))
		for index, weekday := range typed {
			source[index] = weekday
		}
	default:
		return nil, errors.New("automation weekdays are required")
	}
	if len(source) == 0 {
		return nil, errors.New("automation weekdays are required")
	}
	unique := make(map[int]struct{}, len(source))
	for _, value := range source {
		weekday, err := configInteger(value)
		if err != nil {
			return nil, errors.New("automation weekday is invalid")
		}
		if _, err := weekdayNumber(weekday); err != nil {
			return nil, err
		}
		unique[weekday] = struct{}{}
	}
	result := make([]int, 0, len(unique))
	for weekday := range unique {
		result = append(result, weekday)
	}
	sort.Ints(result)
	return result, nil
}

func weekdayNumber(value int) (time.Weekday, error) {
	if value < 1 || value > 7 {
		return 0, errors.New("automation weekday must be between 1 and 7")
	}
	if value == 7 {
		return time.Sunday, nil
	}
	return time.Weekday(value), nil
}

func parseClockTime(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, errors.New("automation time must use HH:MM")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("automation time must use HH:MM")
	}
	return hour, minute, nil
}
