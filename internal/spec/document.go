package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// MaxDocumentBytes limits the size of one input file.
const MaxDocumentBytes = 4 << 20

// Document is one YAML document of an input file, with positions kept for diagnostics.
type Document struct {
	Source string // file name or "-" for stdin
	root   *yaml.Node
	json   []byte // the document as JSON
	value  any    // the document as decoded by jsonschema.UnmarshalJSON (numbers are json.Number)
}

// Parse splits YAML (or JSON, which is valid YAML) into documents. Empty documents
// are skipped. A syntax error stops parsing of the file.
func Parse(source string, data []byte) ([]*Document, Diagnostics) {
	if len(data) > MaxDocumentBytes {
		return nil, Diagnostics{{Severity: SeverityError, Source: source,
			Message: fmt.Sprintf("file is larger than %d bytes", MaxDocumentBytes)}}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var (
		docs  []*Document
		diags Diagnostics
	)
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			diags = append(diags, Diagnostic{Severity: SeverityError, Source: source, Message: "invalid YAML: " + err.Error()})
			break
		}
		if len(n.Content) == 0 || isNull(n.Content[0]) {
			continue
		}
		doc := &Document{Source: source, root: n.Content[0]}
		if d := doc.decode(); d != nil {
			diags = append(diags, *d)
			continue
		}
		docs = append(docs, doc)
	}
	return docs, diags
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// decode converts the YAML tree into JSON, so that schema validation and typed
// decoding see exactly the JSON data model.
func (d *Document) decode() *Diagnostic {
	v, err := toJSONValue(d.root)
	if err != nil {
		diag := d.errorAt(nil, err.Error())
		return &diag
	}
	b, err := json.Marshal(v)
	if err != nil {
		diag := d.errorAt(nil, "cannot convert to JSON: "+err.Error())
		return &diag
	}
	d.json = b
	d.value, err = jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		diag := d.errorAt(nil, "cannot convert to JSON: "+err.Error())
		return &diag
	}
	return nil
}

// toJSONValue walks the node tree instead of using yaml's generic decoding, to
// reject what JSON cannot represent (non-string keys, NaN) with a position.
func toJSONValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return toJSONValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Tag == "!!merge" {
				return nil, fmt.Errorf("line %d: YAML merge keys (<<) are not supported", k.Line)
			}
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return nil, fmt.Errorf("line %d: object keys must be strings", k.Line)
			}
			val, err := toJSONValue(v)
			if err != nil {
				return nil, err
			}
			m[k.Value] = val
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			val, err := toJSONValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, val)
		}
		return s, nil
	case yaml.ScalarNode:
		return scalar(n)
	default:
		return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
	}
}

func scalar(n *yaml.Node) (any, error) {
	switch n.Tag {
	case "!!null":
		return nil, nil
	case "!!bool", "!!int":
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		return v, nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("line %d: %s is not a JSON number", n.Line, n.Value)
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	default: // !!str, !!timestamp, !!binary and custom tags stay text
		return n.Value, nil
	}
}

// header reads apiVersion, kind and metadata.name without trusting the rest of the document.
func (d *Document) header() (apiVersion string, kind Kind, name string) {
	m, _ := d.value.(map[string]any)
	apiVersion, _ = m["apiVersion"].(string)
	k, _ := m["kind"].(string)
	if meta, ok := m["metadata"].(map[string]any); ok {
		name, _ = meta["name"].(string)
	}
	return apiVersion, Kind(k), name
}

// position returns the line and column of the value at path. When the path does not
// exist (for example a missing property), the position of its deepest existing parent is used.
func (d *Document) position(p Path) (line, col int) {
	n := d.root
	line, col = n.Line, n.Column
	for _, seg := range p {
		for n.Kind == yaml.AliasNode {
			n = n.Alias
		}
		next, at := child(n, seg)
		if next == nil {
			break
		}
		n = next
		line, col = at.Line, at.Column
	}
	return line, col
}

// child returns the node under seg and the node whose position should be reported
// (the key for mapping entries, the item itself for sequences).
func child(n *yaml.Node, seg string) (value, at *yaml.Node) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				return n.Content[i+1], n.Content[i]
			}
		}
	case yaml.SequenceNode:
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(n.Content) {
			return n.Content[i], n.Content[i]
		}
	default: // scalars have no children; aliases are resolved by the caller
	}
	return nil, nil
}

func (d *Document) diagnostic(sev Severity, p Path, msg string) Diagnostic {
	line, col := d.position(p)
	_, kind, name := d.header()
	res := ""
	if kind != "" && name != "" {
		res = string(kind) + "/" + name
	}
	return Diagnostic{Severity: sev, Source: d.Source, Line: line, Column: col, Resource: res, Path: p.String(), Message: msg}
}

func (d *Document) errorAt(p Path, msg string) Diagnostic {
	if d.value == nil { // header is not available yet
		line, col := d.position(p)
		return Diagnostic{Severity: SeverityError, Source: d.Source, Line: line, Column: col, Path: p.String(), Message: msg}
	}
	return d.diagnostic(SeverityError, p, msg)
}
