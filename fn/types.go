package fn

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unicode"
)

type FlexibleInt64 int64

func (f *FlexibleInt64) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		// 忽略 null，不覆盖
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	switch val := v.(type) {
	case float64:
		*f = FlexibleInt64(int64(val))
		return nil
	case string:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil
		}
		*f = FlexibleInt64(n)
		return nil
	default:
		return fmt.Errorf("unsupported type: %T", v)
	}
}

type FlexibleInt8 int8

func (f *FlexibleInt8) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		// 忽略 null，不覆盖
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	switch val := v.(type) {
	case float64:
		*f = FlexibleInt8(int64(val))
		return nil
	case string:
		n, err := strconv.ParseInt(val, 10, 8)
		if err != nil {
			return nil
		}
		*f = FlexibleInt8(n)
		return nil
	default:
		return fmt.Errorf("unsupported type: %T", v)
	}
}

type FlexibleFloat64 float64

func (f *FlexibleFloat64) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		// 忽略 null，不覆盖
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}

	switch val := v.(type) {
	case float64:
		*f = FlexibleFloat64(val)
		return nil

	case string:
		n, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil
		}
		*f = FlexibleFloat64(n)
		return nil

	default:
		return fmt.Errorf("unsupported type: %T", v)
	}
}

func CamelToSnake(s string) string {
	var result []rune
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, unicode.ToLower(r))
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}
