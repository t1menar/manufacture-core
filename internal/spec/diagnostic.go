package spec

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Severity of a diagnostic.
type Severity string

// Severities.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Path addresses a value inside a resource, for example spec.stages[1].routes[0].goto.
// Array indexes are stored as decimal strings, as in JSON Pointer.
type Path []string

// Key returns the path extended with an object key. The receiver is not modified.
func (p Path) Key(k string) Path { return append(slices.Clip(p), k) }

// Index returns the path extended with an array index. The receiver is not modified.
func (p Path) Index(i int) Path { return append(slices.Clip(p), strconv.Itoa(i)) }

// String renders the path in dotted form with [i] for array items.
func (p Path) String() string {
	var b strings.Builder
	for _, seg := range p {
		if isIndex(seg) {
			b.WriteString("[" + seg + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}

func isIndex(seg string) bool {
	if seg == "" {
		return false
	}
	for _, r := range seg {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Diagnostic is one finding about the input.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Source   string   `json:"source"`
	Line     int      `json:"line,omitempty"`
	Column   int      `json:"column,omitempty"`
	Resource string   `json:"resource,omitempty"` // "Kind/name" when known
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
}

// String formats the diagnostic as "source:line:col: severity: [resource: ][path: ]message".
func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(d.Source)
	if d.Line > 0 {
		fmt.Fprintf(&b, ":%d:%d", d.Line, d.Column)
	}
	fmt.Fprintf(&b, ": %s: ", d.Severity)
	if d.Resource != "" {
		b.WriteString(d.Resource + ": ")
	}
	if d.Path != "" {
		b.WriteString(d.Path + ": ")
	}
	b.WriteString(d.Message)
	return b.String()
}

// Diagnostics is an ordered list of findings.
type Diagnostics []Diagnostic

// Errors counts error diagnostics.
func (ds Diagnostics) Errors() int { return ds.count(SeverityError) }

// Warnings counts warning diagnostics.
func (ds Diagnostics) Warnings() int { return ds.count(SeverityWarning) }

func (ds Diagnostics) count(s Severity) int {
	n := 0
	for _, d := range ds {
		if d.Severity == s {
			n++
		}
	}
	return n
}

// Sort orders diagnostics by source and position, keeping the order of equal positions.
func (ds Diagnostics) Sort() {
	slices.SortStableFunc(ds, func(a, b Diagnostic) int {
		if c := strings.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
}
