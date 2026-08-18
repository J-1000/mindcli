package sources

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/J-1000/mindcli/internal/storage"
)

var codeLanguagesByExtension = map[string]string{
	".c": "c", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".h": "c", ".hh": "cpp", ".hpp": "cpp",
	".cs": "csharp", ".css": "css", ".dart": "dart", ".ex": "elixir", ".exs": "elixir", ".fs": "fsharp",
	".go": "go", ".graphql": "graphql", ".gql": "graphql", ".hs": "haskell", ".html": "html", ".htm": "html",
	".java": "java", ".js": "javascript", ".jsx": "javascript", ".json": "json", ".kt": "kotlin", ".kts": "kotlin",
	".lua": "lua", ".m": "objective-c", ".mm": "objective-cpp", ".php": "php", ".pl": "perl", ".pm": "perl",
	".proto": "protobuf", ".py": "python", ".r": "r", ".rb": "ruby", ".rs": "rust", ".scala": "scala",
	".sh": "shell", ".bash": "shell", ".zsh": "shell", ".fish": "shell", ".sql": "sql", ".swift": "swift",
	".ts": "typescript", ".tsx": "typescript", ".vue": "vue", ".svelte": "svelte", ".xml": "xml", ".yaml": "yaml",
	".yml": "yaml", ".toml": "toml", ".tf": "terraform", ".tfvars": "terraform", ".zig": "zig",
}

var codeLanguagesByFilename = map[string]string{
	"dockerfile": "dockerfile", "containerfile": "dockerfile", "makefile": "makefile", "gnumakefile": "makefile",
	"justfile": "makefile", "rakefile": "ruby", "gemfile": "ruby", "podfile": "ruby", "build.gradle": "gradle",
	"settings.gradle": "gradle", "cmakelists.txt": "cmake",
}

// CodeSource indexes bounded, recognized UTF-8 source files from configured
// repository roots without following symbolic links.
type CodeSource struct {
	paths        []string
	ignore       []string
	maxFileBytes int64
	maxFiles     int
}

func NewCodeSource(paths, ignore []string, maxFileBytes int64, maxFiles int) *CodeSource {
	return &CodeSource{
		paths:        append([]string(nil), paths...),
		ignore:       append([]string(nil), ignore...),
		maxFileBytes: maxFileBytes,
		maxFiles:     maxFiles,
	}
}

func (c *CodeSource) Name() storage.Source { return storage.SourceCode }

func (c *CodeSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	files := make(chan FileInfo, 100)
	errs := make(chan error, len(c.paths)+4)
	go func() {
		defer close(files)
		defer close(errs)
		indexed := 0
		oversized := 0
		limitReached := false
		for _, configuredPath := range c.paths {
			if limitReached || indexed >= c.maxFiles {
				limitReached = true
				break
			}
			root := normalizePath(expandPath(configuredPath))
			info, err := os.Lstat(root)
			if err != nil {
				if !os.IsNotExist(err) {
					sendSourceError(ctx, errs, err)
				}
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				sendSourceError(ctx, errs, fmt.Errorf("code source skips symbolic-link root %s", root))
				continue
			}
			if !info.IsDir() {
				if languageForCodePath(root) == "" || info.Size() > c.maxFileBytes {
					continue
				}
				if !sendCodeFile(ctx, files, root, info) {
					return
				}
				indexed++
				continue
			}

			walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if entry.Type()&os.ModeSymlink != 0 {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.IsDir() {
					if path != root && c.shouldIgnore(root, path) {
						return filepath.SkipDir
					}
					return nil
				}
				if c.shouldIgnore(root, path) || languageForCodePath(path) == "" {
					return nil
				}
				info, err := entry.Info()
				if err != nil || !info.Mode().IsRegular() {
					return nil
				}
				if info.Size() > c.maxFileBytes {
					oversized++
					return nil
				}
				if indexed >= c.maxFiles {
					limitReached = true
					return filepath.SkipAll
				}
				if !sendCodeFile(ctx, files, path, info) {
					return ctx.Err()
				}
				indexed++
				return nil
			})
			if walkErr != nil && ctx.Err() == nil {
				sendSourceError(ctx, errs, walkErr)
			}
		}
		if oversized > 0 {
			sendSourceError(ctx, errs, fmt.Errorf("skipped %d code file(s) larger than %d bytes", oversized, c.maxFileBytes))
		}
		if limitReached {
			sendSourceError(ctx, errs, fmt.Errorf("code file limit %d reached; remaining files were skipped", c.maxFiles))
		}
	}()
	return files, errs
}

func sendCodeFile(ctx context.Context, files chan<- FileInfo, path string, info os.FileInfo) bool {
	select {
	case files <- FileInfo{Path: path, ModifiedAt: info.ModTime().Unix(), Size: info.Size()}:
		return true
	case <-ctx.Done():
		return false
	}
}

func sendSourceError(ctx context.Context, errs chan<- error, err error) {
	select {
	case errs <- err:
	case <-ctx.Done():
	}
}

func (c *CodeSource) MatchesPath(path string) bool {
	path = normalizePath(path)
	if languageForCodePath(path) == "" {
		return false
	}
	for _, configuredPath := range c.paths {
		root := normalizePath(expandPath(configuredPath))
		if pathWithin(path, root) && !c.shouldIgnore(root, path) {
			return true
		}
	}
	return false
}

func (c *CodeSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	language := languageForCodePath(file.Path)
	if language == "" {
		return nil, fmt.Errorf("unrecognized source-code format: %s", file.Path)
	}
	data, err := readFileBounded(file.Path, c.maxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("reading source code: %w", err)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("source code is not valid UTF-8 text")
	}
	root := c.repositoryRoot(file.Path)
	relativePath := filepath.Base(file.Path)
	repository := filepath.Base(filepath.Dir(file.Path))
	if root != "" {
		repository = filepath.Base(root)
		if relative, relErr := filepath.Rel(root, file.Path); relErr == nil {
			relativePath = filepath.ToSlash(relative)
		}
	}
	content := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	lineCount := 0
	if content != "" {
		lineCount = strings.Count(content, "\n") + 1
	}
	metadata := map[string]string{
		"format":            strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Path)), "."),
		"language":          language,
		"repository":        repository,
		"relative_path":     relativePath,
		"original_path":     file.Path,
		"location":          fmt.Sprintf("lines:1-%d", lineCount),
		"line_count":        strconv.Itoa(lineCount),
		"kind":              "source_code",
		"extraction_method": "source_text",
	}
	if metadata["format"] == "" {
		metadata["format"] = strings.ToLower(filepath.Base(file.Path))
	}
	return extractedDocument(storage.SourceCode, file, relativePath, content, metadata), nil
}

func (c *CodeSource) repositoryRoot(path string) string {
	path = normalizePath(path)
	var candidates []string
	for _, configuredPath := range c.paths {
		root := normalizePath(expandPath(configuredPath))
		info, err := os.Stat(root)
		if err == nil && !info.IsDir() {
			root = filepath.Dir(root)
		}
		if pathWithin(path, root) {
			candidates = append(candidates, root)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return len(candidates[i]) > len(candidates[j]) })
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func (c *CodeSource) shouldIgnore(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return false
	}
	relative = filepath.ToSlash(relative)
	segments := strings.Split(relative, "/")
	for _, pattern := range c.ignore {
		pattern = filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(pattern, "./")))
		if pattern == "" {
			continue
		}
		if strings.Contains(pattern, "/") {
			if matched, _ := filepath.Match(pattern, relative); matched || strings.HasPrefix(relative, strings.TrimSuffix(pattern, "/")+"/") {
				return true
			}
			continue
		}
		for _, segment := range segments {
			if strings.EqualFold(segment, pattern) {
				return true
			}
			if matched, _ := filepath.Match(pattern, segment); matched {
				return true
			}
		}
	}
	return false
}

func languageForCodePath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if language := codeLanguagesByFilename[base]; language != "" {
		return language
	}
	if strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") {
		return ""
	}
	return codeLanguagesByExtension[strings.ToLower(filepath.Ext(base))]
}
