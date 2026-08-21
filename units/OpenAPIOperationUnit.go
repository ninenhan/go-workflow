package units

import (
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
	"unicode/utf8"

	coreexecutor "github.com/ninenhan/go-workflow/core/executor"
	unit "github.com/ninenhan/go-workflow/worker/unit"
)

const openAPIArgumentPrefix = "argument:"

var openAPIFieldKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var openAPIHeaderNamePattern = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

type OpenAPIOperationUnit struct {
	unit.Unit
	client *http.Client
}

var _ unit.ExecutableUnit = (*OpenAPIOperationUnit)(nil)

type openAPIValueRules struct {
	Type             string   `json:"type,omitempty"`
	AllowedValues    []string `json:"allowed_values,omitempty"`
	MinLength        *int     `json:"min_length,omitempty"`
	MaxLength        *int     `json:"max_length,omitempty"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum bool     `json:"exclusive_minimum,omitempty"`
	ExclusiveMaximum bool     `json:"exclusive_maximum,omitempty"`
	MinItems         *int     `json:"min_items,omitempty"`
	MaxItems         *int     `json:"max_items,omitempty"`
}

type openAPIOperationParameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Key      string `json:"key"`
	Required bool   `json:"required,omitempty"`
	openAPIValueRules
}

type openAPIOperationBodyField struct {
	Name         string   `json:"name"`
	Key          string   `json:"key"`
	Path         []string `json:"path,omitempty"`
	Required     bool     `json:"required,omitempty"`
	Nullable     bool     `json:"nullable,omitempty"`
	VariantKey   string   `json:"variant_key,omitempty"`
	VariantValue string   `json:"variant_value,omitempty"`
	openAPIValueRules
}

type openAPIOperationVariantGroup struct {
	Key               string   `json:"key"`
	Required          bool     `json:"required,omitempty"`
	Values            []string `json:"values"`
	DiscriminatorPath []string `json:"discriminator_path,omitempty"`
}

type openAPIOperationParams struct {
	BaseURL          string                         `json:"base_url"`
	Path             string                         `json:"path"`
	Method           string                         `json:"method"`
	Parameters       []openAPIOperationParameter    `json:"parameters,omitempty"`
	BodyFields       []openAPIOperationBodyField    `json:"body_fields,omitempty"`
	VariantGroups    []openAPIOperationVariantGroup `json:"variant_groups,omitempty"`
	ContentType      string                         `json:"content_type,omitempty"`
	TimeoutMS        int                            `json:"timeout_ms"`
	AuthType         string                         `json:"auth_type,omitempty"`
	CredentialSource string                         `json:"credential_source,omitempty"`
	BearerToken      string                         `json:"bearer_token,omitempty"`
	BearerTokenEnv   string                         `json:"bearer_token_env,omitempty"`
	BasicUsername    string                         `json:"basic_username,omitempty"`
	BasicPassword    string                         `json:"basic_password,omitempty"`
	BasicPasswordEnv string                         `json:"basic_password_env,omitempty"`
	APIKeyName       string                         `json:"api_key_name,omitempty"`
	APIKeyValue      string                         `json:"api_key_value,omitempty"`
	APIKeyValueEnv   string                         `json:"api_key_value_env,omitempty"`
	APIKeyLocation   string                         `json:"api_key_location,omitempty"`
	AcceptedStatuses []string                       `json:"accepted_statuses,omitempty"`
	ResponseMode     string                         `json:"response_mode,omitempty"`
	ResponseFields   any                            `json:"response_fields,omitempty"`
	RetryMode        string                         `json:"retry_mode,omitempty"`
}

func (t *OpenAPIOperationUnit) GetUnitName() string {
	return reflect.TypeOf(OpenAPIOperationUnit{}).Name()
}

func (t *OpenAPIOperationUnit) Execute(
	ctx context.Context,
	state unit.ContextMap,
	self *unit.Node,
) (*unit.ExecutionResult, error) {
	if self == nil {
		return nil, errors.New("OpenAPIOperationUnit: missing node")
	}
	config, err := decodeOpenAPIOperationParams(self.Params)
	if err != nil {
		return nil, coreexecutor.PermanentFailure(err)
	}
	requestParams, err := config.httpParams(self.Params)
	if err != nil {
		return nil, coreexecutor.PermanentFailure(err)
	}
	if err := validateHTTPUnitConfiguration(requestParams); err != nil {
		return nil, coreexecutor.PermanentFailure(fmt.Errorf("OpenAPIOperationUnit: %w", err))
	}
	data, err := executeHTTPUnitRequest(ctx, requestParams, nil, t.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("OpenAPIOperationUnit: %w", err)
	}
	return &unit.ExecutionResult{NodeName: t.UnitName, Data: data}, nil
}

func decodeOpenAPIOperationParams(raw map[string]any) (*openAPIOperationParams, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("OpenAPIOperationUnit: encode params: %w", err)
	}
	params := &openAPIOperationParams{}
	if err := json.Unmarshal(encoded, params); err != nil {
		return nil, fmt.Errorf("OpenAPIOperationUnit: decode params: %w", err)
	}
	params.BaseURL = strings.TrimSpace(params.BaseURL)
	base, err := url.Parse(params.BaseURL)
	if err != nil || base.Scheme != "http" && base.Scheme != "https" || base.Host == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("OpenAPIOperationUnit: params.base_url must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	params.Path = strings.TrimSpace(params.Path)
	if !strings.HasPrefix(params.Path, "/") || strings.Contains(params.Path, "?") || strings.Contains(params.Path, "#") {
		return nil, errors.New("OpenAPIOperationUnit: params.path must be an absolute URL path")
	}
	for _, segment := range strings.Split(params.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("OpenAPIOperationUnit: params.path cannot contain dot segments")
		}
	}
	params.Method = strings.ToUpper(strings.TrimSpace(params.Method))
	switch params.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("OpenAPIOperationUnit: unsupported method %q", params.Method)
	}
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = 30000
	}
	params.ContentType = strings.TrimSpace(params.ContentType)
	if params.ContentType == "" {
		params.ContentType = "application/json"
	}
	params.AuthType = strings.ToLower(strings.TrimSpace(params.AuthType))
	if params.AuthType == "" {
		params.AuthType = "none"
	}
	params.CredentialSource = strings.ToLower(strings.TrimSpace(params.CredentialSource))
	if params.CredentialSource == "" {
		params.CredentialSource = httpCredentialInline
	}
	params.ResponseMode = strings.ToLower(strings.TrimSpace(params.ResponseMode))
	if params.ResponseMode == "" {
		params.ResponseMode = "auto"
	}
	params.RetryMode = strings.ToLower(strings.TrimSpace(params.RetryMode))
	if params.RetryMode == "" {
		params.RetryMode = httpRetryNone
	}
	if len(params.BodyFields) > 0 && params.ContentType != "application/json" && params.ContentType != "multipart/form-data" {
		return nil, errors.New("OpenAPIOperationUnit: request bodies must use application/json or multipart/form-data")
	}
	if len(params.BodyFields) > 0 && (params.Method == http.MethodGet || params.Method == http.MethodHead) {
		return nil, fmt.Errorf("OpenAPIOperationUnit: %s cannot define a request body", params.Method)
	}
	for index := range params.Parameters {
		params.Parameters[index].Name = strings.TrimSpace(params.Parameters[index].Name)
		params.Parameters[index].Key = strings.TrimSpace(params.Parameters[index].Key)
		params.Parameters[index].In = strings.ToLower(strings.TrimSpace(params.Parameters[index].In))
		normalizeOpenAPIValueRules(&params.Parameters[index].openAPIValueRules)
	}
	for index := range params.BodyFields {
		params.BodyFields[index].Name = strings.TrimSpace(params.BodyFields[index].Name)
		params.BodyFields[index].Key = strings.TrimSpace(params.BodyFields[index].Key)
		normalizeOpenAPIValueRules(&params.BodyFields[index].openAPIValueRules)
		params.BodyFields[index].VariantKey = strings.TrimSpace(params.BodyFields[index].VariantKey)
		params.BodyFields[index].VariantValue = strings.TrimSpace(params.BodyFields[index].VariantValue)
		if len(params.BodyFields[index].Path) == 0 {
			params.BodyFields[index].Path = []string{params.BodyFields[index].Name}
		}
		for pathIndex := range params.BodyFields[index].Path {
			params.BodyFields[index].Path[pathIndex] = strings.TrimSpace(params.BodyFields[index].Path[pathIndex])
		}
	}
	if params.ContentType == "multipart/form-data" {
		if len(params.VariantGroups) > 0 {
			return nil, errors.New("OpenAPIOperationUnit: multipart bodies cannot use variant groups")
		}
		for _, field := range params.BodyFields {
			if len(field.Path) != 1 || field.VariantKey != "" {
				return nil, fmt.Errorf("OpenAPIOperationUnit: multipart field %q must be top-level", field.Key)
			}
			if field.Type == "file" && field.Nullable {
				return nil, fmt.Errorf("OpenAPIOperationUnit: multipart file field %q cannot be nullable", field.Key)
			}
		}
	} else {
		for _, field := range params.BodyFields {
			if field.Type == "file" {
				return nil, fmt.Errorf("OpenAPIOperationUnit: file field %q requires multipart/form-data", field.Key)
			}
		}
	}
	for index := range params.VariantGroups {
		params.VariantGroups[index].Key = strings.TrimSpace(params.VariantGroups[index].Key)
		for valueIndex := range params.VariantGroups[index].Values {
			params.VariantGroups[index].Values[valueIndex] = strings.TrimSpace(params.VariantGroups[index].Values[valueIndex])
		}
		for pathIndex := range params.VariantGroups[index].DiscriminatorPath {
			params.VariantGroups[index].DiscriminatorPath[pathIndex] = strings.TrimSpace(params.VariantGroups[index].DiscriminatorPath[pathIndex])
		}
	}
	if err := validateOpenAPIFields(params.Path, params.Parameters, params.BodyFields, params.VariantGroups); err != nil {
		return nil, err
	}
	return params, nil
}

func normalizeOpenAPIValueRules(rules *openAPIValueRules) {
	rules.Type = strings.ToLower(strings.TrimSpace(rules.Type))
}

func validateOpenAPIFields(
	pathTemplate string,
	parameters []openAPIOperationParameter,
	bodyFields []openAPIOperationBodyField,
	variantGroups []openAPIOperationVariantGroup,
) error {
	keys := make(map[string]struct{}, len(parameters)+len(bodyFields)+len(variantGroups))
	groups := make(map[string]map[string]struct{}, len(variantGroups))
	bodyPaths := make([]openAPIOperationBodyField, 0, len(bodyFields))
	discriminatorPaths := make([][]string, 0, len(variantGroups))
	pathNames := make(map[string]struct{})
	for _, group := range variantGroups {
		if !openAPIFieldKeyPattern.MatchString(group.Key) || len(group.Values) < 2 || len(group.Values) > 12 {
			return errors.New("OpenAPIOperationUnit: variant groups must have a valid key and 2-12 values")
		}
		if _, exists := keys[group.Key]; exists {
			return fmt.Errorf("OpenAPIOperationUnit: duplicate argument key %q", group.Key)
		}
		values := make(map[string]struct{}, len(group.Values))
		for _, value := range group.Values {
			if value == "" {
				return fmt.Errorf("OpenAPIOperationUnit: variant group %q contains an empty value", group.Key)
			}
			if _, exists := values[value]; exists {
				return fmt.Errorf("OpenAPIOperationUnit: variant group %q contains duplicate value %q", group.Key, value)
			}
			values[value] = struct{}{}
		}
		keys[group.Key] = struct{}{}
		groups[group.Key] = values
		if len(group.DiscriminatorPath) > 0 {
			if len(group.DiscriminatorPath) > 8 {
				return fmt.Errorf("OpenAPIOperationUnit: variant group %q discriminator path exceeds 8 levels", group.Key)
			}
			for _, segment := range group.DiscriminatorPath {
				if segment == "" {
					return fmt.Errorf("OpenAPIOperationUnit: variant group %q discriminator path contains an empty segment", group.Key)
				}
			}
			for _, existing := range discriminatorPaths {
				if openAPIBodyPathsConflict(existing, group.DiscriminatorPath) {
					return fmt.Errorf("OpenAPIOperationUnit: conflicting discriminator path %q", strings.Join(group.DiscriminatorPath, "."))
				}
			}
			discriminatorPaths = append(discriminatorPaths, group.DiscriminatorPath)
		}
	}
	for _, parameter := range parameters {
		if parameter.Name == "" || !openAPIFieldKeyPattern.MatchString(parameter.Key) {
			return errors.New("OpenAPIOperationUnit: parameter names and keys must be valid")
		}
		switch parameter.In {
		case "path":
			if !parameter.Required {
				return fmt.Errorf("OpenAPIOperationUnit: path parameter %q must be required", parameter.Name)
			}
			pathNames[parameter.Name] = struct{}{}
		case "query":
		case "header":
			if !openAPIHeaderNamePattern.MatchString(parameter.Name) ||
				strings.EqualFold(parameter.Name, "Host") ||
				strings.EqualFold(parameter.Name, "Content-Length") {
				return fmt.Errorf("OpenAPIOperationUnit: header parameter %q is not allowed", parameter.Name)
			}
		default:
			return fmt.Errorf("OpenAPIOperationUnit: unsupported parameter location %q", parameter.In)
		}
		if parameter.Type == "file" {
			return fmt.Errorf("OpenAPIOperationUnit: parameter %q cannot be a file", parameter.Name)
		}
		if strings.HasSuffix(parameter.Type, "[]") {
			return fmt.Errorf("OpenAPIOperationUnit: parameter %q cannot use a list type", parameter.Name)
		}
		if err := validateOpenAPIValueRules(parameter.Key, parameter.openAPIValueRules); err != nil {
			return err
		}
		if _, exists := keys[parameter.Key]; exists {
			return fmt.Errorf("OpenAPIOperationUnit: duplicate argument key %q", parameter.Key)
		}
		keys[parameter.Key] = struct{}{}
	}
	for _, field := range bodyFields {
		if field.Name == "" || !openAPIFieldKeyPattern.MatchString(field.Key) ||
			len(field.Path) == 0 || len(field.Path) > 8 ||
			field.Path[len(field.Path)-1] != field.Name {
			return errors.New("OpenAPIOperationUnit: body field names and keys must be valid")
		}
		if err := validateOpenAPIValueRules(field.Key, field.openAPIValueRules); err != nil {
			return err
		}
		for _, segment := range field.Path {
			if segment == "" {
				return errors.New("OpenAPIOperationUnit: body field path segments must be non-empty")
			}
		}
		if (field.VariantKey == "") != (field.VariantValue == "") {
			return fmt.Errorf("OpenAPIOperationUnit: body field %q has an incomplete variant", field.Name)
		}
		if field.VariantKey != "" {
			values, exists := groups[field.VariantKey]
			if !exists {
				return fmt.Errorf("OpenAPIOperationUnit: body field %q references unknown variant group %q", field.Name, field.VariantKey)
			}
			if _, exists := values[field.VariantValue]; !exists {
				return fmt.Errorf("OpenAPIOperationUnit: body field %q references unknown variant value %q", field.Name, field.VariantValue)
			}
		}
		if _, exists := keys[field.Key]; exists {
			return fmt.Errorf("OpenAPIOperationUnit: duplicate argument key %q", field.Key)
		}
		keys[field.Key] = struct{}{}
		for _, discriminatorPath := range discriminatorPaths {
			if openAPIBodyPathsConflict(discriminatorPath, field.Path) {
				return fmt.Errorf("OpenAPIOperationUnit: body field path %q conflicts with a discriminator", strings.Join(field.Path, "."))
			}
		}
		for _, existing := range bodyPaths {
			if openAPIBodyPathsConflict(existing.Path, field.Path) &&
				!openAPIBodyFieldsMutuallyExclusive(existing, field) {
				return fmt.Errorf("OpenAPIOperationUnit: conflicting body field path %q", strings.Join(field.Path, "."))
			}
		}
		bodyPaths = append(bodyPaths, field)
	}
	for name := range pathNames {
		if strings.Count(pathTemplate, "{"+name+"}") != 1 {
			return fmt.Errorf("OpenAPIOperationUnit: path parameter %q must appear exactly once", name)
		}
	}
	unresolved := regexp.MustCompile(`\{[^{}]+\}`).FindString(pathTemplate)
	if unresolved != "" {
		name := strings.TrimSuffix(strings.TrimPrefix(unresolved, "{"), "}")
		if _, exists := pathNames[name]; !exists {
			return fmt.Errorf("OpenAPIOperationUnit: path placeholder %q has no parameter", name)
		}
	}
	return nil
}

func validateOpenAPIValueRules(key string, rules openAPIValueRules) error {
	switch rules.Type {
	case "", "string", "number", "integer", "boolean", "file", "string[]", "number[]", "integer[]", "boolean[]":
	default:
		return fmt.Errorf("OpenAPIOperationUnit: argument %q has unsupported type %q", key, rules.Type)
	}
	itemType := strings.TrimSuffix(rules.Type, "[]")
	isList := strings.HasSuffix(rules.Type, "[]")
	hasLength := rules.MinLength != nil || rules.MaxLength != nil
	hasNumber := rules.Minimum != nil || rules.Maximum != nil || rules.ExclusiveMinimum || rules.ExclusiveMaximum
	hasItems := rules.MinItems != nil || rules.MaxItems != nil
	if rules.Type == "file" && (hasLength || hasNumber || hasItems || len(rules.AllowedValues) > 0) {
		return fmt.Errorf("OpenAPIOperationUnit: file argument %q cannot use scalar or list constraints", key)
	}
	if (hasLength && itemType != "string") ||
		(hasNumber && itemType != "number" && itemType != "integer") ||
		(hasItems && !isList) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q has constraints that do not match its type", key)
	}
	if isList && (hasLength || hasNumber) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q cannot apply scalar constraints to list items", key)
	}
	for name, value := range map[string]*int{
		"min_length": rules.MinLength,
		"max_length": rules.MaxLength,
		"min_items":  rules.MinItems,
		"max_items":  rules.MaxItems,
	} {
		if value != nil && *value < 0 {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q %s must be non-negative", key, name)
		}
	}
	if (rules.ExclusiveMinimum && rules.Minimum == nil) ||
		(rules.ExclusiveMaximum && rules.Maximum == nil) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q has an exclusive bound without its value", key)
	}
	if (rules.MinLength != nil && rules.MaxLength != nil && *rules.MinLength > *rules.MaxLength) ||
		(rules.MinItems != nil && rules.MaxItems != nil && *rules.MinItems > *rules.MaxItems) ||
		rules.Minimum != nil && rules.Maximum != nil &&
			(*rules.Minimum > *rules.Maximum ||
				(*rules.Minimum == *rules.Maximum && (rules.ExclusiveMinimum || rules.ExclusiveMaximum))) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q has invalid bounds", key)
	}
	if len(rules.AllowedValues) > 1000 {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q has too many allowed values", key)
	}
	allowed := make(map[string]struct{}, len(rules.AllowedValues))
	for _, value := range rules.AllowedValues {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q contains an empty allowed value", key)
		}
		if _, exists := allowed[value]; exists {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q contains duplicate allowed value %q", key, value)
		}
		switch itemType {
		case "number":
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return fmt.Errorf("OpenAPIOperationUnit: argument %q contains a non-numeric allowed value", key)
			}
		case "integer":
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return fmt.Errorf("OpenAPIOperationUnit: argument %q contains a non-integer allowed value", key)
			}
		case "boolean":
			if value != "true" && value != "false" {
				return fmt.Errorf("OpenAPIOperationUnit: argument %q contains a non-boolean allowed value", key)
			}
		}
		allowed[value] = struct{}{}
	}
	return nil
}

func (p *openAPIOperationParams) httpParams(raw map[string]any) (*httpUnitParams, error) {
	requestPath := p.Path
	query := make(map[string]any)
	headers := make(map[string]string)
	body := make(map[string]any)
	selectedVariants := make(map[string]string, len(p.VariantGroups))
	for _, group := range p.VariantGroups {
		value, exists := raw[openAPIArgumentPrefix+group.Key]
		if !exists || value == nil || value == "" {
			if group.Required {
				return nil, fmt.Errorf("OpenAPIOperationUnit: variant %q is required", group.Key)
			}
			continue
		}
		selected, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("OpenAPIOperationUnit: variant %q must be text", group.Key)
		}
		if !openAPIVariantValueAllowed(group.Values, selected) {
			return nil, fmt.Errorf("OpenAPIOperationUnit: variant %q has unsupported value %q", group.Key, selected)
		}
		selectedVariants[group.Key] = selected
		if len(group.DiscriminatorPath) > 0 {
			if err := setOpenAPIBodyValue(body, group.DiscriminatorPath, selected); err != nil {
				return nil, err
			}
		}
	}
	for _, parameter := range p.Parameters {
		value, exists := raw[openAPIArgumentPrefix+parameter.Key]
		if !exists || value == nil || value == "" {
			if parameter.Required {
				return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q is required", parameter.Key)
			}
			continue
		}
		typedValue, err := coerceOpenAPIValue(parameter.Key, parameter.openAPIValueRules, value)
		if err != nil {
			return nil, err
		}
		if err := validateOpenAPIValue(parameter.Key, parameter.openAPIValueRules, typedValue); err != nil {
			return nil, err
		}
		switch parameter.In {
		case "path":
			requestPath = strings.Replace(requestPath, "{"+parameter.Name+"}", escapeOpenAPIPathArgument(fmt.Sprint(typedValue)), 1)
		case "query":
			query[parameter.Name] = typedValue
		case "header":
			headerValue := fmt.Sprint(typedValue)
			if strings.ContainsAny(headerValue, "\r\n") {
				return nil, fmt.Errorf("OpenAPIOperationUnit: header argument %q contains a line break", parameter.Key)
			}
			headers[http.CanonicalHeaderKey(parameter.Name)] = headerValue
		}
	}
	for _, field := range p.BodyFields {
		if field.VariantKey != "" && selectedVariants[field.VariantKey] != field.VariantValue {
			continue
		}
		value, exists := raw[openAPIArgumentPrefix+field.Key]
		if !exists {
			if field.Required {
				return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q is required", field.Key)
			}
			continue
		}
		if value == nil {
			if !field.Nullable {
				return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q cannot be null", field.Key)
			}
			if err := setOpenAPIBodyValue(body, field.Path, nil); err != nil {
				return nil, err
			}
			continue
		}
		if openAPIArgumentMissing(value) {
			if field.Required {
				return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q is required", field.Key)
			}
			continue
		}
		typedValue, err := coerceOpenAPIValue(field.Key, field.openAPIValueRules, value)
		if err != nil {
			return nil, err
		}
		if err := validateOpenAPIValue(field.Key, field.openAPIValueRules, typedValue); err != nil {
			return nil, err
		}
		if err := setOpenAPIBodyValue(body, field.Path, typedValue); err != nil {
			return nil, err
		}
	}
	if strings.Contains(requestPath, "{") || strings.Contains(requestPath, "}") {
		return nil, errors.New("OpenAPIOperationUnit: request path contains an unresolved placeholder")
	}
	requestURL := strings.TrimSuffix(p.BaseURL, "/") + requestPath
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return nil, fmt.Errorf("OpenAPIOperationUnit: build request URL: %w", err)
	}
	queryValues := parsedURL.Query()
	for key, value := range query {
		queryValues.Set(key, fmt.Sprint(value))
	}
	parsedURL.RawQuery = queryValues.Encode()
	requestURL = parsedURL.String()
	bodyMode := httpBodyModeAuto
	if p.ContentType == "multipart/form-data" {
		bodyMode = httpBodyModeMultipart
	} else if len(body) > 0 {
		headers["Content-Type"] = p.ContentType
	}
	return &httpUnitParams{
		URL:              requestURL,
		Method:           p.Method,
		Headers:          headers,
		AuthType:         p.AuthType,
		CredentialSource: p.CredentialSource,
		BearerToken:      p.BearerToken,
		BearerTokenEnv:   p.BearerTokenEnv,
		BasicUsername:    p.BasicUsername,
		BasicPassword:    p.BasicPassword,
		BasicPasswordEnv: p.BasicPasswordEnv,
		APIKeyName:       p.APIKeyName,
		APIKeyValue:      p.APIKeyValue,
		APIKeyValueEnv:   p.APIKeyValueEnv,
		APIKeyLocation:   strings.ToLower(strings.TrimSpace(p.APIKeyLocation)),
		Body:             body,
		BodyMode:         bodyMode,
		AcceptedStatuses: p.AcceptedStatuses,
		ResponseMode:     p.ResponseMode,
		ResponseFields:   p.ResponseFields,
		TimeoutMS:        p.TimeoutMS,
		RetryMode:        p.RetryMode,
	}, nil
}

func openAPIVariantValueAllowed(values []string, selected string) bool {
	for _, value := range values {
		if value == selected {
			return true
		}
	}
	return false
}

func openAPIBodyFieldsMutuallyExclusive(left, right openAPIOperationBodyField) bool {
	return left.VariantKey != "" &&
		left.VariantKey == right.VariantKey &&
		left.VariantValue != right.VariantValue
}

func coerceOpenAPIValue(key string, rules openAPIValueRules, value any) (any, error) {
	if strings.HasSuffix(rules.Type, "[]") {
		reflected := reflect.ValueOf(value)
		if !reflected.IsValid() || reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q must be a list", key)
		}
		itemRules := rules
		itemRules.Type = strings.TrimSuffix(rules.Type, "[]")
		itemRules.MinItems = nil
		itemRules.MaxItems = nil
		items := make([]any, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			item, err := coerceOpenAPIValue(key, itemRules, reflected.Index(index).Interface())
			if err != nil {
				return nil, fmt.Errorf("%w at item %d", err, index+1)
			}
			items[index] = item
		}
		return items, nil
	}
	switch rules.Type {
	case "":
		return value, nil
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q must be text", key)
		}
		return text, nil
	case "number":
		number, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
		if err != nil {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q must be a number", key)
		}
		return number, nil
	case "integer":
		number, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q must be an integer", key)
		}
		return number, nil
	case "boolean":
		if boolean, ok := value.(bool); ok {
			return boolean, nil
		}
		boolean, err := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(value)))
		if err != nil {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q must be true or false", key)
		}
		return boolean, nil
	case "file":
		raw, ok := value.(map[string]any)
		if !ok || raw["kind"] != httpFileValueKind {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q must be a file", key)
		}
		if _, err := decodeHTTPUnitFileValue(raw); err != nil {
			return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q: %w", key, err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("OpenAPIOperationUnit: argument %q has unsupported type %q", key, rules.Type)
	}
}

func validateOpenAPIValue(key string, rules openAPIValueRules, value any) error {
	if strings.HasSuffix(rules.Type, "[]") {
		reflected := reflect.ValueOf(value)
		length := reflected.Len()
		if rules.MinItems != nil && length < *rules.MinItems {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q requires at least %d items", key, *rules.MinItems)
		}
		if rules.MaxItems != nil && length > *rules.MaxItems {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q allows at most %d items", key, *rules.MaxItems)
		}
		itemRules := rules
		itemRules.Type = strings.TrimSuffix(rules.Type, "[]")
		itemRules.MinItems = nil
		itemRules.MaxItems = nil
		for index := 0; index < length; index++ {
			if err := validateOpenAPIValue(key, itemRules, reflected.Index(index).Interface()); err != nil {
				return fmt.Errorf("%w at item %d", err, index+1)
			}
		}
		return nil
	}
	if len(rules.AllowedValues) > 0 && !openAPIVariantValueAllowed(rules.AllowedValues, fmt.Sprint(value)) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q has an unsupported value", key)
	}
	switch rules.Type {
	case "string":
		length := utf8.RuneCountInString(value.(string))
		if rules.MinLength != nil && length < *rules.MinLength {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q requires at least %d characters", key, *rules.MinLength)
		}
		if rules.MaxLength != nil && length > *rules.MaxLength {
			return fmt.Errorf("OpenAPIOperationUnit: argument %q allows at most %d characters", key, *rules.MaxLength)
		}
	case "number":
		return validateOpenAPINumber(key, rules, value.(float64))
	case "integer":
		return validateOpenAPINumber(key, rules, float64(value.(int64)))
	case "file":
		_, err := decodeHTTPUnitFileValue(value.(map[string]any))
		return err
	}
	return nil
}

func validateOpenAPINumber(key string, rules openAPIValueRules, value float64) error {
	if rules.Minimum != nil && (value < *rules.Minimum || rules.ExclusiveMinimum && value == *rules.Minimum) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q is below its minimum", key)
	}
	if rules.Maximum != nil && (value > *rules.Maximum || rules.ExclusiveMaximum && value == *rules.Maximum) {
		return fmt.Errorf("OpenAPIOperationUnit: argument %q exceeds its maximum", key)
	}
	return nil
}

func openAPIArgumentMissing(value any) bool {
	if value == nil || value == "" {
		return true
	}
	reflected := reflect.ValueOf(value)
	return (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) && reflected.Len() == 0
}

func openAPIBodyPathsConflict(left, right []string) bool {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func setOpenAPIBodyValue(body map[string]any, path []string, value any) error {
	current := body
	for _, segment := range path[:len(path)-1] {
		existing, exists := current[segment]
		if !exists {
			nested := make(map[string]any)
			current[segment] = nested
			current = nested
			continue
		}
		nested, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("OpenAPIOperationUnit: body path %q conflicts with a value", strings.Join(path, "."))
		}
		current = nested
	}
	current[path[len(path)-1]] = value
	return nil
}

func escapeOpenAPIPathArgument(value string) string {
	switch value {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		return url.PathEscape(value)
	}
}

func (t *OpenAPIOperationUnit) GetUnitMeta() *unit.Unit {
	return &t.Unit
}

func NewOpenAPIOperationUnit() OpenAPIOperationUnit {
	action := OpenAPIOperationUnit{}
	action.UnitName = action.GetUnitName()
	return action
}
