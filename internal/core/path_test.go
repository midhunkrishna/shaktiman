package core

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathContains_Basic(t *testing.T) {
	t.Parallel()

	root := filepath.Clean("/tmp/proj")
	cases := []struct {
		name string
		p    string
		want bool
	}{
		{"same", "/tmp/proj", true},
		{"child file", "/tmp/proj/main.go", true},
		{"nested dir", "/tmp/proj/sub/dir/x.go", true},
		{"sibling", "/tmp/proj-other", false},
		{"parent", "/tmp", false},
		{"unrelated", "/etc/passwd", false},
		{"trailing dot child", "/tmp/proj/.", true},
		// "..-escape" is intentionally a textual exit; filepath.Rel
		// resolves it before the prefix check.
		{"dot-dot escape", "/tmp/proj/../etc", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pathContains(root, filepath.Clean(c.p))
			if got != c.want {
				t.Errorf("pathContains(%q, %q) = %v, want %v", root, c.p, got, c.want)
			}
		})
	}
}

// TestPathContains_CaseFold verifies that on case-insensitive
// filesystems (darwin/windows) the comparison is case-folded — a hand
// crafted symlink resolving to "/Tmp/Proj" must still be considered
// contained by "/tmp/proj".
func TestPathContains_CaseFold(t *testing.T) {
	if caseSensitiveFS() {
		t.Skipf("skipping on case-sensitive FS (%s)", runtime.GOOS)
	}

	if !pathContains("/tmp/proj", "/Tmp/Proj/main.go") {
		t.Error("expected case-fold match to succeed")
	}
	if pathContains("/tmp/proj", "/tmp/proj-other/main.go") {
		t.Error("case-fold should not erase boundary")
	}
}
