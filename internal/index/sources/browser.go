package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
	_ "github.com/mattn/go-sqlite3"
)

// BrowserSource indexes browser history and bookmarks.
type BrowserSource struct {
	browsers []string
}

// NewBrowserSource creates a new browser history source.
func NewBrowserSource(browsers []string) *BrowserSource {
	if len(browsers) == 0 {
		browsers = []string{"chrome", "firefox", "safari"}
	}
	return &BrowserSource{browsers: browsers}
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
	Browser    string
	Kind       string // history or bookmark
}

// Scan finds browser history databases and returns them as files to index.
// Each browser's history is treated as a single "file" to parse.
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
			for _, p := range candidates {
				if p == "" {
					continue
				}
				info, err := os.Stat(p)
				if err != nil {
					continue // Browser not installed or file not accessible.
				}
				select {
				case files <- FileInfo{
					Path:       p,
					ModifiedAt: info.ModTime().Unix(),
					Size:       info.Size(),
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return files, errs
}

// Parse reads browser history and returns a document with all entries.
func (b *BrowserSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	browser := identifyBrowser(file.Path)
	base := strings.ToLower(filepath.Base(file.Path))

	if browser == "chrome" && base == "bookmarks" {
		entries, err := readChromeBookmarks(file.Path)
		if err != nil {
			return nil, err
		}
		return buildBrowserDocument(file, browser, entries), nil
	}

	// Copy the database to a temp file since browsers may lock it.
	tmpFile, err := copyToTemp(file.Path)
	if err != nil {
		return nil, fmt.Errorf("copying browser db: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile) }()

	var entries []historyEntry
	var parseErr error

	switch browser {
	case "chrome":
		entries, parseErr = readChromeHistory(tmpFile)
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
	default:
		return nil, fmt.Errorf("unknown browser: %s", browser)
	}

	if parseErr != nil {
		return nil, parseErr
	}

	return buildBrowserDocument(file, browser, entries), nil
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
		SELECT p.url, COALESCE(b.title, p.title, '') AS title
		FROM moz_bookmarks b
		JOIN moz_places p ON b.fk = p.id
		WHERE b.type = 1 AND p.url IS NOT NULL AND p.url != ''
		ORDER BY b.dateAdded DESC
		LIMIT 2000
	`)
	if err != nil {
		return nil, fmt.Errorf("querying firefox bookmarks: %w", err)
	}
	defer rows.Close()

	var entries []historyEntry
	for rows.Next() {
		var url, title string
		if err := rows.Scan(&url, &title); err != nil {
			continue
		}
		entries = append(entries, historyEntry{
			URL:     url,
			Title:   title,
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
	Name     string               `json:"name"`
	Type     string               `json:"type"`
	URL      string               `json:"url"`
	Children []chromeBookmarkNode `json:"children"`
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
		*out = append(*out, historyEntry{
			URL:     node.URL,
			Title:   node.Name,
			Browser: "chrome",
			Kind:    "bookmark",
		})
	}
	for _, child := range node.Children {
		collectChromeBookmarks(child, out)
	}
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
