package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(mainVersion string, settings map[string]string) *debug.BuildInfo {
	info := &debug.BuildInfo{}
	info.Main.Version = mainVersion
	for k, v := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return info
}

func TestFormatVersion(t *testing.T) {
	const sha = "4589c86abcdef0123456789"

	for name, tc := range map[string]struct {
		info *debug.BuildInfo
		want string
	}{
		// An installed binary knows its tag; the commit stamps are redundant.
		"tagged release": {
			buildInfo("v1.2.3", map[string]string{"vcs.revision": sha}),
			"v1.2.3",
		},
		"local build": {
			buildInfo("(devel)", map[string]string{"vcs.revision": sha, "vcs.modified": "false"}),
			"(devel) 4589c86abcde",
		},
		// A revision shorter than the truncation length must survive intact.
		"short revision": {
			buildInfo("(devel)", map[string]string{"vcs.revision": "4589c86"}),
			"(devel) 4589c86",
		},
		"local build with uncommitted changes": {
			buildInfo("(devel)", map[string]string{"vcs.revision": sha, "vcs.modified": "true"}),
			"(devel) 4589c86abcde (dirty)",
		},
		// `go test` builds carry no VCS stamps at all.
		"no stamps": {
			buildInfo("(devel)", nil),
			"(devel)",
		},
		"empty main version": {
			buildInfo("", nil),
			"(devel)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := formatVersion(tc.info); got != tc.want {
				t.Errorf("formatVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Whatever the build, version() says something a human can paste into a bug report.
func TestVersionIsNeverEmpty(t *testing.T) {
	got := version()
	if strings.TrimSpace(got) == "" {
		t.Error("version() is empty")
	}
}
