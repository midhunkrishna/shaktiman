package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// ScannedFile holds information about a discovered source file.
type ScannedFile struct {
	Path        string  // project-relative path
	AbsPath     string  // absolute path after symlink resolution
	ContentHash string  // SHA-256 hex of file contents
	Mtime       float64 // Unix timestamp
	Size        int64
	Language    string
}

// ScanInput configures a file scanning operation.
type ScanInput struct {
	ProjectRoot string
}

// ScanResult holds the output of a file scan.
type ScanResult struct {
	Files []ScannedFile
}

// skipDirs are directories that are always skipped during scanning.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".shaktiman":   true,
	"vendor":       true,
	"__pycache__":  true,
}

// languageExtensions maps file extensions to language names.
var languageExtensions = map[string]string{
	".ts":  "typescript",
	".tsx": "typescript",
	".py":  "python",
	".go":  "go",
}

// ScanRepo walks the project tree and returns all indexable source files.
func ScanRepo(ctx context.Context, input ScanInput) (*ScanResult, error) {
	root := input.ProjectRoot
	gi := loadIgnorePatterns(root)

	var files []ScannedFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		name := d.Name()

		// Skip hidden directories and known non-source dirs
		if d.IsDir() {
			if skipDirs[name] || (strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Check file extension
		ext := strings.ToLower(filepath.Ext(name))
		lang, supported := languageExtensions[ext]
		if !supported {
			return nil
		}

		// Get project-relative path
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// Check gitignore patterns
		if gi != nil && gi.MatchesPath(relPath) {
			return nil
		}

		// Resolve symlinks (DM-7)
		absPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil // skip unresolvable symlinks
		}

		// Ensure resolved path is still within project root
		absRoot, _ := filepath.EvalSymlinks(root)
		if !strings.HasPrefix(absPath, absRoot) {
			return nil // skip symlinks pointing outside project
		}

		// Read file for hash and size
		content, err := os.ReadFile(absPath)
		if err != nil {
			return nil // skip unreadable files
		}

		// Skip binary files (check first 512 bytes for null bytes)
		if isBinary(content) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(content))

		files = append(files, ScannedFile{
			Path:        relPath,
			AbsPath:     absPath,
			ContentHash: hash,
			Mtime:       float64(info.ModTime().UnixMilli()) / 1000.0,
			Size:        info.Size(),
			Language:    lang,
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("scan repo %s: %w", root, err)
	}

	return &ScanResult{Files: files}, nil
}

// loadIgnorePatterns loads .gitignore and .shaktimanignore patterns.
func loadIgnorePatterns(root string) *ignore.GitIgnore {
	var patterns []string

	for _, name := range []string{".gitignore", ".shaktimanignore"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				patterns = append(patterns, line)
			}
		}
	}

	if len(patterns) == 0 {
		return nil
	}

	return ignore.CompileIgnoreLines(patterns...)
}

// isBinary checks if content appears to be binary by looking for null bytes.
func isBinary(content []byte) bool {
	checkLen := 512
	if len(content) < checkLen {
		checkLen = len(content)
	}
	for i := 0; i < checkLen; i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}
