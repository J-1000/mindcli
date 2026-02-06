package sources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// historyEntry holds a single browser history entry.
type historyEntry struct {
	URL        string
	Title      string
	VisitCount int
	LastVisit  time.Time
	Browser    string
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
			dbPath := browserDBPath(browser)
			if dbPath == "" {
				continue
			}

			info, err := os.Stat(dbPath)
			if err != nil {
				continue // Browser not installed or history not accessible
			}

			select {
			case files <- FileInfo{
				Path:       dbPath,
				ModifiedAt: info.ModTime().Unix(),
				Size:       info.Size(),
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return files, errs
}

// Parse reads browser history and returns a document with all entries.
func (b *BrowserSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	browser := identifyBrowser(file.Path)

	// Copy the database to a temp file since browsers may lock it.
	tmpFile, err := copyToTemp(file.Path)
	if err != nil {
		return nil, fmt.Errorf("copying browser db: %w", err)
	}
	defer os.Remove(tmpFile)

	var entries []historyEntry
	var parseErr error

	switch browser {
	case "chrome":
		entries, parseErr = readChromeHistory(tmpFile)
	case "firefox":
		entries, parseErr = readFirefoxHistory(tmpFile)
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
	defer srcFile.Close()

	tmpFile, err := os.CreateTemp("", "mindcli-browser-*.db")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// readChromeHistory reads Chrome's History database.
func readChromeHistory(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

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
	defer rows.Close()

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
		})
	}

	return entries, nil
}

// readFirefoxHistory reads Firefox's places.sqlite database.
func readFirefoxHistory(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

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
	defer rows.Close()

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
		})
	}

	return entries, nil
}

// readSafariHistory reads Safari's History.db database.
func readSafariHistory(dbPath string) ([]historyEntry, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT hi.url, hv.title, hi.visit_count
		FROM history_items hi
		LEFT JOIN history_visits hv ON hi.id = hv.history_item
		WHERE hv.title IS NOT NULL AND hv.title != ''
		GROUP BY hi.url
		ORDER BY hv.visit_time DESC
		LIMIT 5000
	`)
	if err != nil {
		return nil, fmt.Errorf("querying safari history: %w", err)
	}
	defer rows.Close()

	var entries []historyEntry
	for rows.Next() {
		var url, title string
		var visitCount int

		if err := rows.Scan(&url, &title, &visitCount); err != nil {
			continue
		}

		entries = append(entries, historyEntry{
			URL:        url,
			Title:      title,
			VisitCount: visitCount,
			Browser:    "safari",
		})
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

// buildBrowserDocument creates a Document from browser history entries.
func buildBrowserDocument(file FileInfo, browser string, entries []historyEntry) *storage.Document {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Title)
		sb.WriteString("\n")
		sb.WriteString(e.URL)
		sb.WriteString("\n\n")
	}

	content := sb.String()
	browserName := strings.ToUpper(browser[:1]) + browser[1:]
	title := fmt.Sprintf("%s Browser History (%d entries)", browserName, len(entries))

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
			"browser":     browser,
			"entry_count": fmt.Sprintf("%d", len(entries)),
		},
		ContentHash: hex.EncodeToString(contentHash[:]),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}
}
