package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/embeddings"
	"github.com/jankowtf/mindcli/internal/index"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
	"github.com/jankowtf/mindcli/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse command line
	indexCmd := flag.NewFlagSet("index", flag.ExitOnError)
	indexPaths := indexCmd.String("paths", "", "Comma-separated paths to index (overrides config)")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "index":
			indexCmd.Parse(os.Args[2:])
			return runIndex(*indexPaths)
		case "help", "-h", "--help":
			printUsage()
			return nil
		}
	}

	// Default: run TUI
	return runTUI()
}

func printUsage() {
	fmt.Println(`MindCLI - Personal Knowledge Search

Usage:
  mindcli              Start the TUI
  mindcli index        Index configured sources
  mindcli help         Show this help

Index options:
  -paths string        Comma-separated paths to index (overrides config)

Examples:
  mindcli                          # Start TUI
  mindcli index                    # Index all configured sources
  mindcli index -paths ~/notes     # Index specific paths`)
}

func runTUI() error {
	// Load configuration
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Ensure data directory exists
	dataDir, err := cfg.DataDir()
	if err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	// Open database
	dbPath, err := cfg.DatabasePath()
	if err != nil {
		return fmt.Errorf("getting database path: %w", err)
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Open search index
	indexPath := filepath.Join(dataDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		return fmt.Errorf("opening search index: %w", err)
	}
	defer searchIndex.Close()

	// Create and run the TUI
	model := tui.New(db, searchIndex)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}

func runIndex(pathsOverride string) error {
	// Load configuration
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Override paths if provided
	if pathsOverride != "" {
		cfg.Sources.Markdown.Paths = filepath.SplitList(pathsOverride)
	}

	// Ensure data directory exists
	dataDir, err := cfg.DataDir()
	if err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	// Open database
	dbPath, err := cfg.DatabasePath()
	if err != nil {
		return fmt.Errorf("getting database path: %w", err)
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Open search index
	indexPath := filepath.Join(dataDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		return fmt.Errorf("opening search index: %w", err)
	}
	defer searchIndex.Close()

	// Set up vector store and embedder (optional - fails gracefully)
	vectorPath := filepath.Join(dataDir, "vectors.graph")
	vectors, err := storage.NewVectorStore(vectorPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vector store unavailable: %v\n", err)
		vectors = nil
	}
	if vectors != nil {
		defer vectors.Close()
	}

	var embedder embeddings.Embedder
	if cfg.Embeddings.Provider == "ollama" {
		ollamaEmb := embeddings.NewOllamaEmbedder(cfg.Embeddings.OllamaURL, cfg.Embeddings.Model)
		cachePath := filepath.Join(dataDir, "embeddings.db")
		cached, err := embeddings.NewCachedEmbedder(ollamaEmb, cachePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: embedding cache unavailable: %v\n", err)
			embedder = ollamaEmb
		} else {
			defer cached.Close()
			embedder = cached
		}

		// Test connectivity by checking if Ollama is reachable.
		ctx := context.Background()
		if _, err := ollamaEmb.Embed(ctx, "test"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: Ollama not available, skipping embeddings: %v\n", err)
			embedder = nil
		}
	}

	// Create indexer
	indexer := index.NewIndexer(db, searchIndex, vectors, embedder, cfg)
	indexer.SetProgressReporter(&consoleProgressReporter{})

	// Run indexing
	ctx := context.Background()
	stats, err := indexer.IndexAll(ctx)
	if err != nil {
		return fmt.Errorf("indexing: %w", err)
	}

	// Save vector index to disk.
	if err := indexer.SaveVectors(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: saving vectors: %v\n", err)
	}

	fmt.Printf("\nIndexing complete:\n")
	fmt.Printf("  Total files:   %d\n", stats.TotalFiles)
	fmt.Printf("  Indexed:       %d\n", stats.IndexedFiles)
	fmt.Printf("  Errors:        %d\n", stats.Errors)
	if embedder != nil && vectors != nil {
		fmt.Printf("  Vectors:       %d\n", vectors.Len())
	}

	return nil
}

// consoleProgressReporter prints progress to the console.
type consoleProgressReporter struct {
	current int
	total   int
}

func (r *consoleProgressReporter) OnStart(source string, total int) {
	r.total = total
	fmt.Printf("Indexing %s: %d files\n", source, total)
}

func (r *consoleProgressReporter) OnProgress(source string, current, total int, path string) {
	r.current = current
	// Print progress every 10 files or at the end
	if current%10 == 0 || current == total {
		fmt.Printf("\r  [%d/%d] %s", current, total, truncatePath(path, 50))
	}
}

func (r *consoleProgressReporter) OnComplete(source string, indexed, errors int) {
	fmt.Printf("\r  Completed: %d indexed, %d errors\n", indexed, errors)
}

func (r *consoleProgressReporter) OnError(source string, path string, err error) {
	fmt.Fprintf(os.Stderr, "\n  Error: %s: %v\n", path, err)
}

func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path + " "
	}
	return "..." + path[len(path)-maxLen+3:] + " "
}
