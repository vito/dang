package main

import "runtime/debug"

// versionInfo reports the module version and VCS revision stamped into the
// binary by the Go toolchain. Builds outside a released module (e.g. `go build`
// in a checkout) report "dev" plus the commit hash when available.
func versionInfo() (version, commit string) {
	version = "dev"
	commit = "unknown"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				commit += " (dirty)"
			}
		}
	}
	return
}
