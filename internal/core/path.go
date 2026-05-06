package core

import (
	"path/filepath"
	"runtime"
	"strings"
)

// pathContains reports whether absPath is the same as absRoot or lies
// inside it. Both inputs must already be absolute, symlink-resolved
// paths.
//
// Uses filepath.Rel + a `..` prefix check rather than
// strings.HasPrefix(absPath, absRoot+sep) because:
//
//   - HasPrefix doesn't normalize trailing separators or `.` segments,
//     so ("/a/b", "/a/b/") and ("/a/b/.", "/a/b") both fail.
//   - filepath.Rel returns a path relative to the root; if the result
//     starts with `..` (or is exactly `..`), absPath is outside the
//     root regardless of how it was spelled.
//
// On darwin and windows the comparison is case-insensitive because the
// underlying filesystems (APFS-default, NTFS-default) are case-folding
// — a hand-crafted "Foo" symlink that resolves outside the root would
// otherwise smuggle past a case-sensitive HasPrefix check.
func pathContains(absRoot, absPath string) bool {
	if !caseSensitiveFS() {
		absRoot = strings.ToLower(absRoot)
		absPath = strings.ToLower(absPath)
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// caseSensitiveFS reports whether the local filesystem is case
// sensitive in its path comparisons. Used to decide whether
// pathContains should case-fold.
func caseSensitiveFS() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return false
	default:
		return true
	}
}
