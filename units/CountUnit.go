package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// CountUnit counts a selected aspect of the incoming value.
type CountUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*CountUnit)(nil)

func (u *CountUnit) GetUnitName() string {
	return reflect.TypeOf(CountUnit{}).Name()
}

func (u *CountUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("CountUnit: missing input")
	}
	mode := "items"
	if configured, exists := self.Params["count"]; exists {
		mode = strings.TrimSpace(fmt.Sprint(configured))
	}

	var (
		count int
		err   error
	)
	switch mode {
	case "items":
		count, err = countCollectionItems(self.Input.Data)
	case "characters", "words", "lines":
		text, ok := splitTextInput(self.Input.Data)
		if !ok {
			return nil, fmt.Errorf("CountUnit: %s mode requires text input, got %T", mode, self.Input.Data)
		}
		switch mode {
		case "characters":
			count = utf8.RuneCountInString(text)
		case "words":
			count = countTextWords(text)
		case "lines":
			count = countTextLines(text)
		}
	default:
		return nil, fmt.Errorf("CountUnit: unsupported count %q", mode)
	}
	if err != nil {
		return nil, err
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: count}, nil
}

func countCollectionItems(value any) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("CountUnit: items mode requires a list, array, or dictionary")
	}
	if _, bytes := value.([]byte); bytes {
		return 0, fmt.Errorf("CountUnit: items mode requires a list, array, or dictionary, got []byte")
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Array, reflect.Slice, reflect.Map:
		return reflected.Len(), nil
	default:
		return 0, fmt.Errorf("CountUnit: items mode requires a list, array, or dictionary, got %T", value)
	}
}

func countTextWords(text string) int {
	count := 0
	inWord := false
	for _, character := range text {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			if !inWord {
				count++
			}
			inWord = true
			continue
		}
		if (character == '\'' || character == '’') && inWord {
			continue
		}
		inWord = false
	}
	return count
}

func countTextLines(text string) int {
	if text == "" {
		return 0
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	return strings.Count(normalized, "\n") + 1
}

func (u *CountUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewCountUnit() CountUnit {
	u := CountUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
