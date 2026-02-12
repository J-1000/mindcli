package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
		case "search":
			if len(os.Args) < 3 {
				return fmt.Errorf("usage: mindcli search \"query\"")
			}
			return runSearch(strings.Join(os.Args[2:], " "))
		case "export":
			return runExport(os.Args[2:])
		case "tag":
			return runTag(os.Args[2:])
		case "collection":
			return runCollection(os.Args[2:])
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
  mindcli search "..." Search and print results
  mindcli export "..." Export search results (--format json|csv|markdown)
  mindcli tag ...      Manage document tags (add, remove, list)
  mindcli collection   Manage collections (create, delete, list, show, add, remove, rename)
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
  mindcli search "Go concurrency"               # Search without TUI
  mindcli export "Go" --format csv             # Export results as CSV
  mindcli export "Go" --output results.json    # Export to file
  mindcli ask "what did I write about Go?"     # Ask a question
  mindcli collection create "reading-list"   # Create a collection
  mindcli collection list                    # List all collections`)
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

func runSearch(queryStr string) error {
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

	parsed := query.ParseQuery(queryStr)
	searchQ := parsed.SearchTerms
	if parsed.SourceFilter != "" {
		searchQ = searchQ + " source:" + parsed.SourceFilter
	}

	ctx := context.Background()
	results, err := searchIndex.Search(ctx, searchQ, 20)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		doc, err := db.GetDocument(ctx, r.ID)
		if err != nil || doc == nil {
			continue
		}
		preview := doc.Preview
		if preview == "" && len(doc.Content) > 100 {
			preview = doc.Content[:100] + "..."
		} else if preview == "" {
			preview = doc.Content
		}
		fmt.Printf("%d. %s\n   %s [%s] (score: %.2f)\n   %s\n\n",
			i+1, doc.Title, doc.Path, doc.Source, r.Score, preview)
	}

	return nil
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	format := fs.String("format", "json", "Output format: json, csv, markdown")
	output := fs.String("output", "", "Output file (default: stdout)")
	limit := fs.Int("limit", 50, "Maximum number of results")
	fs.Parse(args)

	queryStr := strings.Join(fs.Args(), " ")
	if queryStr == "" {
		return fmt.Errorf("usage: mindcli export \"query\" [--format json|csv|markdown] [--output file] [--limit N]")
	}

	switch *format {
	case "json", "csv", "markdown":
	default:
		return fmt.Errorf("unsupported format %q: use json, csv, or markdown", *format)
	}

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

	parsed := query.ParseQuery(queryStr)
	searchQ := parsed.SearchTerms
	if parsed.SourceFilter != "" {
		searchQ = searchQ + " source:" + parsed.SourceFilter
	}

	ctx := context.Background()
	var results storage.SearchResults

	// Try hybrid search first.
	vectorPath := filepath.Join(dataDir, "vectors.graph")
	if _, statErr := os.Stat(vectorPath); statErr == nil {
		vectors, vErr := storage.NewVectorStore(vectorPath)
		if vErr == nil && vectors.Len() > 0 {
			defer vectors.Close()
			ollamaEmb := embeddings.NewOllamaEmbedder(cfg.Embeddings.OllamaURL, cfg.Embeddings.Model)
			cachePath := filepath.Join(dataDir, "embeddings.db")
			cached, cErr := embeddings.NewCachedEmbedder(ollamaEmb, cachePath)
			if cErr == nil {
				defer cached.Close()
				hybrid := query.NewHybridSearcher(searchIndex, vectors, cached, db, cfg.Search.HybridWeight)
				hybridResults, hErr := hybrid.Search(ctx, searchQ, *limit)
				if hErr == nil {
					results = hybridResults
				}
			}
		}
	}

	// Fallback to Bleve search.
	if len(results) == 0 {
		bleveResults, err := searchIndex.Search(ctx, searchQ, *limit)
		if err != nil {
			return fmt.Errorf("searching: %w", err)
		}
		for _, r := range bleveResults {
			doc, err := db.GetDocument(ctx, r.ID)
			if err == nil && doc != nil {
				results = append(results, &storage.SearchResult{
					Document:  doc,
					Score:     r.Score,
					BM25Score: r.Score,
				})
			}
		}
	}

	if len(results) == 0 {
		return fmt.Errorf("no results found for %q", queryStr)
	}

	// Determine output writer.
	var w io.Writer = os.Stdout
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			return fmt.Errorf("creating output file: %w", err)
		}
		defer f.Close()
		w = f
	}

	switch *format {
	case "json":
		return exportJSON(w, results)
	case "csv":
		return exportCSV(w, results)
	case "markdown":
		return exportMarkdown(w, results)
	}
	return nil
}

func runTag(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mindcli tag <add|remove|list> [args...]")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
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

	ctx := context.Background()

	switch args[0] {
	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: mindcli tag add <doc-path> <tag>")
		}
		doc, err := db.GetDocumentByPath(ctx, args[1])
		if err != nil {
			return fmt.Errorf("document not found: %s", args[1])
		}
		if err := db.AddTag(ctx, doc.ID, args[2]); err != nil {
			return fmt.Errorf("adding tag: %w", err)
		}
		fmt.Printf("Added tag %q to %s\n", args[2], doc.Title)

	case "remove":
		if len(args) < 3 {
			return fmt.Errorf("usage: mindcli tag remove <doc-path> <tag>")
		}
		doc, err := db.GetDocumentByPath(ctx, args[1])
		if err != nil {
			return fmt.Errorf("document not found: %s", args[1])
		}
		if err := db.RemoveTag(ctx, doc.ID, args[2]); err != nil {
			return fmt.Errorf("removing tag: %w", err)
		}
		fmt.Printf("Removed tag %q from %s\n", args[2], doc.Title)

	case "list":
		if len(args) >= 2 {
			// List tags for a specific document
			doc, err := db.GetDocumentByPath(ctx, args[1])
			if err != nil {
				return fmt.Errorf("document not found: %s", args[1])
			}
			tags, err := db.GetTags(ctx, doc.ID)
			if err != nil {
				return fmt.Errorf("getting tags: %w", err)
			}
			if len(tags) == 0 {
				fmt.Printf("No tags for %s\n", doc.Title)
			} else {
				fmt.Printf("Tags for %s:\n", doc.Title)
				for _, tag := range tags {
					fmt.Printf("  %s\n", tag)
				}
			}
		} else {
			// List all tags
			tags, err := db.ListAllTags(ctx)
			if err != nil {
				return fmt.Errorf("listing tags: %w", err)
			}
			if len(tags) == 0 {
				fmt.Println("No tags found.")
			} else {
				fmt.Println("All tags:")
				for _, tag := range tags {
					fmt.Printf("  %s\n", tag)
				}
			}
		}

	default:
		return fmt.Errorf("unknown tag subcommand %q: use add, remove, or list", args[0])
	}

	return nil
}

func runCollection(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mindcli collection <create|delete|list|show|add|remove|rename> [args...]")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
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

	ctx := context.Background()

	switch args[0] {
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: mindcli collection create <name> [--query \"...\"] [--description \"...\"]")
		}
		name := args[1]
		fs := flag.NewFlagSet("collection-create", flag.ExitOnError)
		queryStr := fs.String("query", "", "Saved search query")
		desc := fs.String("description", "", "Collection description")
		fs.Parse(args[2:])

		col := &storage.Collection{Name: name, Query: *queryStr, Description: *desc}
		if err := db.CreateCollection(ctx, col); err != nil {
			return fmt.Errorf("creating collection: %w", err)
		}
		fmt.Printf("Created collection %q\n", name)

	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: mindcli collection delete <name>")
		}
		if err := db.DeleteCollectionByName(ctx, args[1]); err != nil {
			return fmt.Errorf("deleting collection: %w", err)
		}
		fmt.Printf("Deleted collection %q\n", args[1])

	case "list":
		cols, err := db.ListCollections(ctx)
		if err != nil {
			return fmt.Errorf("listing collections: %w", err)
		}
		if len(cols) == 0 {
			fmt.Println("No collections found.")
		} else {
			for _, c := range cols {
				count, _ := db.CountCollectionDocuments(ctx, c.ID)
				desc := ""
				if c.Description != "" {
					desc = " - " + c.Description
				}
				fmt.Printf("  %s (%d docs)%s\n", c.Name, count, desc)
			}
		}

	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: mindcli collection show <name>")
		}
		col, err := db.GetCollectionByName(ctx, args[1])
		if err != nil {
			return fmt.Errorf("collection not found: %s", args[1])
		}
		count, _ := db.CountCollectionDocuments(ctx, col.ID)
		fmt.Printf("Collection: %s\n", col.Name)
		if col.Description != "" {
			fmt.Printf("Description: %s\n", col.Description)
		}
		if col.Query != "" {
			fmt.Printf("Query: %s\n", col.Query)
		}
		fmt.Printf("Documents: %d\n", count)
		fmt.Printf("Created: %s\n", col.CreatedAt.Format("2006-01-02 15:04:05"))

		docs, _ := db.GetCollectionDocuments(ctx, col.ID)
		for i, doc := range docs {
			fmt.Printf("  %d. %s (%s)\n", i+1, doc.Title, doc.Path)
		}

	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: mindcli collection add <collection-name> <doc-path>")
		}
		col, err := db.GetCollectionByName(ctx, args[1])
		if err != nil {
			return fmt.Errorf("collection not found: %s", args[1])
		}
		doc, err := db.GetDocumentByPath(ctx, args[2])
		if err != nil {
			return fmt.Errorf("document not found: %s", args[2])
		}
		if err := db.AddToCollection(ctx, col.ID, doc.ID); err != nil {
			return fmt.Errorf("adding to collection: %w", err)
		}
		fmt.Printf("Added %q to collection %q\n", doc.Title, col.Name)

	case "remove":
		if len(args) < 3 {
			return fmt.Errorf("usage: mindcli collection remove <collection-name> <doc-path>")
		}
		col, err := db.GetCollectionByName(ctx, args[1])
		if err != nil {
			return fmt.Errorf("collection not found: %s", args[1])
		}
		doc, err := db.GetDocumentByPath(ctx, args[2])
		if err != nil {
			return fmt.Errorf("document not found: %s", args[2])
		}
		if err := db.RemoveFromCollection(ctx, col.ID, doc.ID); err != nil {
			return fmt.Errorf("removing from collection: %w", err)
		}
		fmt.Printf("Removed %q from collection %q\n", doc.Title, col.Name)

	case "rename":
		if len(args) < 3 {
			return fmt.Errorf("usage: mindcli collection rename <old-name> <new-name>")
		}
		col, err := db.GetCollectionByName(ctx, args[1])
		if err != nil {
			return fmt.Errorf("collection not found: %s", args[1])
		}
		if err := db.RenameCollection(ctx, col.ID, args[2]); err != nil {
			return fmt.Errorf("renaming collection: %w", err)
		}
		fmt.Printf("Renamed collection %q to %q\n", args[1], args[2])

	default:
		return fmt.Errorf("unknown collection subcommand %q: use create, delete, list, show, add, remove, or rename", args[0])
	}

	return nil
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

	// Generate answer via Ollama with streaming.
	llm := query.NewLLMClient(cfg.Embeddings.OllamaURL, cfg.Embeddings.LLMModel)
	err = llm.GenerateAnswerStream(ctx, question, contexts, func(token string, done bool) {
		fmt.Print(token)
	})
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

	fmt.Printf("\n\nSources:\n")
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
