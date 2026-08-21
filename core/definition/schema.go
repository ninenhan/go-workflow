package definition

import (
	"reflect"
	"strings"
	"time"
)

const WorkflowContractVersion = 1

var workflowContractEnums = map[reflect.Type][]string{
	reflect.TypeOf(EdgeKind("")):    {string(EdgeKindNormal), string(EdgeKindBack)},
	reflect.TypeOf(TriggerType("")): {string(TriggerManual), string(TriggerHTTP), string(TriggerCron)},
	reflect.TypeOf(InputMode("")):   {string(InputModeReplace), string(InputModeObject), string(InputModeArray)},
	reflect.TypeOf(InputSource("")): {string(InputSourceNode), string(InputSourceVar), string(InputSourceRequest), string(InputSourceRun)},
	reflect.TypeOf(BranchMode("")):  {string(BranchAll), string(BranchFirst)},
	reflect.TypeOf(VersionStatus("")): {
		string(VersionDraft), string(VersionPublished), string(VersionArchived),
	},
}

// WorkflowDefinitionJSONSchema is generated from the server domain types. It is
// descriptive of the wire structure; semantic execution rules remain owned by
// the compiler and are applied by /v1/validate, version creation, and run APIs.
func WorkflowDefinitionJSONSchema() map[string]any {
	builder := workflowSchemaBuilder{definitions: map[string]any{}}
	root := builder.schema(reflect.TypeOf(WorkflowDefinition{}), true)
	return map[string]any{
		"$schema":            "https://json-schema.org/draft/2020-12/schema",
		"$id":                "https://go-workflow.local/contracts/workflow-definition-v1.schema.json",
		"title":              "WorkflowDefinition",
		"x-contract-version": WorkflowContractVersion,
		"allOf":              []any{root},
		"$defs":              builder.definitions,
	}
}

type workflowSchemaBuilder struct {
	definitions map[string]any
	building    map[reflect.Type]bool
}

func (b *workflowSchemaBuilder) schema(value reflect.Type, reference bool) any {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if value == reflect.TypeOf(time.Duration(0)) {
		return map[string]any{"type": "integer", "format": "duration-nanoseconds"}
	}
	if values := workflowContractEnums[value]; len(values) > 0 {
		return map[string]any{"type": "string", "enum": values}
	}

	switch value.Kind() {
	case reflect.Interface:
		return map[string]any{}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": b.schema(value.Elem(), true)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": b.schema(value.Elem(), true)}
	case reflect.Struct:
		name := value.Name()
		if reference && name != "" {
			if _, exists := b.definitions[name]; !exists {
				if b.building == nil {
					b.building = map[reflect.Type]bool{}
				}
				if !b.building[value] {
					b.building[value] = true
					b.definitions[name] = b.structSchema(value)
					delete(b.building, value)
				}
			}
			return map[string]any{"$ref": "#/$defs/" + name}
		}
		return b.structSchema(value)
	default:
		return map[string]any{}
	}
}

func (b *workflowSchemaBuilder) structSchema(value reflect.Type) map[string]any {
	properties := map[string]any{}
	required := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		optional := false
		for _, option := range parts[1:] {
			optional = optional || option == "omitempty"
		}
		properties[name] = b.schema(field.Type, true)
		if !optional {
			required = append(required, name)
		}
	}
	result := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}
