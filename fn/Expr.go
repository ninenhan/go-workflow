package fn

import (
	"errors"
	"fmt"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/shopspring/decimal"
	"math"
	"reflect"
	"regexp"
	"sort"
	"sync"
)

// progCache: map[reflect.Type]*sync.Map where inner key=code, val=*cachedProgram.
var progCache sync.Map

// evalPrecision controls rounding to tame float artifacts (e.g., 9*0.07 -> 0.63).
const evalPrecision = 12

type cachedProgram struct {
	prog *vm.Program
}

func sqrtFn(params ...any) (any, error) {
	if len(params) != 1 {
		return nil, fmt.Errorf("sqrt expects 1 argument")
	}
	var x float64
	switch v := params[0].(type) {
	case float64:
		x = v
	case int:
		x = float64(v)
	case int64:
		x = float64(v)
	default:
		return nil, fmt.Errorf("sqrt arg must be number, got %T", params[0])
	}

	if x < 0 {
		return nil, fmt.Errorf("sqrt of negative number: %v", x)
	}
	return math.Sqrt(x), nil
}
func envCache(envType reflect.Type) *sync.Map {
	if m, ok := progCache.Load(envType); ok {
		return m.(*sync.Map)
	}
	m := &sync.Map{}
	if actual, loaded := progCache.LoadOrStore(envType, m); loaded {
		return actual.(*sync.Map)
	}
	return m
}

func CompileExpr[E any](code string) (*vm.Program, error) {
	var zero E
	t := reflect.TypeOf(zero)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, errors.New("env must be a non-pointer struct type")
	}
	return expr.Compile(
		code,
		expr.Env(zero),
		expr.AsFloat64(),
		expr.MaxNodes(1024),       // 防止恶意超复杂表达式
		expr.DisableAllBuiltins(), // 禁用内置函数（重要）
		expr.Function("sqrt", sqrtFn),
	)
}

func compileWithDump[E any](code string) (*cachedProgram, error) {
	var zero E
	t := reflect.TypeOf(zero)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, errors.New("env must be a non-pointer struct type")
	}
	prog, err := expr.Compile(
		code,
		expr.Env(zero),
		expr.AsFloat64(),
		expr.MaxNodes(1024),
		expr.DisableAllBuiltins(),
		expr.Function("sqrt", sqrtFn),
	)
	if err != nil {
		return nil, err
	}
	return &cachedProgram{
		prog: prog,
	}, nil
}

func GetProgram[E any](code string) (*cachedProgram, error) {
	var zero E
	t := reflect.TypeOf(zero)
	cache := envCache(t)
	if p, ok := cache.Load(code); ok {
		return p.(*cachedProgram), nil
	}
	prog, err := compileWithDump[E](code)
	if err != nil {
		return nil, err
	}
	if p, loaded := cache.LoadOrStore(code, prog); loaded {
		return p.(*cachedProgram), nil
	}
	return prog, nil
}

func Eval[E any](code string, env E) (float64, error) {
	value, _, err := EvalWithTrace(code, env)
	return value, err
}

// EvalWithTrace returns the numeric result and the expression string used for compilation.
// (Using expr v1 API; no AST dump available here.)
func EvalWithTrace[E any](code string, env E) (float64, string, error) {
	prog, err := GetProgram[E](code)
	if err != nil {
		return 0, "", err
	}

	out, err := expr.Run(prog.prog, env)
	if err != nil {
		return 0, expandExpr(code, env), err
	}
	return roundFloat(out.(float64), evalPrecision), expandExpr(code, env), nil
}

func roundFloat(val float64, precision int) float64 {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return val
	}
	if precision <= 0 {
		return math.Round(val)
	}
	// Use decimal to avoid overflow when val is large and precision is high.
	d := decimal.NewFromFloat(val)
	scale := decimal.New(1, int32(precision))
	rounded, _ := d.Mul(scale).Round(0).Div(scale).Float64()
	return rounded
}

// expandExpr performs a simple string substitution of struct field names with their values.
// It is best-effort tracing and does not execute expressions; intended for debugging display.
func expandExpr(code string, env any) string {
	val := reflect.ValueOf(env)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return code
	}
	type pair struct {
		name  string
		value string
	}
	var fields []pair
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		// Only exported fields are readable via Interface; skip others.
		if !val.Field(i).CanInterface() {
			continue
		}
		fields = append(fields, pair{
			name:  typ.Field(i).Name,
			value: fmt.Sprintf("%v", val.Field(i).Interface()),
		})
	}
	// Replace longer names first to avoid partial replacements (e.g., ABC before A).
	sort.Slice(fields, func(i, j int) bool { return len(fields[i].name) > len(fields[j].name) })
	out := code
	for _, f := range fields {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(f.name) + `\b`)
		out = re.ReplaceAllString(out, f.value)
	}
	return out
}
