// Package buildinfo exposes the version stamped at build time.
package buildinfo

import "runtime/debug"

// Set with -ldflags "-X github.com/t1menar/manufacture-core/internal/buildinfo.version=v0.1.0 ...".
var (
	version = "dev"
	commit  = ""
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version"`
}

// Get returns the build information, falling back to VCS data embedded by the Go toolchain.
func Get() Info {
	info := Info{Version: version, Commit: commit}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info.GoVersion = bi.GoVersion
		if info.Commit == "" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					info.Commit = s.Value
				}
			}
		}
	}
	return info
}
