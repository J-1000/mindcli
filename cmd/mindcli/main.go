package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/embeddings"
	"github.com/jankowtf/mindcli/internal/index"
	"github.com/jankowtf/mindcli/internal/privacy"
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
	indexForce := indexCmd.Bool("force", false, "Re-index everything, ignoring unchanged-file checks")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "index":
			indexCmd.Parse(os.Args[2:])
			return runIndex(*indexPaths, *indexWatch, *indexForce)
		case "reindex":
			fs := flag.NewFlagSet("reindex", flag.ExitOnError)
			paths := fs.String("paths", "", "Comma-separated paths to index (overrides config)")
			fs.Parse(os.Args[2:])
			return runIndex(*paths, false, true)
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
		case "clipboard":
			return runClipboard(os.Args[2:])
		case "collection":
			return runCollection(os.Args[2:])
		case "ask":
			if len(os.Args) < 3 {
				return fmt.Errorf("usage: mindcli ask \"your question\"")
			}
			return runAsk(strings.Join(os.Args[2:], " "))
		case "clean":
			return runClean()
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
  mindcli reindex      Re-index everything (ignores unchanged-file checks)
  mindcli watch        Watch for file changes and re-index
  mindcli search "..." Search and print results
  mindcli export "..." Export search results (--format json|csv|markdown)
  mindcli ask "..."    Ask a question (RAG answer via Ollama)
  mindcli tag ...      Manage document tags (add, remove, list)
  mindcli clipboard    Manage clipboard index (clear, cleanup)
  mindcli collection   Manage collections (create, delete, list, show, add, remove, rename)
  mindcli clean        Remove documents whose files no longer exist
  mindcli config       Initialize config file
  mindcli version      Show version info
  mindcli help         Show this help

Index options:
  -paths string        Comma-separated paths to index (overrides config)
  -watch               Watch for file changes after indexing
  -force               Re-index everything, ignoring unchanged-file checks

Examples:
  mindcli                                      # Start TUI
  mindcli index                                # Index all configured sources
  mindcli index -paths ~/notes                 # Index specific paths
  mindcli index -watch                         # Index then watch for changes
  mindcli reindex                              # Full rebuild (e.g. after model change)
  mindcli search "Go concurrency"               # Search without TUI
  mindcli export "Go" --format csv             # Export results as CSV
  mindcli export "Go" --output results.json    # Export to file
  mindcli ask "what did I write about Go?"     # Ask a question
  mindcli clipboard clear                       # Remove all clipboard documents from index
  mindcli clipboard cleanup                     # Remove old clipboard documents by retention policy
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

func buildRedactor(cfg *config.Config) privacy.Redactor {
	redactor, errs := privacy.NewRedactor(cfg.Privacy.RedactPatterns)
	for _, err := range errs {
		log.Printf("Skipping redact pattern: %v", err)
	}
	return redactor
}

// openOpts selects which subsystems openStores wires up.
type openOpts struct {
	vectors  bool // open/create the vector store
	embedder bool // set up the embedder (cached)
	llm      bool // set up the LLM client
	hybrid   bool // build a hybrid searcher (needs vectors + embedder)
	indexing bool // indexing mode: create vectors even if empty; test embedder connectivity
}

// stores holds the open handles shared across commands. Always includes the
// config, data dir, database, and Bleve search index; optional members may be
// nil depending on openOpts and availability (semantic search degrades
// gracefully).
type stores struct {
	cfg      *config.Config
	dataDir  string
	db       *storage.DB
	bleve    *search.BleveIndex
	vectors  *storage.VectorStore
	embedder embeddings.Embedder
	cached   *embeddings.CachedEmbedder
	llm      *query.LLMClient
	hybrid   *query.HybridSearcher
}

// openStores opens the database and search index, then optionally wires up the
// vector store, embedder, LLM client, and hybrid searcher. The caller must call
// Close when done.
func openStores(opts openOpts) (*stores, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	dataDir, err := cfg.DataDir()
	if err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}
	dbPath, err := cfg.DatabasePath()
	if err != nil {
		return nil, fmt.Errorf("getting database path: %w", err)
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	s := &stores{cfg: cfg, dataDir: dataDir, db: db}

	indexPath := filepath.Join(dataDir, "search.bleve")
	bleve, err := search.NewBleveIndex(indexPath)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("opening search index: %w", err)
	}
	s.bleve = bleve

	if opts.vectors {
		s.openVectors(opts.indexing)
	}
	if opts.embedder {
		s.openEmbedder(opts.indexing)
	}
	if opts.llm {
		switch cfg.Embeddings.Provider {
		case "ollama":
			s.llm = query.NewLLMClient(cfg.Embeddings.OllamaURL, cfg.Embeddings.LLMModel)
		case "openai":
			s.llm = query.NewOpenAILLMClient(cfg.Embeddings.OpenAIKey, cfg.Embeddings.LLMModel)
		}
	}
	if opts.hybrid && s.vectors != nil && s.embedder != nil && s.vectors.Len() > 0 {
		s.hybrid = query.NewHybridSearcher(s.bleve, s.vectors, s.embedder, s.db, cfg.Search.HybridWeight)
	}

	return s, nil
}

// openVectors loads the vector store. In indexing mode it is always created
// (so embeddings can be added); otherwise it is only loaded when a non-empty
// graph already exists on disk.
func (s *stores) openVectors(indexing bool) {
	vectorPath := filepath.Join(s.dataDir, "vectors.graph")
	if indexing {
		vs, err := storage.NewVectorStore(vectorPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: vector store unavailable: %v\n", err)
			return
		}
		// Warn loudly if the configured model differs from the one that
		// produced the existing vectors: dimensions may not match and a full
		// reindex (mindcli index -force) is needed for consistent results.
		if prev := vs.Model(); prev != "" && prev != s.cfg.Embeddings.Model {
			fmt.Fprintf(os.Stderr,
				"warning: embedding model changed (%s -> %s); run 'mindcli index -force' to rebuild the vector index\n",
				prev, s.cfg.Embeddings.Model)
		}
		vs.SetModel(s.cfg.Embeddings.Model)
		s.vectors = vs
		return
	}
	if _, err := os.Stat(vectorPath); err != nil {
		return
	}
	vs, err := storage.NewVectorStore(vectorPath)
	if err != nil {
		return
	}
	if vs.Len() == 0 {
		vs.Close()
		return
	}
	s.vectors = vs
}

// openEmbedder sets up the embedder for the configured provider. In indexing
// mode it tests connectivity and disables embeddings if the backend is down.
func (s *stores) openEmbedder(indexing bool) {
	var base embeddings.Embedder
	switch s.cfg.Embeddings.Provider {
	case "ollama":
		base = embeddings.NewOllamaEmbedder(s.cfg.Embeddings.OllamaURL, s.cfg.Embeddings.Model)
	case "openai":
		base = embeddings.NewOpenAIEmbedder(s.cfg.Embeddings.OpenAIKey, s.cfg.Embeddings.Model)
	default:
		return
	}

	cachePath := filepath.Join(s.dataDir, "embeddings.db")
	if cached, err := embeddings.NewCachedEmbedder(base, cachePath, s.cfg.Embeddings.Model); err != nil {
		fmt.Fprintf(os.Stderr, "warning: embedding cache unavailable: %v\n", err)
		s.embedder = base
	} else {
		s.cached = cached
		s.embedder = cached
	}

	if indexing {
		// Probe the backend so a misconfigured provider degrades to BM25-only
		// rather than failing every document.
		if _, err := base.Embed(context.Background(), "test"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: embeddings unavailable (%s), skipping: %v\n", s.cfg.Embeddings.Provider, err)
			s.embedder = nil
		}
	}
}

// Close releases all open handles.
func (s *stores) Close() {
	if s == nil {
		return
	}
	if s.cached != nil {
		s.cached.Close()
	}
	if s.vectors != nil {
		s.vectors.Close()
	}
	if s.bleve != nil {
		s.bleve.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
}

// searchResults runs a parsed query through the hybrid searcher when available,
// falling back to Bleve-only. It is the single search entry point shared by the
// search, export, and ask commands.
func searchResults(ctx context.Context, s *stores, parsed query.ParsedQuery, limit int) (storage.SearchResults, error) {
	searchQ := parsed.SearchTerms
	if parsed.SourceFilter != "" {
		searchQ = searchQ + " source:" + parsed.SourceFilter
	}

	var results storage.SearchResults
	if s.hybrid != nil {
		r, err := s.hybrid.Search(ctx, searchQ, limit)
		if err != nil {
			return nil, err
		}
		results = r
	} else {
		bleveResults, err := s.bleve.Search(ctx, searchQ, limit)
		if err != nil {
			return nil, err
		}
		for _, r := range bleveResults {
			doc, err := s.db.GetDocument(ctx, r.ID)
			if err == nil && doc != nil {
				results = append(results, &storage.SearchResult{
					Document:  doc,
					Score:     r.Score,
					BM25Score: r.Score,
				})
			}
		}
	}

	return query.FilterByTime(results, parsed, time.Now()), nil
}

func runTUI() error {
	s, err := openStores(openOpts{vectors: true, embedder: true, llm: true, hybrid: true})
	if err != nil {
		return err
	}
	defer s.Close()

	redactor := buildRedactor(s.cfg)
	model := tui.New(s.db, s.bleve, s.hybrid, s.llm, redactor)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}

func runIndex(pathsOverride string, watch, force bool) error {
	s, err := openStores(openOpts{vectors: true, embedder: true, indexing: true})
	if err != nil {
		return err
	}
	defer s.Close()

	// Override paths if provided.
	if pathsOverride != "" {
		s.cfg.Sources.Markdown.Paths = parsePathsOverride(pathsOverride)
	}

	indexer := index.NewIndexer(s.db, s.bleve, s.vectors, s.embedder, s.cfg)
	indexer.SetForce(force)
	indexer.SetProgressReporter(&consoleProgressReporter{})

	ctx := context.Background()
	stats, err := indexer.IndexAll(ctx)
	if err != nil {
		return fmt.Errorf("indexing: %w", err)
	}

	if err := indexer.SaveVectors(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: saving vectors: %v\n", err)
	}

	fmt.Printf("\nIndexing complete:\n")
	fmt.Printf("  Total files:   %d\n", stats.TotalFiles)
	fmt.Printf("  Indexed:       %d\n", stats.IndexedFiles)
	fmt.Printf("  Errors:        %d\n", stats.Errors)
	if s.embedder != nil && s.vectors != nil {
		fmt.Printf("  Vectors:       %d\n", s.vectors.Len())
	}

	if watch {
		return startWatching(indexer, s.cfg)
	}

	return nil
}

func parsePathsOverride(pathsOverride string) []string {
	var paths []string
	for _, part := range strings.Split(pathsOverride, ",") {
		for _, p := range filepath.SplitList(strings.TrimSpace(part)) {
			p = strings.TrimSpace(p)
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

func runWatch() error {
	s, err := openStores(openOpts{vectors: true, embedder: true, indexing: true})
	if err != nil {
		return err
	}
	defer s.Close()

	indexer := index.NewIndexer(s.db, s.bleve, s.vectors, s.embedder, s.cfg)
	return startWatching(indexer, s.cfg)
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
	s, err := openStores(openOpts{vectors: true, embedder: true, hybrid: true})
	if err != nil {
		return err
	}
	defer s.Close()

	parsed := query.ParseQuery(queryStr)
	ctx := context.Background()
	results, err := searchResults(ctx, s, parsed, s.cfg.Search.ResultsLimit)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	redactor := buildRedactor(s.cfg)
	for i, r := range results {
		doc := r.Document
		preview := doc.Preview
		if preview == "" && len(doc.Content) > 100 {
			preview = doc.Content[:100] + "..."
		} else if preview == "" {
			preview = doc.Content
		}
		preview = redactor.Redact(preview)
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

	s, err := openStores(openOpts{vectors: true, embedder: true, hybrid: true})
	if err != nil {
		return err
	}
	defer s.Close()

	parsed := query.ParseQuery(queryStr)
	ctx := context.Background()
	results, err := searchResults(ctx, s, parsed, *limit)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}
	if len(results) == 0 {
		return fmt.Errorf("no results found for %q", queryStr)
	}

	redactor := buildRedactor(s.cfg)

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
		return exportJSON(w, results, redactor)
	case "csv":
		return exportCSV(w, results, redactor)
	case "markdown":
		return exportMarkdown(w, results, redactor)
	}
	return nil
}

func runTag(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mindcli tag <add|remove|list> [args...]")
	}

	s, err := openStores(openOpts{})
	if err != nil {
		return err
	}
	defer s.Close()
	db := s.db
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

	s, err := openStores(openOpts{})
	if err != nil {
		return err
	}
	defer s.Close()
	db := s.db
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

func runClipboard(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mindcli clipboard <clear|cleanup>")
	}

	s, err := openStores(openOpts{vectors: true})
	if err != nil {
		return err
	}
	defer s.Close()

	ctx := context.Background()
	docs, err := s.db.ListDocuments(ctx, storage.SourceClipboard)
	if err != nil {
		return fmt.Errorf("listing clipboard documents: %w", err)
	}

	switch args[0] {
	case "clear":
		removed, err := purgeClipboardDocuments(ctx, s.db, s.bleve, s.vectors, docs, func(*storage.Document) bool { return true })
		if err != nil {
			return err
		}
		fmt.Printf("Removed %d clipboard documents.\n", removed)
		return nil

	case "cleanup":
		cutoff := time.Now().AddDate(0, 0, -s.cfg.Sources.Clipboard.RetentionDays)
		removed, err := purgeClipboardDocuments(ctx, s.db, s.bleve, s.vectors, docs, func(doc *storage.Document) bool {
			return doc.ModifiedAt.Before(cutoff)
		})
		if err != nil {
			return err
		}
		fmt.Printf("Removed %d clipboard documents older than %s.\n", removed, cutoff.Format("2006-01-02"))
		return nil

	default:
		return fmt.Errorf("unknown clipboard subcommand %q: use clear or cleanup", args[0])
	}
}

func purgeClipboardDocuments(
	ctx context.Context,
	db *storage.DB,
	searchIndex *search.BleveIndex,
	vectors *storage.VectorStore,
	docs []*storage.Document,
	shouldDelete func(*storage.Document) bool,
) (int, error) {
	removed := 0
	for _, doc := range docs {
		if !shouldDelete(doc) {
			continue
		}

		chunks, err := db.GetChunksByDocument(ctx, doc.ID)
		if err == nil && vectors != nil {
			for _, chunk := range chunks {
				vectors.Delete(chunk.ID)
			}
		}
		_ = db.DeleteChunksByDocument(ctx, doc.ID)

		if err := searchIndex.Delete(ctx, doc.ID); err != nil {
			return removed, fmt.Errorf("removing %q from search index: %w", doc.ID, err)
		}
		if err := db.DeleteDocument(ctx, doc.ID); err != nil {
			return removed, fmt.Errorf("removing %q from database: %w", doc.ID, err)
		}
		removed++
	}
	return removed, nil
}

func runAsk(question string) error {
	s, err := openStores(openOpts{vectors: true, embedder: true, llm: true, hybrid: true})
	if err != nil {
		return err
	}
	defer s.Close()

	parsed := query.ParseQuery(question)
	ctx := context.Background()
	results, err := searchResults(ctx, s, parsed, 10)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}

	docs := make([]*storage.Document, 0, len(results))
	for _, r := range results {
		docs = append(docs, r.Document)
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
	conf := query.EstimateAnswerConfidence(question, contexts)

	if s.llm == nil {
		fmt.Printf("(LLM unavailable, showing top results for: %s)\n\n", parsed.SearchTerms)
		printAskSources(docs)
		return nil
	}

	// Generate answer via the LLM with streaming.
	redactor := buildRedactor(s.cfg)
	var answerBuilder strings.Builder
	err = s.llm.GenerateAnswerStream(ctx, question, contexts, func(token string, done bool) {
		if redactor.Enabled() {
			if done {
				fmt.Print(redactor.Redact(answerBuilder.String()))
				return
			}
			answerBuilder.WriteString(token)
			return
		}
		fmt.Print(token)
	})
	if err != nil {
		// If the LLM fails, show search results instead.
		fmt.Printf("(LLM unavailable, showing top results for: %s)\n\n", parsed.SearchTerms)
		printAskSources(docs)
		return nil
	}

	fmt.Printf("\nConfidence: %s (%.2f)\n", strings.ToUpper(conf.Level), conf.Score)
	fmt.Printf("\n\nSources:\n")
	printAskSources(docs)

	return nil
}

func printAskSources(docs []*storage.Document) {
	for i, doc := range docs {
		if i >= 5 {
			break
		}
		fmt.Printf("  %d. %s (%s)\n", i+1, doc.Title, doc.Path)
	}
}

func runClean() error {
	s, err := openStores(openOpts{vectors: true})
	if err != nil {
		return err
	}
	defer s.Close()

	indexer := index.NewIndexer(s.db, s.bleve, s.vectors, s.embedder, s.cfg)
	removed, err := indexer.Prune(context.Background())
	if err != nil {
		return fmt.Errorf("pruning: %w", err)
	}
	if err := indexer.SaveVectors(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: saving vectors: %v\n", err)
	}
	fmt.Printf("Removed %d documents whose files no longer exist.\n", removed)
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
