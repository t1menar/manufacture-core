// Package spec defines the resources of the Manufacture API (Module, Pipeline),
// decodes them from YAML or JSON and validates them.
//
// Validation has three layers, each reported as a Diagnostic with file, line and
// field path:
//
//  1. JSON Schema (package schemas): structure, types, enums, patterns. The same
//     schemas are vendored by the SDKs, so every client agrees on the shape.
//  2. Strict decoding into Go types: unknown fields are rejected, which also
//     guards the Go types from drifting away from the schemas.
//  3. Semantic rules that a schema cannot express well: exactly one of
//     step/parallel, route targets and loop limits, unique step ids, CEL
//     expressions, durations, references between resources.
package spec
