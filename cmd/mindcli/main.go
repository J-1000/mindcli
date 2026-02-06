package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/embeddings"
	"github.com/jankowtf/mindcli/internal/index"
	"github.com/jankowtf/mindcli/internal/query"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
	"github.com/jankowtf/mindcli/internal/tui"
)

// Build-time variables set via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
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
	indexWatch := indexCmd.Bool("watch", false, "Watch for file changes after indexing")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "index":
			indexCmd.Parse(os.Args[2:])
			return runIndex(*indexPaths, *indexWatch)
		case "watch":
			return runWatch()
		case "ask":
			if len(os.Args) < 3 {
				return fmt.Errorf("usage: mindcli ask \"your question\"")
			}
			return runAsk(strings.Join(os.Args[2:], " "))
		case "config":
			return runConfigInit()
		case "version", "-v", "--version":
			fmt.Printf("mindcli %s (commit: %s, built: %s)\n", version, commit, date)
			return nil
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
  mindcli watch        Watch for file changes and re-index
  mindcli ask "..."    Ask a question (RAG answer via Ollama)
  mindcli config       Initialize config file
  mindcli version      Show version info
  mindcli help         Show this help

Index options:
  -paths string        Comma-separated paths to index (overrides config)
  -watch               Watch for file changes after indexing

Examples:
  mindcli                                      # Start TUI
  mindcli index                                # Index all configured sources
  mindcli index -paths ~/notes                 # Index specific paths
  mindcli index -watch                         # Index then watch for changes
  mindcli ask "what did I write about Go?"     # Ask a question`)
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func runTUI() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
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

	// Set up hybrid search (optional - degrades gracefully)
	var hybrid *query.HybridSearcher
	vectorPath := filepath.Join(dataDir, "vectors.graph")
	if _, err := os.Stat(vectorPath); err == nil {
		// Vector store exists, try to load it.
		vectors, err := storage.NewVectorStore(vectorPath)
		if err == nil && vectors.Len() > 0 {
			defer vectors.Close()

			ollamaEmb := embeddings.NewOllamaEmbedder(
				cfg.Embeddings.OllamaURL,
				cfg.Embeddings.Model,
			)
			cachePath := filepath.Join(dataDir, "embeddings.db")
			cached, err := embeddings.NewCachedEmbedder(ollamaEmb, cachePath)
			if err == nil {
				defer cached.Close()
				hybrid = query.NewHybridSearcher(searchIndex, vectors, cached, db, cfg.Search.HybridWeight)
			}
		}
	}

	// Set up LLM client (optional - for answer generation)
	var llm *query.LLMClient
	if cfg.Embeddings.Provider == "ollama" {
		llm = query.NewLLMClient(cfg.Embeddings.OllamaURL, cfg.Embeddings.LLMModel)
	}

	// Create and run the TUI
	model := tui.New(db, searchIndex, hybrid, llm)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}

func runIndex(pathsOverride string, watch bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
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

	// Start file watching if requested.
	if watch {
		return startWatching(indexer, cfg)
	}

	return nil
}

func runWatch() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	dataDir, err := cfg.DataDir()
	if err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	dbPath, err := cfg.DatabasePath()
	if err != nil {
		return err
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	indexPath := filepath.Join(dataDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		return err
	}
	defer searchIndex.Close()

	indexer := index.NewIndexer(db, searchIndex, nil, nil, cfg)
	return startWatching(indexer, cfg)
}

func startWatching(indexer *index.Indexer, cfg *config.Config) error {
	var paths []string
	if cfg.Sources.Markdown.Enabled {
		paths = append(paths, cfg.Sources.Markdown.Paths...)
	}
	if cfg.Sources.PDF.Enabled {
		paths = append(paths, cfg.Sources.PDF.Paths...)
	}

	if len(paths) == 0 {
		return fmt.Errorf("no paths to watch")
	}

	watcher, err := index.NewWatcher(indexer, paths)
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}

	fmt.Printf("Watching %d directories for changes (Ctrl+C to stop)...\n", len(paths))
	for _, p := range paths {
		fmt.Printf("  %s\n", p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopping watcher...")
		cancel()
	}()

	return watcher.Start(ctx)
}

func runAsk(question string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	dataDir, err := cfg.DataDir()
	if err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	dbPath, err := cfg.DatabasePath()
	if err != nil {
		return fmt.Errorf("getting database path: %w", err)
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	indexPath := filepath.Join(dataDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		return fmt.Errorf("opening search index: %w", err)
	}
	defer searchIndex.Close()

	// Parse the query for search terms and source filters.
	parsed := query.ParseQuery(question)
	searchQ := parsed.SearchTerms
	if parsed.SourceFilter != "" {
		searchQ = searchQ + " source:" + parsed.SourceFilter
	}

	// Set up hybrid search if available.
	ctx := context.Background()
	var docs []*storage.Document

	vectorPath := filepath.Join(dataDir, "vectors.graph")
	if _, err := os.Stat(vectorPath); err == nil {
		vectors, err := storage.NewVectorStore(vectorPath)
		if err == nil && vectors.Len() > 0 {
			defer vectors.Close()
			ollamaEmb := embeddings.NewOllamaEmbedder(cfg.Embeddings.OllamaURL, cfg.Embeddings.Model)
			cachePath := filepath.Join(dataDir, "embeddings.db")
			cached, err := embeddings.NewCachedEmbedder(ollamaEmb, cachePath)
			if err == nil {
				defer cached.Close()
				hybrid := query.NewHybridSearcher(searchIndex, vectors, cached, db, cfg.Search.HybridWeight)
				results, err := hybrid.Search(ctx, searchQ, 10)
				if err == nil {
					for _, r := range results {
						docs = append(docs, r.Document)
					}
				}
			}
		}
	}

	// Fallback to Bleve search if hybrid didn't produce results.
	if len(docs) == 0 {
		results, err := searchIndex.Search(ctx, searchQ, 10)
		if err != nil {
			return fmt.Errorf("searching: %w", err)
		}
		for _, r := range results {
			doc, err := db.GetDocument(ctx, r.ID)
			if err == nil && doc != nil {
				docs = append(docs, doc)
			}
		}
	}

	if len(docs) == 0 {
		fmt.Println("No relevant documents found.")
		return nil
	}

	// Build context from search results.
	contexts := make([]string, 0, 5)
	for i, doc := range docs {
		if i >= 5 {
			break
		}
		content := doc.Content
		if len(content) > 1000 {
			content = content[:1000]
		}
		contexts = append(contexts, content)
	}

	// Generate answer via Ollama.
	llm := query.NewLLMClient(cfg.Embeddings.OllamaURL, cfg.Embeddings.LLMModel)
	answer, err := llm.GenerateAnswer(ctx, question, contexts)
	if err != nil {
		// If LLM fails, show search results instead.
		fmt.Printf("(Ollama unavailable, showing top results for: %s)\n\n", parsed.SearchTerms)
		for i, doc := range docs {
			if i >= 5 {
				break
			}
			fmt.Printf("%d. %s\n   %s [%s]\n", i+1, doc.Title, doc.Path, doc.Source)
		}
		return nil
	}

	fmt.Println(answer)
	fmt.Printf("\nSources:\n")
	for i, doc := range docs {
		if i >= 5 {
			break
		}
		fmt.Printf("  %d. %s (%s)\n", i+1, doc.Title, doc.Path)
	}

	return nil
}

func runConfigInit() error {
	cfg := config.Default()
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	configPath, _ := config.ConfigPath()
	fmt.Printf("Config written to: %s\n", configPath)
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
