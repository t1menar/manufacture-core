package expr

import (
	"reflect"
	"testing"
)

func TestEval(t *testing.T) {
	vars := Vars{
		"data":  map[string]any{"text": "hello", "n": 2.0},
		"visit": int64(2),
		"steps": map[string]any{
			"scenario": map[string]any{
				"output": map[string]any{"v": 2.0},
				"visits": map[int64]any{1: map[string]any{"output": map[string]any{"v": 1.0}}, 2: map[string]any{"output": map[string]any{"v": 2.0}}},
			},
			"review": map[string]any{"output": map[string]any{"decision": "rejected", "comment": "shorter"}},
		},
	}
	cases := []struct {
		src  string
		want any
	}{
		{`data.text + " world"`, "hello world"},
		{`visit > 1 ? steps.scenario.visits[visit - 1].output : {}`, map[string]any{"v": 1.0}},
		{`has(steps.review.output.comment) ? steps.review.output.comment : ''`, "shorter"},
		{`steps.review.output.decision == 'rejected'`, true},
		{`visit`, 2.0},
		{`{'a': [1, 2]}`, map[string]any{"a": []any{1.0, 2.0}}},
	}
	for _, c := range cases {
		got, err := Eval(c.src, ScopeRun, vars)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
	if _, err := EvalBool(`data.missing.x`, ScopeRun, vars); err == nil {
		t.Error("missing key must fail")
	}
}
