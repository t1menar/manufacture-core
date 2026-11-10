package mfctl

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// column renders one table column from an item.
type column struct {
	title string
	value func(m map[string]any) string
}

func str(path ...string) func(map[string]any) string {
	return func(m map[string]any) string {
		var v any = m
		for _, p := range path {
			mm, ok := v.(map[string]any)
			if !ok {
				return ""
			}
			v = mm[p]
		}
		switch x := v.(type) {
		case nil:
			return ""
		case string:
			return x
		case float64:
			return fmt.Sprintf("%g", x)
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	}
}

var moduleColumns = []column{
	{"NAME", str("name")},
	{"REVISION", str("revision")},
	{"IMAGE", str("image")},
	{"RUNTIME", str("runtime")},
	{"READY", func(m map[string]any) string {
		return str("replicas", "ready")(m) + "/" + str("replicas", "desired")(m)
	}},
	{"HEALTH", str("health")},
	{"QUEUE", str("queue_depth")},
}

var pipelineColumns = []column{
	{"NAME", str("name")},
	{"REVISION", str("revision")},
	{"STAGES", func(m map[string]any) string {
		ss, _ := m["stages"].([]any)
		names := make([]string, 0, len(ss))
		for _, s := range ss {
			if sm, ok := s.(map[string]any); ok {
				names = append(names, str("name")(sm))
			}
		}
		return strings.Join(names, " → ")
	}},
	{"ACTIVE", str("runs", "active")},
	{"ATTENTION", str("runs", "needs_attention")},
}

var runColumns = []column{
	{"ID", str("id")},
	{"PIPELINE", str("pipeline")},
	{"REVISION", str("revision")},
	{"STATUS", str("status")},
	{"STAGE", str("stage")},
	{"CREATED", str("created_at")},
}

var revisionColumns = []column{
	{"REVISION", str("revision")},
	{"CURRENT", func(m map[string]any) string {
		if b, _ := m["current"].(bool); b {
			return "*"
		}
		return ""
	}},
	{"APPLIED", str("applied_at")},
	{"BY", str("applied_by")},
	{"SOURCE", str("source")},
	{"HASH", func(m map[string]any) string {
		h := str("spec_hash")(m)
		if len(h) > 12 {
			h = h[:12]
		}
		return h
	}},
}

func render(w io.Writer, format string, raw json.RawMessage, cols []column) error {
	switch format {
	case "json":
		_, err := w.Write(raw)
		return err
	case "yaml":
		return printYAML(w, raw)
	case "table":
	default:
		return usageError{fmt.Sprintf("-o must be table, json or yaml, got %q", format)}
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = c.title
	}
	fmt.Fprintln(tw, strings.Join(titles, "\t"))
	for _, it := range list.Items {
		vals := make([]string, len(cols))
		for i, c := range cols {
			vals[i] = c.value(it)
		}
		fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	return tw.Flush()
}
