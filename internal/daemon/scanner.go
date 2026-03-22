package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
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
	".rs":  "rust",
}

// LanguageForExt returns the language for a file extension (e.g. ".go" → "go").
func LanguageForExt(ext string) (string, bool) {
	lang, ok := languageExtensions[ext]
	return lang, ok
}

// ScanRepo walks the project tree and returns all indexable source files.
func ScanRepo(ctx context.Context, input ScanInput) (*ScanResult, error) {
	logger := slog.Default().With("component", "scanner")
	root := input.ProjectRoot
	gi := loadIgnorePatterns(root)

	// Resolve root to absolute path once for symlink boundary checks
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root symlinks: %w", err)
	}

	var files []ScannedFile
	var skipped int

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skipped++
			logger.Debug("scan skip: walk error", "path", path, "err", err)
			return nil
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
			skipped++
			logger.Debug("scan skip: rel path failed", "path", path, "err", err)
			return nil
		}

		// Check gitignore patterns
		if gi != nil && gi.MatchesPath(relPath) {
			skipped++
			logger.Debug("scan skip: ignored", "path", relPath)
			return nil
		}

		// Resolve symlinks (DM-7)
		absPath, err := filepath.Abs(path)
		if err != nil {
			skipped++
			logger.Debug("scan skip: abs path failed", "path", path, "err", err)
			return nil
		}
		absPath, err = filepath.EvalSymlinks(absPath)
		if err != nil {
			skipped++
			logger.Debug("scan skip: unresolvable symlink", "path", path, "err", err)
			return nil
		}

		// Ensure resolved path is still within project root
		if !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) && absPath != absRoot {
			skipped++
			logger.Debug("scan skip: symlink outside root", "path", path, "target", absPath)
			return nil
		}

		// Read file for hash and size
		content, err := os.ReadFile(absPath)
		if err != nil {
			skipped++
			logger.Debug("scan skip: unreadable", "path", absPath, "err", err)
			return nil
		}

		// Skip binary files (check first 512 bytes for null bytes)
		if isBinary(content) {
			skipped++
			logger.Debug("scan skip: binary file", "path", relPath)
			return nil
		}

		info, err := d.Info()
		if err != nil {
			skipped++
			logger.Debug("scan skip: stat failed", "path", path, "err", err)
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

	logger.Info("scan complete",
		"root", root,
		"files_found", len(files),
		"files_skipped", skipped)

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
