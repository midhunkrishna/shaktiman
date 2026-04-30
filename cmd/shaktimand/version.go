package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// binaryVersion is set via -ldflags "-X main.binaryVersion=..." at release
// build time. Falls back to "dev" for ad-hoc builds.
var binaryVersion = "dev"

// vcsRevision is the git commit short hash captured at build time when
// available via runtime/debug.BuildInfo (Go 1.18+ injects it
// automatically for module builds inside a git repo).
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return ""
}

// vcsModified reports whether the working tree had uncommitted changes
// at build time. Useful in the banner: a dirty build is a flag during
// post-mortem investigation.
func vcsModified() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.modified" {
			return s.Value == "true"
		}
	}
	return false
}

func versionLine() string {
	rev := vcsRevision()
	suffix := ""
	if vcsModified() {
		suffix = "-dirty"
	}
	if rev == "" {
		return fmt.Sprintf("shaktimand %s (%s/%s)", binaryVersion, runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("shaktimand %s+%s%s (%s/%s)",
		binaryVersion, rev, suffix, runtime.GOOS, runtime.GOARCH)
}
