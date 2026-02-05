package sources

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanner_Scan(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "scanner-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test structure
	files := map[string]string{
		"note1.md":             "# Note 1",
		"note2.txt":            "Note 2 content",
		"ignore-me.log":        "log content",
		"subdir/note3.md":      "# Note 3",
		"subdir/deep/note4.md": "# Note 4",
		".git/config":          "git config",
		"node_modules/pkg.md":  "should ignore",
	}

	for path, content := range files {
		fullPath := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("creating dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("writing file: %v", err)
		}
	}

	tests := []struct {
		name       string
		config     ScanConfig
		wantCount  int
		wantPaths  []string
		dontWant   []string
	}{
		{
			name: "markdown only",
			config: ScanConfig{
				Paths:      []string{tmpDir},
				Extensions: []string{".md"},
				Ignore:     []string{".git", "node_modules"},
			},
			wantCount: 3,
			wantPaths: []string{"note1.md", "note3.md", "note4.md"},
			dontWant:  []string{"node_modules", ".git"},
		},
		{
			name: "multiple extensions",
			config: ScanConfig{
				Paths:      []string{tmpDir},
				Extensions: []string{".md", ".txt"},
				Ignore:     []string{".git", "node_modules"},
			},
			wantCount: 4,
			wantPaths: []string{"note1.md", "note2.txt"},
		},
		{
			name: "no extension filter",
			config: ScanConfig{
				Paths:  []string{tmpDir},
				Ignore: []string{".git", "node_modules"},
			},
			wantCount: 5, // All files: note1.md, note2.txt, ignore-me.log, note3.md, note4.md
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := NewScanner(tt.config)
			ctx := context.Background()

			filesChan, errsChan := scanner.Scan(ctx)

			var found []FileInfo
			for f := range filesChan {
				found = append(found, f)
			}

			// Check errors
			for err := range errsChan {
				t.Errorf("scan error: %v", err)
			}

			if len(found) != tt.wantCount {
				t.Errorf("got %d files, want %d", len(found), tt.wantCount)
				for _, f := range found {
					t.Logf("  found: %s", f.Path)
				}
			}

			// Check wanted paths are found
			for _, want := range tt.wantPaths {
				foundIt := false
				for _, f := range found {
					if filepath.Base(f.Path) == want ||
						filepath.Base(filepath.Dir(f.Path))+"/"+filepath.Base(f.Path) == want {
						foundIt = true
						break
					}
				}
				if !foundIt {
					t.Errorf("expected to find %s", want)
				}
			}

			// Check unwanted paths are not found
			for _, dontWant := range tt.dontWant {
				for _, f := range found {
					if filepath.Base(filepath.Dir(f.Path)) == dontWant {
						t.Errorf("should not find files in %s, but found %s", dontWant, f.Path)
					}
				}
			}
		})
	}
}

func TestScanner_Cancellation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scanner-cancel-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create many files
	for i := 0; i < 100; i++ {
		path := filepath.Join(tmpDir, "note"+string(rune('a'+i%26))+".md")
		os.WriteFile(path, []byte("content"), 0644)
	}

	scanner := NewScanner(ScanConfig{
		Paths:      []string{tmpDir},
		Extensions: []string{".md"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	filesChan, _ := scanner.Scan(ctx)

	// Cancel after first file
	count := 0
	for range filesChan {
		count++
		if count == 1 {
			cancel()
		}
	}

	// Should not have processed all files
	if count >= 100 {
		t.Errorf("cancellation did not stop scan, got %d files", count)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/notes", filepath.Join(home, "notes")},
		{"~/", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := expandPath(tt.input)
		if got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
