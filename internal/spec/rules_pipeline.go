package spec

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/t1menar/manufacture-core/internal/spec/expr"
)

func checkPipeline(r *reporter, p *Pipeline) {
	spec := &p.Spec
	sp := Path{"spec"}

	if t := spec.Trigger; t != nil && t.Schedule != "" {
		if err := checkCron(t.Schedule); err != nil {
			r.errorf(sp.Key("trigger").Key("schedule"), "%v", err)
		}
	}
	if c := spec.Context; c != nil && len(c.Schema) > 0 {
		if err := compileContextSchema(c.Schema); err != nil {
			r.errorf(sp.Key("context").Key("schema"), "invalid JSON Schema: %v", err)
		}
	}
	if spec.Defaults != nil {
		checkPolicy(r, sp.Key("defaults"), *spec.Defaults)
	}
	if b := spec.Budget; b != nil && b.WarnUSD != 0 && b.RunUSD != 0 && b.WarnUSD >= b.RunUSD {
		r.errorf(sp.Key("budget").Key("warn_usd"), "warn_usd (%g) must be below run_usd (%g)", b.WarnUSD, b.RunUSD)
	}

	stageIndex := make(map[string]int, len(spec.Stages))
	duplicate := make(map[int]bool)
	for i, st := range spec.Stages {
		if first, dup := stageIndex[st.Name]; dup {
			r.errorf(sp.Key("stages").Index(i).Key("name"), "stage %q is already defined at spec.stages[%d]", st.Name, first)
			duplicate[i] = true
			continue
		}
		stageIndex[st.Name] = i
	}

	checkMaxVisits(r, sp, spec.Stages)

	stepIDs := map[string]string{} // step id → path of its first definition
	for i := range spec.Stages {
		st := &spec.Stages[i]
		stp := sp.Key("stages").Index(i)
		if duplicate[i] || !checkStageShape(r, stp, st) {
			continue
		}
		assignStepIDs(st)
		checkStageExpressions(r, stp, st)
		checkRoutes(r, stp, i, st.Routes, stageIndex)
		forEachStep(*st, stp, func(s *Step, p Path) {
			if first, dup := stepIDs[s.ID]; dup {
				r.errorf(p, "step id %q is already used at %s; set a unique id", s.ID, first)
			} else {
				stepIDs[s.ID] = p.String()
			}
			checkStep(r, p, s, st.Kind(), spec.Defaults)
		})
	}
}

// checkMaxVisits requires routes into the same stage to agree on max_visits: it
// bounds the total visits of the target stage, whichever route leads there.
func checkMaxVisits(r *reporter, sp Path, stages []Stage) {
	type first struct {
		n    int
		path Path
	}
	byTarget := map[string]first{}
	for i, st := range stages {
		for j, rt := range st.Routes {
			if rt.MaxVisits == 0 {
				continue
			}
			p := sp.Key("stages").Index(i).Key("routes").Index(j).Key("max_visits")
			f, ok := byTarget[rt.Goto]
			switch {
			case !ok:
				byTarget[rt.Goto] = first{rt.MaxVisits, p}
			case f.n != rt.MaxVisits:
				r.errorf(p, "conflicting max_visits for stage %q: %d here, %d at %s", rt.Goto, rt.MaxVisits, f.n, f.path)
			}
		}
	}
}

// checkStageShape enforces the combinations the schema leaves open and reports
// whether the stage is well-formed enough for further checks.
func checkStageShape(r *reporter, p Path, st *Stage) bool {
	hasStep, hasParallel := st.Step != nil, len(st.Parallel) > 0
	switch {
	case hasStep == hasParallel:
		r.errorf(p, "stage must define exactly one of step or parallel")
		return false
	case st.Foreach != "" && hasParallel:
		r.errorf(p.Key("foreach"), "foreach runs one step per item; use step instead of parallel")
		return false
	}
	ok := true
	if st.MaxParallel > 0 && st.Foreach == "" {
		r.errorf(p.Key("max_parallel"), "max_parallel applies only to foreach stages")
		ok = false
	}
	if j := st.Join; j != nil {
		if st.Kind() == StageSingle {
			r.errorf(p.Key("join"), "join applies only to parallel and foreach stages")
			return false
		}
		switch {
		case j.Mode == JoinQuorum && j.Quorum == 0:
			r.errorf(p.Key("join"), "join.quorum is required when mode is %q", JoinQuorum)
			ok = false
		case j.Mode != JoinQuorum && j.Quorum != 0:
			r.errorf(p.Key("join").Key("quorum"), "quorum is used only when mode is %q", JoinQuorum)
			ok = false
		case hasParallel && j.Quorum > len(st.Parallel):
			r.errorf(p.Key("join").Key("quorum"), "quorum %d exceeds the number of parallel steps (%d)", j.Quorum, len(st.Parallel))
			ok = false
		}
	}
	return ok
}

// assignStepIDs fills omitted step ids: the stage name for single and foreach
// stages, the module name for parallel steps.
func assignStepIDs(st *Stage) {
	if st.Step != nil && st.Step.ID == "" {
		st.Step.ID = st.Name
	}
	for i := range st.Parallel {
		if st.Parallel[i].ID == "" {
			st.Parallel[i].ID = st.Parallel[i].Module
		}
	}
}

func checkStageExpressions(r *reporter, p Path, st *Stage) {
	checkExpr(r, p.Key("when"), st.When, expr.ScopeRun, expr.Bool)
	checkExpr(r, p.Key("foreach"), st.Foreach, expr.ScopeRun, expr.List)
}

func checkRoutes(r *reporter, stagePath Path, from int, routes []Route, stageIndex map[string]int) {
	for i, rt := range routes {
		p := stagePath.Key("routes").Index(i)
		checkExpr(r, p.Key("when"), rt.When, expr.ScopeRun, expr.Bool)
		to, ok := stageIndex[rt.Goto]
		if !ok {
			r.errorf(p.Key("goto"), "stage %q is not defined in this pipeline", rt.Goto)
			continue
		}
		if to <= from && rt.MaxVisits == 0 {
			r.errorf(p, "route back to stage %q forms a loop; set max_visits to bound it", rt.Goto)
		}
	}
}

func checkStep(r *reporter, p Path, s *Step, kind StageKind, defaults *StepPolicy) {
	scope := expr.ScopeRun
	if kind == StageForeach {
		scope = expr.ScopeItem
	}
	checkExpr(r, p.Key("when"), s.When, scope, expr.Bool)
	for name, src := range s.Input {
		checkExpr(r, p.Key("input").Key(name), src, scope, expr.Any)
	}
	if s.Timeout != 0 || s.Lease != 0 || s.Retry != nil { // defaults alone are checked once
		checkPolicy(r, p, s.Policy(defaults))
	}
	if s.Wait != nil && kind != StageSingle {
		r.errorf(p.Key("wait"), "wait is allowed only on a single-step stage, not in a %s stage", kind)
	}
	explicit := s.OnTimeout
	if explicit == "" && defaults != nil {
		explicit = defaults.OnTimeout
	}
	switch {
	case s.Wait != nil && explicit == "":
		r.warnf(p.Key("wait"), "on_timeout is not set, so an expired wait is retried; a review gate usually wants on_timeout: attention")
	case explicit == OnTimeoutRetry:
		if rt := s.Policy(defaults).Retry; rt != nil && rt.MaxAttempts == 1 {
			r.warnf(p.Key("on_timeout"), "on_timeout: retry with retry.max_attempts 1 never retries; the run goes to needs_attention")
		}
	}
}

// checkPolicy validates the effective reliability settings of a step or the defaults.
func checkPolicy(r *reporter, p Path, pol StepPolicy) {
	if pol.Timeout != 0 && pol.Lease != 0 && pol.Lease >= pol.Timeout {
		r.errorf(p.Key("lease"), "lease (%s) must be shorter than timeout (%s)", pol.Lease, pol.Timeout)
	}
	if rt := pol.Retry; rt != nil && rt.Initial != 0 && rt.Max != 0 && rt.Initial > rt.Max {
		r.errorf(p.Key("retry").Key("initial"), "initial delay (%s) must not exceed max (%s)", rt.Initial, rt.Max)
	}
}

func checkExpr(r *reporter, p Path, src string, scope expr.Scope, want expr.Result) {
	if src == "" {
		return
	}
	if err := expr.Check(src, scope, want); err != nil {
		r.errorf(p, "invalid expression: %v", err)
	}
}

func compileContextSchema(raw []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	const url = "mem://context.schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return err
	}
	if _, err := c.Compile(url); err != nil {
		// The library does not implement Unwrap on SchemaValidationError.
		var serr *jsonschema.SchemaValidationError
		if errors.As(err, &serr) {
			var verr *jsonschema.ValidationError
			if errors.As(serr.Err, &verr) {
				return errors.New(strings.Join(leafMessages(verr), "; "))
			}
		}
		return err
	}
	return nil
}

var (
	cronDescriptors = map[string]bool{"@yearly": true, "@annually": true, "@monthly": true, "@weekly": true, "@daily": true, "@midnight": true, "@hourly": true}
	cronField       = regexp.MustCompile(`^(\*|[0-9A-Za-z]+(-[0-9A-Za-z]+)?)(/[0-9]+)?(,(\*|[0-9A-Za-z]+(-[0-9A-Za-z]+)?)(/[0-9]+)?)*$`)
)

// checkCron checks the shape of a cron expression; value ranges are checked by the scheduler.
func checkCron(s string) error {
	if strings.HasPrefix(s, "@") {
		if !cronDescriptors[s] {
			return fmt.Errorf("unknown schedule descriptor %q", s)
		}
		return nil
	}
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have 5 fields (minute hour day month weekday), got %d", len(fields))
	}
	for _, f := range fields {
		if !cronField.MatchString(f) {
			return fmt.Errorf("invalid cron field %q", f)
		}
	}
	return nil
}
