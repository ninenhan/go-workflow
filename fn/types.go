package fn

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"gorm.io/datatypes"
	"strconv"
	"strings"
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

// StringSlice 就是一个可以自动 JSON ↔ Go 切片的类型
type StringSlice []string

// Scan 把数据库中读出来的 JSON 反序列化到 StringSlice
func (s *StringSlice) Scan(src any) error {
	if src == nil {
		*s = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T", src)
	}
	if len(b) == 0 {
		*s = nil
		return nil
	}
	return json.Unmarshal(b, s)
}

func (s StringSlice) ToStringSlice() []string {
	return s
}

// Value 把 Go 切片序列化成 JSON，写回数据库
func (s StringSlice) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil // 写 SQL NULL
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(b), nil // MySQL JSON 列接受字符串形式的 JSON 文本
}

type IntTuple [2]int

func (p IntTuple) Value() (driver.Value, error) {
	return json.Marshal(p)
}

func (p *IntTuple) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan IntPair: %v", value)
	}
	return json.Unmarshal(bytes, p)
}

type FlexibleString string

func (s *FlexibleString) UnmarshalJSON(b []byte) error {
	// null → ""
	if bytes.Equal(b, []byte("null")) {
		*s = ""
		return nil
	}

	// 已经是字符串
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = FlexibleString(str)
		return nil
	}

	// 其他原子类型：number / bool
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}

	switch vv := v.(type) {
	case json.Number:
		sval := vv.String()
		// 去掉小数点尾零，避免 "123.000"
		if strings.Contains(sval, ".") {
			sval = strings.TrimRight(sval, "0")
			sval = strings.TrimRight(sval, ".")
		}
		*s = FlexibleString(sval)
	case bool:
		if vv {
			*s = "true"
		} else {
			*s = "false"
		}
	default:
		*s = FlexibleString(string(b))
	}
	return nil
}

// 转回 string 用
func (s FlexibleString) String() string {
	return string(s)
}

func MapToJSON(m map[string]any) (datatypes.JSON, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
}
