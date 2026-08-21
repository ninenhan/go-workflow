package units

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	unit "github.com/ninenhan/go-workflow/worker/unit"
)

// ChangeCaseUnit applies one selected Unicode-aware case transformation.
type ChangeCaseUnit struct {
	unit.Unit
}

var _ unit.ExecutableUnit = (*ChangeCaseUnit)(nil)

func (u *ChangeCaseUnit) GetUnitName() string {
	return reflect.TypeOf(ChangeCaseUnit{}).Name()
}

func (u *ChangeCaseUnit) Execute(_ context.Context, _ unit.ContextMap, self *unit.Node) (*unit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("ChangeCaseUnit: missing text input")
	}
	input, ok := splitTextInput(self.Input.Data)
	if !ok {
		return nil, fmt.Errorf("ChangeCaseUnit: input must be text, got %T", self.Input.Data)
	}
	mode := "upper"
	if configured, exists := self.Params["case"]; exists {
		mode = strings.TrimSpace(fmt.Sprint(configured))
	}

	var result string
	switch mode {
	case "upper":
		result = strings.ToUpper(input)
	case "lower":
		result = strings.ToLower(input)
	case "title":
		result = titleCaseText(input)
	case "sentence":
		result = sentenceCaseText(input)
	default:
		return nil, fmt.Errorf("ChangeCaseUnit: unsupported case %q", mode)
	}
	return &unit.ExecutionResult{NodeName: u.UnitName, Data: result}, nil
}

func titleCaseText(input string) string {
	wordStart := true
	return strings.Map(func(character rune) rune {
		switch {
		case unicode.IsLetter(character):
			if wordStart {
				wordStart = false
				return unicode.ToTitle(character)
			}
			return unicode.ToLower(character)
		case unicode.IsNumber(character):
			wordStart = false
		case character != '\'' && character != '’':
			wordStart = true
		}
		return character
	}, input)
}

func sentenceCaseText(input string) string {
	sentenceStart := true
	return strings.Map(func(character rune) rune {
		character = unicode.ToLower(character)
		if sentenceStart {
			switch {
			case unicode.IsLetter(character):
				sentenceStart = false
				return unicode.ToUpper(character)
			case unicode.IsNumber(character):
				sentenceStart = false
			}
		}
		if character == '.' || character == '!' || character == '?' {
			sentenceStart = true
		}
		return character
	}, input)
}

func (u *ChangeCaseUnit) GetUnitMeta() *unit.Unit {
	return &u.Unit
}

func NewChangeCaseUnit() ChangeCaseUnit {
	u := ChangeCaseUnit{}
	u.UnitName = u.GetUnitName()
	return u
}
