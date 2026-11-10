// Package expr compiles the CEL expressions used in pipeline specs (conditions,
// routes, foreach lists, input projections) so that errors surface at validation
// time instead of in the middle of a run.
package expr

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
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
