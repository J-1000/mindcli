package sources

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScanConfig configures the file scanner.
type ScanConfig struct {
	Paths      []string
	Extensions []string
	Ignore     []string
}

// Scanner walks directories and returns matching files.
type Scanner struct {
	config ScanConfig
	extMap map[string]bool
}

// NewScanner creates a new file scanner.
func NewScanner(config ScanConfig) *Scanner {
	extMap := make(map[string]bool, len(config.Extensions))
	for _, ext := range config.Extensions {
		// Normalize extension to lowercase with leading dot
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		extMap[strings.ToLower(ext)] = true
	}

	return &Scanner{
		config: config,
		extMap: extMap,
	}
}

// Scan walks all configured paths and sends file info to the returned channel.
func (s *Scanner) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	files := make(chan FileInfo, 100)
	errs := make(chan error, 10)

	go func() {
		defer close(files)
		defer close(errs)

		for _, basePath := range s.config.Paths {
			// Expand home directory
			path := expandPath(basePath)

			info, err := os.Stat(path)
			if err != nil {
				if !os.IsNotExist(err) {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			if !info.IsDir() {
				// Single file
				if s.matchesExtension(path) {
					select {
					case files <- FileInfo{
						Path:       path,
						ModifiedAt: info.ModTime().Unix(),
						Size:       info.Size(),
					}:
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			// Walk directory
			err = filepath.WalkDir(path, func(filePath string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // Skip inaccessible files
				}

				// Check context cancellation
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				// Skip ignored directories
				if d.IsDir() {
					if s.shouldIgnore(filePath, d.Name()) {
						return filepath.SkipDir
					}
					return nil
				}

				// Check extension
				if !s.matchesExtension(filePath) {
					return nil
				}

				// Skip ignored files
				if s.shouldIgnore(filePath, d.Name()) {
					return nil
				}

				// Get file info
				info, err := d.Info()
				if err != nil {
					return nil // Skip files we can't stat
				}

				select {
				case files <- FileInfo{
					Path:       filePath,
					ModifiedAt: info.ModTime().Unix(),
					Size:       info.Size(),
				}:
				case <-ctx.Done():
					return ctx.Err()
				}

				return nil
			})

			if err != nil && err != context.Canceled {
				select {
				case errs <- err:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return files, errs
}

// MatchesPath reports whether a path is included by this scanner's config.
func (s *Scanner) MatchesPath(path string) bool {
	filePath := normalizePath(path)
	if filePath == "" {
		return false
	}

	if !s.matchesExtension(filePath) {
		return false
	}

	if s.shouldIgnore(filePath, filepath.Base(filePath)) {
		return false
	}

	for _, p := range s.config.Paths {
		if pathWithin(filePath, normalizePath(expandPath(p))) {
			return true
		}
	}

	return false
}

func (s *Scanner) matchesExtension(path string) bool {
	if len(s.extMap) == 0 {
		return true // No filter means all files
	}
	ext := strings.ToLower(filepath.Ext(path))
	return s.extMap[ext]
}

func (s *Scanner) shouldIgnore(path, name string) bool {
	for _, pattern := range s.config.Ignore {
		// Check exact name match
		if name == pattern {
			return true
		}
		// Check if pattern matches path component
		if strings.Contains(path, string(filepath.Separator)+pattern+string(filepath.Separator)) {
			return true
		}
		// Check glob pattern
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
}

func pathWithin(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+string(filepath.Separator))
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
