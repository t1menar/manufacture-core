// Package expr compiles the CEL expressions used in pipeline specs (conditions,
// routes, foreach lists, input projections) so that errors surface at validation
// time instead of in the middle of a run.
package expr

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"google.golang.org/protobuf/types/known/structpb"
)

// Scope selects the variables an expression may use.
type Scope int

const (
	// ScopeRun has data, steps, run and attempt: stage conditions, routes, foreach and step inputs.
	ScopeRun Scope = iota
	// ScopeItem adds item and index: steps of a foreach stage.
	ScopeItem
)

// Result is the kind of value an expression must produce.
type Result int

const (
	// Any accepts every result type.
	Any Result = iota
	// Bool requires a boolean (conditions and routes).
	Bool
	// List requires a list (foreach).
	List
)

func (r Result) String() string {
	switch r {
	case Bool:
		return "bool"
	case List:
		return "list"
	default:
		return "any"
	}
}

// Variables available to every expression. Run data has no static type, so it is dyn.
func runVariables() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Variable("data", cel.DynType),
		cel.Variable("steps", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("run", cel.DynType),
		cel.Variable("attempt", cel.IntType),
		// visit is the visit number of the stage being evaluated (1 on the first pass);
		// steps.<id>.visits[n], steps.<id>.last and steps.<id>.visit hold the history.
		cel.Variable("visit", cel.IntType),
	}
}

var envs = sync.OnceValues(func() (map[Scope]*cel.Env, error) {
	run, err := cel.NewEnv(runVariables()...)
	if err != nil {
		return nil, err
	}
	item, err := run.Extend(cel.Variable("item", cel.DynType), cel.Variable("index", cel.IntType))
	if err != nil {
		return nil, err
	}
	return map[Scope]*cel.Env{ScopeRun: run, ScopeItem: item}, nil
})

// Check parses and type-checks src in the scope and verifies its result type.
func Check(src string, scope Scope, want Result) error {
	all, err := envs()
	if err != nil { // the environment is static, so this is a programming error
		panic(err)
	}
	env, ok := all[scope]
	if !ok {
		return fmt.Errorf("unknown expression scope %d", scope)
	}
	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return errors.New(oneLine(iss.Err().Error()))
	}
	out := ast.OutputType()
	if out.IsExactType(types.DynType) || want == Any {
		return nil
	}
	switch {
	case want == Bool && out.IsExactType(types.BoolType):
		return nil
	case want == List && out.Kind() == types.ListKind:
		return nil
	}
	return fmt.Errorf("must evaluate to %s, got %s", want, out)
}

// oneLine keeps the first line of a CEL error; the rest is a caret drawing that
// does not fit a one-line diagnostic.
func oneLine(s string) string {
	s = strings.TrimPrefix(s, "ERROR: <input>:")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, " expecting {"); i >= 0 { // the token list is longer than the expression
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// Vars are the variables of one evaluation. Values are JSON-like Go values
// (map[string]any, []any, string, float64, bool, nil); visits maps use int64 keys.
type Vars map[string]any

var programs sync.Map // key: scope + "\x00" + src -> cel.Program

func program(src string, scope Scope) (cel.Program, error) {
	key := fmt.Sprintf("%d\x00%s", scope, src)
	if p, ok := programs.Load(key); ok {
		return p.(cel.Program), nil //nolint:forcetypeassert // only programs are stored.
	}
	all, err := envs()
	if err != nil {
		panic(err)
	}
	env := all[scope]
	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return nil, errors.New(oneLine(iss.Err().Error()))
	}
	p, err := env.Program(ast, cel.CostLimit(1_000_000), cel.InterruptCheckFrequency(100))
	if err != nil {
		return nil, err
	}
	programs.Store(key, p)
	return p, nil
}

// Eval evaluates src with vars and returns a JSON-like Go value. Variables of
// the scope that vars does not set are bound to null (attempt and visit to 0).
func Eval(src string, scope Scope, vars Vars) (any, error) {
	p, err := program(src, scope)
	if err != nil {
		return nil, err
	}
	in := map[string]any{"data": map[string]any{}, "steps": map[string]any{}, "run": map[string]any{}, "attempt": int64(0), "visit": int64(0)}
	if scope == ScopeItem {
		in["item"], in["index"] = nil, int64(0)
	}
	for k, v := range vars {
		in[k] = v
	}
	out, _, err := p.Eval(in)
	if err != nil {
		return nil, errors.New(oneLine(err.Error()))
	}
	return toJSON(out)
}

// EvalBool evaluates a condition.
func EvalBool(src string, scope Scope, vars Vars) (bool, error) {
	v, err := Eval(src, scope, vars)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("condition evaluated to %T, not bool", v)
	}
	return b, nil
}

func toJSON(v ref.Val) (any, error) {
	pv, err := v.ConvertToNative(reflect.TypeFor[*structpb.Value]())
	if err != nil {
		return nil, fmt.Errorf("result is not JSON: %w", err)
	}
	return pv.(*structpb.Value).AsInterface(), nil //nolint:forcetypeassert // requested type.
}
