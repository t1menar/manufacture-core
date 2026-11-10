// Package schemas holds the Manufacture contract: JSON Schemas of the resources
// that users apply to the core. The files are the source of truth shared with the
// SDKs; Go code embeds them instead of duplicating the rules.
package schemas

import (
	"embed"
	"strings"
)

//go:embed VERSION v1/*.json
var files embed.FS

// FS returns the embedded schema files; paths are relative to this directory (for example "v1/module.schema.json").
func FS() embed.FS { return files }

// Version returns the semantic version of the contract (the VERSION file).
func Version() string {
	b, err := files.ReadFile("VERSION")
	if err != nil {
		panic("schemas: VERSION is not embedded: " + err.Error())
	}
	return strings.TrimSpace(string(b))
}

// BaseURL is the prefix of every schema $id.
const BaseURL = "https://manufacture.dev/schemas/"
