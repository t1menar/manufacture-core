package engine

import "github.com/t1menar/manufacture-core/internal/spec/expr"

func evalRoute(src string, vars expr.Vars) (bool, error) {
	return expr.EvalBool(src, expr.ScopeRun, vars)
}
