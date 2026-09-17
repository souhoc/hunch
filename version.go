package main

import "runtime/debug"

// version reports what this binary was built from. A binary installed with
// `go install ...@v1.2.3` carries the module version; one built from a
// checkout carries the commit in the VCS stamps instead. Neither needs
// -ldflags, so there is nothing to keep in step at release time.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return formatVersion(info)
}

func formatVersion(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if revision == "" {
		return "(devel)"
	}

	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified == "true" {
		return "(devel) " + revision + " (dirty)"
	}
	return "(devel) " + revision
}
