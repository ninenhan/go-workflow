package fn

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// SmartTime 可识别多种时间格式，统一序列化输出格式
type SmartTime struct {
	time.Time
}

// 输出格式（可修改）
const outputLayout = "2006-01-02 15:04:05"

var possibleLayouts = []string{
	time.RFC3339,          // 2025-11-12T15:04:05Z
	"2006-01-02 15:04:05", // 2025-11-12 15:04:05
	"2006-01-02",          // 2025-11-12
	"2006-01-02 15:04",
	time.DateTime,         // 2025/11/12 15:04:05
	time.DateOnly,         // 2025/11/12
	"2006-01-02T15:04:05", // ISO 格式无 Z
}

// MarshalJSON 统一输出格式
func (t SmartTime) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte(`""`), nil
	}
	return []byte(`"` + t.Format(outputLayout) + `"`), nil
}

// UnmarshalJSON 可自动识别多种时间格式
func (t *SmartTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" {
		t.Time = time.Time{}
		return nil
	}

	// 尝试多种格式解析
	for _, layout := range possibleLayouts {
		if parsed, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			t.Time = parsed
			return nil
		}
	}

	// 尝试解析为时间戳
	if ts, err := parseTimestamp(s); err == nil {
		t.Time = time.Unix(ts, 0)
		return nil
	}

	return errors.New("invalid time format: " + s)
}

// parseTimestamp 支持秒/毫秒/微秒/纳秒级别
func parseTimestamp(s string) (int64, error) {
	if len(s) == 0 {
		return 0, errors.New("empty timestamp")
	}
	// 去除小数点部分
	if i := strings.Index(s, "."); i >= 0 {
		s = s[:i]
	}
	n, err := json.Number(s).Int64()
	if err != nil {
		return 0, err
	}
	switch {
	case n > 1e18:
		return n / 1e9, nil // 纳秒
	case n > 1e15:
		return n / 1e6, nil // 微秒
	case n > 1e12:
		return n / 1e3, nil // 毫秒
	default:
		return n, nil // 秒
	}
}

// ParseSmartTime 自动识别 string / number / timestamp 转换为 time.Time
func ParseSmartTime(v any) (time.Time, error) {
	switch val := v.(type) {
	case time.Time:
		return val, nil

	case *time.Time:
		if val == nil {
			return time.Time{}, errors.New("nil *time.Time")
		}
		return *val, nil

	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return time.Time{}, errors.New("empty string")
		}

		for _, layout := range possibleLayouts {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return t, nil
			}
		}

		// 尝试纯数字（时间戳）
		if ts, err := strconv.ParseFloat(s, 64); err == nil {
			return parseTimestampToTime(ts)
		}

		return time.Time{}, errors.New("unknown string time format: " + s)

	case json.Number:
		if ts, err := val.Float64(); err == nil {
			return parseTimestampToTime(ts)
		}
		return time.Time{}, errors.New("invalid json.Number")

	case int64:
		return parseTimestampToTime(float64(val))
	case int, int32, float64:
		return parseTimestampToTime(reflect.ValueOf(val).Float())
	default:
		return time.Time{}, errors.New("unsupported type")
	}
}

// parseTimestamp 支持秒、毫秒、微秒、纳秒
func parseTimestampToTime(ts float64) (time.Time, error) {
	switch {
	case ts > 1e18:
		return time.Unix(0, int64(ts)), nil // 纳秒
	case ts > 1e15:
		return time.Unix(0, int64(ts*1e3)), nil // 微秒
	case ts > 1e12:
		return time.UnixMilli(int64(ts)), nil // 毫秒
	case ts > 1e9:
		return time.Unix(int64(ts), 0), nil // 秒
	default:
		return time.Time{}, errors.New("invalid timestamp range")
	}
}
