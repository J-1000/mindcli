package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
	_ "github.com/mattn/go-sqlite3"
)

// BrowserSource indexes browser history and bookmarks.
type BrowserSource struct {
	browsers []string
	options  BrowserOptions
	client   *http.Client
}

// NewBrowserSource creates a new browser history source.
func NewBrowserSource(browsers []string) *BrowserSource {
	if len(browsers) == 0 {
		browsers = []string{"chrome", "firefox", "safari"}
	}
	source := &BrowserSource{browsers: browsers}
	source.SetOptions(DefaultBrowserOptions())
	return source
}

// Name returns the source name.
func (b *BrowserSource) Name() storage.Source {
	return storage.SourceBrowser
}

// MatchesPath reports whether this source is configured to handle the path.
func (b *BrowserSource) MatchesPath(path string) bool {
	target := normalizePath(path)
	for _, browser := range b.browsers {
		if normalizePath(browserDBPath(browser)) == target || normalizePath(browserBookmarkPath(browser)) == target {
			return true
		}
	}
	return false
}

// historyEntry holds a single browser history entry.
type historyEntry struct {
	URL        string
	Title      string
	VisitCount int
	LastVisit  time.Time
	AddedAt    time.Time
	Browser    string
	Kind       string // history or bookmark
}

// Scan finds browser profile data and returns one artifact per browser profile.
// Chrome history and bookmarks are coalesced so a URL that appears in both is
// emitted as one document.
func (b *BrowserSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	files := make(chan FileInfo, 10)
	errs := make(chan error, 10)

	go func() {
		defer close(files)
		defer close(errs)

		for _, browser := range b.browsers {
			candidates := []string{
				browserDBPath(browser),
				browserBookmarkPath(browser),
			}
			var artifact FileInfo
			for _, p := range candidates {
				if p == "" {
					continue
				}
				info, err := os.Stat(p)
				if err != nil {
					continue // Browser not installed or file not accessible.
				}
				if artifact.Path == "" {
					artifact.Path = p
				}
				artifact.Size += info.Size()
				if modified := info.ModTime().Unix(); modified > artifact.ModifiedAt {
					artifact.ModifiedAt = modified
				}
			}
			if artifact.Path == "" {
				continue
			}
			select {
			case files <- artifact:
			case <-ctx.Done():
				return
			}
		}
	}()

	return files, errs
}

// Parse reads browser history and returns a document with all entries.
// Deprecated: browser indexing uses ParseDocuments so each page is searchable
// independently. Parse remains as a compatibility shim for Source callers.
func (b *BrowserSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	browser, entries, err := b.readEntries(ctx, file)
	if err != nil {
		return nil, err
	}
	return buildBrowserDocument(file, browser, entries), nil
}

// ParseDocuments returns one independently searchable document per normalized
// URL in the browser profile.
func (b *BrowserSource) ParseDocuments(ctx context.Context, file FileInfo) ([]*storage.Document, error) {
	browser, entries, err := b.readEntries(ctx, file)
	if err != nil {
		return nil, err
	}
	docs := retainBrowserDocuments(buildBrowserDocuments(file, browser, entries), b.options, time.Now())
	if b.options.IncludeContent {
		if err := b.enrichBrowserDocuments(ctx, docs); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

// ReconciliationScope identifies all virtual documents owned by one browser
// profile, regardless of whether History or Bookmarks was the scanned path.
func (b *BrowserSource) ReconciliationScope(file FileInfo) string {
	browser := identifyBrowser(file.Path)
	if browser == "" {
		return ""
	}
	return browser + ":" + browserProfile(browser, file.Path)
}

// IsDocumentInScope reports whether doc belongs to the scanned browser
// profile. The fallback recognizes aggregate documents created by older
// MindCLI versions so the first new index pass removes them.
func (b *BrowserSource) IsDocumentInScope(file FileInfo, doc *storage.Document) bool {
	if doc == nil || doc.Source != storage.SourceBrowser {
		return false
	}
	scope := b.ReconciliationScope(file)
	if scope == "" {
		return false
	}
	if doc.Metadata[IngestionScopeMetadata] == scope || doc.Metadata["browser_scope"] == scope {
		return true
	}
	if doc.Metadata["entry_count"] == "" {
		return false
	}
	browser := identifyBrowser(file.Path)
	return doc.Metadata["browser"] == browser &&
		browserProfile(browser, doc.Path) == browserProfile(browser, file.Path)
}

func (b *BrowserSource) readEntries(ctx context.Context, file FileInfo) (string, []historyEntry, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	browser := identifyBrowser(file.Path)
	if browser == "" {
		return "", nil, fmt.Errorf("unknown browser: %s", browser)
	}

	if browser == "chrome" {
		return b.readChromeProfile(file)
	}

	// Copy the database to a temp file since browsers may lock it.
	tmpFile, err := copyToTemp(file.Path)
	if err != nil {
		return "", nil, fmt.Errorf("copying browser db: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile) }()

	var entries []historyEntry
	var parseErr error

	switch browser {
	case "firefox":
		entries, parseErr = readFirefoxHistory(tmpFile)
		if parseErr == nil {
			bookmarks, err := readFirefoxBookmarks(tmpFile)
			if err == nil {
				entries = append(entries, bookmarks...)
			}
		}
	case "safari":
		entries, parseErr = readSafariHistory(tmpFile)
	}

	if parseErr != nil {
		return "", nil, parseErr
	}

	return browser, entries, nil
}

func (b *BrowserSource) readChromeProfile(file FileInfo) (string, []historyEntry, error) {
	dir := filepath.Dir(file.Path)
	historyPath := filepath.Join(dir, "History")
	bookmarksPath := filepath.Join(dir, "Bookmarks")
	var entries []historyEntry

	if _, err := os.Stat(historyPath); err == nil {
		tmpFile, err := copyToTemp(historyPath)
		if err != nil {
			return "", nil, fmt.Errorf("copying browser db: %w", err)
		}
		history, parseErr := readChromeHistory(tmpFile)
		_ = os.Remove(tmpFile)
		if parseErr != nil {
			return "", nil, parseErr
		}
		entries = append(entries, history...)
	}

	if _, err := os.Stat(bookmarksPath); err == nil {
		bookmarks, parseErr := readChromeBookmarks(bookmarksPath)
		if parseErr != nil {
			return "", nil, parseErr
		}
		entries = append(entries, bookmarks...)
	}

	return "chrome", entries, nil
}

// browserDBPath returns the history database path for a browser.
func browserDBPath(browser string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}

	switch browser {
	case "chrome":
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(home, "Library/Application Support/Google/Chrome/Default/History")
		case "linux":
			return filepath.Join(home, ".config/google-chrome/Default/History")
		}
	case "firefox":
		switch runtime.GOOS {
		case "darwin":
			return findFirefoxProfile(filepath.Join(home, "Library/Application Support/Firefox/Profiles"))
		case "linux":
			return findFirefoxProfile(filepath.Join(home, ".mozilla/firefox"))
		}
	case "safari":
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, "Library/Safari/History.db")
		}
	}
	return ""
}

// browserBookmarkPath returns bookmark file path for browsers that expose it.
func browserBookmarkPath(browser string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}

	switch browser {
	case "chrome":
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(home, "Library/Application Support/Google/Chrome/Default/Bookmarks")
		case "linux":
			return filepath.Join(home, ".config/google-chrome/Default/Bookmarks")
		}
	}
	return ""
}

// findFirefoxProfile finds the default Firefox profile's places.sqlite.
func findFirefoxProfile(profilesDir string) string {
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "default") {
			places := filepath.Join(profilesDir, e.Name(), "places.sqlite")
			if _, err := os.Stat(places); err == nil {
				return places
			}
		}
	}
	return ""
}

// identifyBrowser guesses the browser from the database path.
func identifyBrowser(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "chrome"):
		return "chrome"
	case strings.Contains(lower, "firefox") || strings.Contains(lower, "places.sqlite"):
		return "firefox"
	case strings.Contains(lower, "safari"):
		return "safari"
	}
	return ""
}

// copyToTemp copies a file to a temporary location (avoids database locks).
func copyToTemp(src string) (string, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp("", "mindcli-browser-*.db")
	if err != nil {
		_ = srcFile.Close()
		return "", err
	}

	_, copyErr := io.Copy(tmpFile, srcFile)
	srcCloseErr := srcFile.Close()
	tmpCloseErr := tmpFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmpFile.Name())
		return "", copyErr
	}
	if srcCloseErr != nil {
		_ = os.Remove(tmpFile.Name())
		return "", srcCloseErr
	}
	if tmpCloseErr != nil {
		_ = os.Remove(tmpFile.Name())
		return "", tmpCloseErr
	}

	return tmpFile.Name(), nil
}

// readChromeHistory reads Chrome's History database.
func readChromeHistory(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT url, title, visit_count, last_visit_time
		FROM urls
		WHERE title != ''
		ORDER BY last_visit_time DESC
		LIMIT 5000
	`)
	if err != nil {
		return nil, fmt.Errorf("querying chrome history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []historyEntry
	for rows.Next() {
		var url, title string
		var visitCount int
		var lastVisit int64

		if err := rows.Scan(&url, &title, &visitCount, &lastVisit); err != nil {
			continue
		}

		// Chrome stores time as microseconds since 1601-01-01.
		t := chromeTimeToGo(lastVisit)

		entries = append(entries, historyEntry{
			URL:        url,
			Title:      title,
			VisitCount: visitCount,
			LastVisit:  t,
			Browser:    "chrome",
			Kind:       "history",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading chrome history: %w", err)
	}

	return entries, nil
}

// readFirefoxHistory reads Firefox's places.sqlite database.
func readFirefoxHistory(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT url, title, visit_count, last_visit_date
		FROM moz_places
		WHERE title IS NOT NULL AND title != ''
		ORDER BY last_visit_date DESC
		LIMIT 5000
	`)
	if err != nil {
		return nil, fmt.Errorf("querying firefox history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []historyEntry
	for rows.Next() {
		var url, title string
		var visitCount int
		var lastVisit sql.NullInt64

		if err := rows.Scan(&url, &title, &visitCount, &lastVisit); err != nil {
			continue
		}

		var t time.Time
		if lastVisit.Valid {
			// Firefox stores time as microseconds since Unix epoch.
			t = time.Unix(lastVisit.Int64/1000000, (lastVisit.Int64%1000000)*1000)
		}

		entries = append(entries, historyEntry{
			URL:        url,
			Title:      title,
			VisitCount: visitCount,
			LastVisit:  t,
			Browser:    "firefox",
			Kind:       "history",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading firefox history: %w", err)
	}

	return entries, nil
}

// readSafariHistory reads Safari's History.db database.
func readSafariHistory(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// Group by URL and use the latest visit. SQLite's MAX() with bare columns
	// guarantees hv.title comes from the row holding MAX(hv.visit_time), so the
	// title and timestamp are taken from the most recent visit deterministically.
	rows, err := db.Query(`
		SELECT hi.url, hv.title, hi.visit_count, MAX(hv.visit_time) AS visit_time
		FROM history_items hi
		JOIN history_visits hv ON hi.id = hv.history_item
		WHERE hv.title IS NOT NULL AND hv.title != ''
		GROUP BY hi.url
		ORDER BY visit_time DESC
		LIMIT 5000
	`)
	if err != nil {
		return nil, fmt.Errorf("querying safari history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []historyEntry
	for rows.Next() {
		var url, title string
		var visitCount int
		var visitTime sql.NullFloat64

		if err := rows.Scan(&url, &title, &visitCount, &visitTime); err != nil {
			continue
		}

		var t time.Time
		if visitTime.Valid {
			// Safari stores time as CFAbsoluteTime: seconds since 2001-01-01.
			t = time.Unix(int64(visitTime.Float64)+978307200, 0)
		}

		entries = append(entries, historyEntry{
			URL:        url,
			Title:      title,
			VisitCount: visitCount,
			LastVisit:  t,
			Browser:    "safari",
			Kind:       "history",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading safari history: %w", err)
	}

	return entries, nil
}

// chromeTimeToGo converts Chrome's timestamp to Go time.
// Chrome uses microseconds since 1601-01-01.
func chromeTimeToGo(chromeTime int64) time.Time {
	const chromeEpochOffset = 11644473600 // seconds between 1601-01-01 and 1970-01-01
	unixMicro := chromeTime - chromeEpochOffset*1000000
	return time.Unix(unixMicro/1000000, (unixMicro%1000000)*1000)
}

func readFirefoxBookmarks(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT p.url, COALESCE(b.title, p.title, '') AS title, b.dateAdded
		FROM moz_bookmarks b
		JOIN moz_places p ON b.fk = p.id
		WHERE b.type = 1 AND p.url IS NOT NULL AND p.url != ''
		ORDER BY b.dateAdded DESC
		LIMIT 2000
	`)
	if err != nil {
		return nil, fmt.Errorf("querying firefox bookmarks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []historyEntry
	for rows.Next() {
		var url, title string
		var dateAdded sql.NullInt64
		if err := rows.Scan(&url, &title, &dateAdded); err != nil {
			continue
		}
		var addedAt time.Time
		if dateAdded.Valid {
			addedAt = time.Unix(dateAdded.Int64/1000000, (dateAdded.Int64%1000000)*1000)
		}
		entries = append(entries, historyEntry{
			URL:     url,
			Title:   title,
			AddedAt: addedAt,
			Browser: "firefox",
			Kind:    "bookmark",
		})
	}
	return entries, nil
}

type chromeBookmarksPayload struct {
	Roots map[string]chromeBookmarkNode `json:"roots"`
}

type chromeBookmarkNode struct {
	Name      string               `json:"name"`
	Type      string               `json:"type"`
	URL       string               `json:"url"`
	DateAdded string               `json:"date_added"`
	Children  []chromeBookmarkNode `json:"children"`
}

func readChromeBookmarks(path string) ([]historyEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading chrome bookmarks: %w", err)
	}

	var payload chromeBookmarksPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parsing chrome bookmarks: %w", err)
	}

	var entries []historyEntry
	for _, root := range payload.Roots {
		collectChromeBookmarks(root, &entries)
	}
	return entries, nil
}

func collectChromeBookmarks(node chromeBookmarkNode, out *[]historyEntry) {
	if node.Type == "url" && node.URL != "" {
		var addedAt time.Time
		if value, err := strconv.ParseInt(node.DateAdded, 10, 64); err == nil {
			addedAt = chromeTimeToGo(value)
		}
		*out = append(*out, historyEntry{
			URL:     node.URL,
			Title:   node.Name,
			AddedAt: addedAt,
			Browser: "chrome",
			Kind:    "bookmark",
		})
	}
	for _, child := range node.Children {
		collectChromeBookmarks(child, out)
	}
}

type browserPage struct {
	normalizedURL string
	URL           string
	title         string
	visitCount    int
	lastVisit     time.Time
	addedAt       time.Time
	history       bool
	bookmark      bool
}

func buildBrowserDocuments(file FileInfo, browser string, entries []historyEntry) []*storage.Document {
	pages := make(map[string]*browserPage)
	for _, entry := range entries {
		normalized := normalizeBrowserURL(entry.URL)
		if normalized == "" {
			continue
		}

		page := pages[normalized]
		if page == nil {
			page = &browserPage{normalizedURL: normalized, URL: entry.URL}
			pages[normalized] = page
		}
		if page.title == "" || entry.LastVisit.After(page.lastVisit) {
			page.title = entry.Title
			page.URL = entry.URL
		}
		page.visitCount += entry.VisitCount
		if entry.LastVisit.After(page.lastVisit) {
			page.lastVisit = entry.LastVisit
		}
		if page.addedAt.IsZero() || (!entry.AddedAt.IsZero() && entry.AddedAt.Before(page.addedAt)) {
			page.addedAt = entry.AddedAt
		}
		switch entry.Kind {
		case "history":
			page.history = true
		case "bookmark":
			page.bookmark = true
		}
	}

	ordered := make([]*browserPage, 0, len(pages))
	for _, page := range pages {
		ordered = append(ordered, page)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].lastVisit.Equal(ordered[j].lastVisit) {
			return ordered[i].normalizedURL < ordered[j].normalizedURL
		}
		return ordered[i].lastVisit.After(ordered[j].lastVisit)
	})

	profile := browserProfile(browser, file.Path)
	scope := browser + ":" + profile
	docs := make([]*storage.Document, 0, len(ordered))
	for _, page := range ordered {
		identityHash := sha256.Sum256([]byte(scope + "\x00" + page.normalizedURL))
		content := strings.TrimSpace(page.title + "\n" + page.URL)
		contentHash := sha256.Sum256([]byte(content))
		modifiedAt := page.lastVisit
		if modifiedAt.IsZero() {
			modifiedAt = page.addedAt
		}
		if modifiedAt.IsZero() {
			modifiedAt = time.Unix(file.ModifiedAt, 0)
		}

		metadata := map[string]string{
			"url":            page.URL,
			"normalized_url": page.normalizedURL,
			"browser":        browser,
			"profile":        profile,
			"browser_scope":  scope,
			"source_path":    file.Path,
			"visit_count":    strconv.Itoa(page.visitCount),
			"kind":           browserPageKind(page),
			"history":        strconv.FormatBool(page.history),
			"bookmark":       strconv.FormatBool(page.bookmark),
		}
		if !page.lastVisit.IsZero() {
			metadata["last_visit"] = page.lastVisit.UTC().Format(time.RFC3339)
		}
		if !page.addedAt.IsZero() {
			metadata["added_at"] = page.addedAt.UTC().Format(time.RFC3339)
		}

		title := strings.TrimSpace(page.title)
		if title == "" {
			title = page.URL
		}
		docs = append(docs, &storage.Document{
			ID:          "browser:" + hex.EncodeToString(identityHash[:16]),
			Source:      storage.SourceBrowser,
			Path:        page.URL,
			Title:       title,
			Content:     content,
			Preview:     generatePreview(content, 500),
			Metadata:    metadata,
			ContentHash: hex.EncodeToString(contentHash[:]),
			IndexedAt:   time.Now(),
			ModifiedAt:  modifiedAt,
		})
	}

	return docs
}

func browserPageKind(page *browserPage) string {
	switch {
	case page.history && page.bookmark:
		return "history,bookmark"
	case page.bookmark:
		return "bookmark"
	default:
		return "history"
	}
}

func browserProfile(browser, path string) string {
	if browser == "safari" {
		return "default"
	}
	profile := strings.TrimSpace(filepath.Base(filepath.Dir(path)))
	if profile == "" || profile == "." || profile == string(filepath.Separator) {
		return "default"
	}
	return profile
}

func normalizeBrowserURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return ""
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Fragment = ""
	parsed.User = nil
	if parsed.Host != "" {
		host := strings.ToLower(parsed.Hostname())
		port := parsed.Port()
		if port != "" && !((parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443")) {
			host += ":" + port
		}
		parsed.Host = host
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Path == "" {
		parsed.Path = "/"
	}

	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// NormalizeWebURL returns the stable HTTP(S) identity shared by browser
// indexing and deliberate URL captures.
func NormalizeWebURL(raw string) string {
	normalized := normalizeBrowserURL(raw)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return normalized
}

// buildBrowserDocument creates a Document from browser history entries.
func buildBrowserDocument(file FileInfo, browser string, entries []historyEntry) *storage.Document {
	var sb strings.Builder
	var historyCount int
	var bookmarkCount int
	for _, e := range entries {
		if e.Kind == "bookmark" {
			bookmarkCount++
			sb.WriteString("[Bookmark] ")
		} else {
			historyCount++
		}
		sb.WriteString(e.Title)
		sb.WriteString("\n")
		sb.WriteString(e.URL)
		sb.WriteString("\n\n")
	}

	content := sb.String()
	browserName := strings.ToUpper(browser[:1]) + browser[1:]
	title := fmt.Sprintf("%s Browser Data (%d entries)", browserName, len(entries))

	pathHash := sha256.Sum256([]byte(file.Path))
	id := hex.EncodeToString(pathHash[:8])

	contentHash := sha256.Sum256([]byte(content))

	return &storage.Document{
		ID:      id,
		Source:  storage.SourceBrowser,
		Path:    file.Path,
		Title:   title,
		Content: content,
		Preview: generatePreview(content, 500),
		Metadata: map[string]string{
			"browser":        browser,
			"entry_count":    fmt.Sprintf("%d", len(entries)),
			"history_count":  fmt.Sprintf("%d", historyCount),
			"bookmark_count": fmt.Sprintf("%d", bookmarkCount),
		},
		ContentHash: hex.EncodeToString(contentHash[:]),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}
}
