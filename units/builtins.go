package units

import (
	"fmt"

	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

var builtinRegistrations = []workerunit.Registration{
	{Name: "AdjustDateUnit", Factory: func() workerunit.ExecutableUnit { return &AdjustDateUnit{} }},
	{Name: "AskForInputUnit", Factory: func() workerunit.ExecutableUnit { return &AskForInputUnit{} }},
	{Name: "BranchJoinUnit", Factory: func() workerunit.ExecutableUnit { return &BranchJoinUnit{} }},
	{Name: "BreakUnit", Factory: func() workerunit.ExecutableUnit { return &BreakUnit{} }},
	{Name: "CalculateUnit", Factory: func() workerunit.ExecutableUnit { return &CalculateUnit{} }},
	{Name: "ChangeCaseUnit", Factory: func() workerunit.ExecutableUnit { return &ChangeCaseUnit{} }},
	{Name: "ChooseFromMenuUnit", Factory: func() workerunit.ExecutableUnit { return &ChooseFromMenuUnit{} }},
	{Name: "CombineTextUnit", Factory: func() workerunit.ExecutableUnit { return &CombineTextUnit{} }},
	{Name: "ContinueUnit", Factory: func() workerunit.ExecutableUnit { return &ContinueUnit{} }},
	{Name: "CountUnit", Factory: func() workerunit.ExecutableUnit { return &CountUnit{} }},
	{Name: "DateUnit", Factory: func() workerunit.ExecutableUnit { return &DateUnit{} }},
	{Name: "DictionaryUnit", Factory: func() workerunit.ExecutableUnit { return &DictionaryUnit{} }},
	{Name: "FormatDateUnit", Factory: func() workerunit.ExecutableUnit { return &FormatDateUnit{} }},
	{Name: "GetDictionaryValueUnit", Factory: func() workerunit.ExecutableUnit { return &GetDictionaryValueUnit{} }},
	{Name: "GetListItemUnit", Factory: func() workerunit.ExecutableUnit { return &GetListItemUnit{} }},
	{Name: "GotoUnit", Factory: func() workerunit.ExecutableUnit { return &GotoUnit{} }},
	{Name: "HttpUnit", Factory: func() workerunit.ExecutableUnit { return &HttpUnit{} }},
	{Name: "IfUnit", Factory: logicAliasFactory("IfUnit")},
	{Name: "LLMUnit", Factory: func() workerunit.ExecutableUnit { return &LLMUnit{} }},
	{Name: "ListUnit", Factory: func() workerunit.ExecutableUnit { return &ListUnit{} }},
	{Name: "LogUnit", Factory: logicAliasFactory("LogUnit")},
	{Name: "LogicUnit", Factory: logicAliasFactory("LogicUnit")},
	{Name: "OpenAPIOperationUnit", Factory: func() workerunit.ExecutableUnit { return &OpenAPIOperationUnit{} }},
	{Name: "ReadableUnit", Factory: func() workerunit.ExecutableUnit { return &ReadableUnit{Format: "text"} }},
	{Name: "RemarkUnit", Factory: func() workerunit.ExecutableUnit { return &RemarkUnit{} }},
	{Name: "ReplaceTextUnit", Factory: func() workerunit.ExecutableUnit { return &ReplaceTextUnit{} }},
	{Name: "ScriptUnit", Factory: func() workerunit.ExecutableUnit { return &ScriptUnit{Language: "javascript"} }},
	{Name: "SetEnvUnit", Factory: func() workerunit.ExecutableUnit { return &SetEnvUnit{} }},
	{Name: "SplitTextUnit", Factory: func() workerunit.ExecutableUnit { return &SplitTextUnit{} }},
	{Name: "TerminalUnit", Factory: func() workerunit.ExecutableUnit { return &TerminalUnit{} }},
	{Name: "TextUnit", Factory: func() workerunit.ExecutableUnit { return &TextUnit{} }},
	{Name: "TimeoutUnit", Factory: func() workerunit.ExecutableUnit { return &TimeoutUnit{} }},
}

// BuiltinRegistrations returns a copy of the standard unit catalog. Callers can
// select a subset without mutating package-level state.
func BuiltinRegistrations() []workerunit.Registration {
	return append([]workerunit.Registration(nil), builtinRegistrations...)
}

func BuiltinNames() []string {
	names := make([]string, 0, len(builtinRegistrations))
	for _, registration := range builtinRegistrations {
		names = append(names, registration.Name)
	}
	return names
}

// RegisterBuiltins atomically installs the standard unit catalog into reg.
func RegisterBuiltins(reg *workerunit.Registry) error {
	if reg == nil {
		return fmt.Errorf("register builtin units: unit registry is nil")
	}
	if err := reg.RegisterAll(BuiltinRegistrations()...); err != nil {
		return fmt.Errorf("register builtin units: %w", err)
	}
	return nil
}
