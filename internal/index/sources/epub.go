package sources

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
)

// EPUBSource indexes each spine item in an EPUB as an independent document.
type EPUBSource struct {
	scanner              *Scanner
	maxFileBytes         int64
	maxDecompressedBytes int64
}

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Metadata struct {
		Title    string `xml:"title"`
		Creator  string `xml:"creator"`
		Language string `xml:"language"`
	} `xml:"metadata"`
	Manifest []struct {
		ID        string `xml:"id,attr"`
		Href      string `xml:"href,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"manifest>item"`
	Spine []struct {
		IDRef string `xml:"idref,attr"`
	} `xml:"spine>itemref"`
}

func NewEPUBSource(paths, ignore []string, maxFileBytes, maxDecompressedBytes int64) *EPUBSource {
	return &EPUBSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: []string{".epub"},
			Ignore:     ignore,
		}),
		maxFileBytes:         maxFileBytes,
		maxDecompressedBytes: maxDecompressedBytes,
	}
}

func (e *EPUBSource) Name() storage.Source { return storage.SourceEPUB }

func (e *EPUBSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return e.scanner.Scan(ctx)
}

func (e *EPUBSource) MatchesPath(filePath string) bool { return e.scanner.MatchesPath(filePath) }

func (e *EPUBSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	docs, err := e.ParseDocuments(ctx, file)
	if err != nil {
		return nil, err
	}
	var content strings.Builder
	for _, doc := range docs {
		if content.Len() > 0 {
			content.WriteString("\n\n---\n\n")
		}
		content.WriteString(doc.Title)
		content.WriteString("\n\n")
		content.WriteString(doc.Content)
	}
	metadata := map[string]string{
		"format":            "epub",
		"original_path":     file.Path,
		"location":          "book",
		"extraction_method": "xhtml_text",
	}
	title := strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	return extractedDocument(storage.SourceEPUB, file, title, content.String(), metadata), nil
}

func (e *EPUBSource) ParseDocuments(ctx context.Context, file FileInfo) ([]*storage.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := readFileBounded(file.Path, e.maxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("reading EPUB: %w", err)
	}
	archive, budget, err := openBoundedZIP(data, e.maxDecompressedBytes)
	if err != nil {
		return nil, fmt.Errorf("opening EPUB: %w", err)
	}
	containerEntry := findZIPEntry(archive, "META-INF/container.xml")
	if containerEntry == nil {
		return nil, fmt.Errorf("EPUB has no META-INF/container.xml")
	}
	containerData, err := readZIPEntry(containerEntry, budget)
	if err != nil {
		return nil, fmt.Errorf("reading EPUB container: %w", err)
	}
	var container epubContainer
	if err := xml.Unmarshal(containerData, &container); err != nil || len(container.Rootfiles) == 0 {
		if err == nil {
			err = fmt.Errorf("container has no rootfile")
		}
		return nil, fmt.Errorf("parsing EPUB container: %w", err)
	}
	opfPath := cleanArchivePath(container.Rootfiles[0].FullPath)
	if opfPath == "" {
		return nil, fmt.Errorf("EPUB rootfile path is unsafe")
	}
	opfEntry := findZIPEntry(archive, opfPath)
	if opfEntry == nil {
		return nil, fmt.Errorf("EPUB package %q is missing", opfPath)
	}
	opfData, err := readZIPEntry(opfEntry, budget)
	if err != nil {
		return nil, fmt.Errorf("reading EPUB package: %w", err)
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, fmt.Errorf("parsing EPUB package: %w", err)
	}

	manifest := make(map[string]struct {
		href      string
		mediaType string
	})
	for _, item := range pkg.Manifest {
		manifest[item.ID] = struct {
			href      string
			mediaType string
		}{href: item.Href, mediaType: item.MediaType}
	}
	spine := append([]struct {
		IDRef string `xml:"idref,attr"`
	}(nil), pkg.Spine...)
	if len(spine) == 0 {
		ids := make([]string, 0, len(manifest))
		for id, item := range manifest {
			if isEPUBTextType(item.mediaType, item.href) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			spine = append(spine, struct {
				IDRef string `xml:"idref,attr"`
			}{IDRef: id})
		}
	}

	bookTitle := strings.Join(strings.Fields(pkg.Metadata.Title), " ")
	if bookTitle == "" {
		bookTitle = strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	}
	var docs []*storage.Document
	for position, ref := range spine {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, ok := manifest[ref.IDRef]
		if !ok || !isEPUBTextType(item.mediaType, item.href) {
			continue
		}
		entryPath := cleanArchivePath(path.Join(path.Dir(opfPath), item.href))
		if entryPath == "" {
			continue
		}
		entry := findZIPEntry(archive, entryPath)
		if entry == nil {
			continue
		}
		chapterData, readErr := readZIPEntry(entry, budget)
		if readErr != nil {
			return nil, fmt.Errorf("reading EPUB section %q: %w", entryPath, readErr)
		}
		chapterTitle, content, parseErr := extractHTMLText(bytes.NewReader(chapterData))
		if parseErr != nil {
			return nil, fmt.Errorf("parsing EPUB section %q: %w", entryPath, parseErr)
		}
		if content == "" {
			continue
		}
		if chapterTitle == "" {
			chapterTitle = strings.TrimSuffix(path.Base(entryPath), path.Ext(entryPath))
		}
		title := chapterTitle
		if !strings.EqualFold(bookTitle, chapterTitle) {
			title = bookTitle + " — " + chapterTitle
		}
		metadata := map[string]string{
			"format":            "epub",
			"original_path":     file.Path,
			"location":          "spine:" + strconv.Itoa(position+1),
			"section":           chapterTitle,
			"archive_path":      entryPath,
			"extraction_method": "xhtml_text",
		}
		if creator := strings.Join(strings.Fields(pkg.Metadata.Creator), " "); creator != "" {
			metadata["creator"] = creator
		}
		if language := strings.TrimSpace(pkg.Metadata.Language); language != "" {
			metadata["language"] = language
		}
		doc := &storage.Document{
			ID:          stableDocumentID(storage.SourceEPUB, file.Path, entryPath),
			Source:      storage.SourceEPUB,
			Path:        file.Path,
			Title:       title,
			Content:     content,
			Preview:     generatePreview(content, 500),
			Metadata:    metadata,
			ContentHash: hashContent(content),
			IndexedAt:   time.Now(),
			ModifiedAt:  time.Unix(file.ModifiedAt, 0),
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("EPUB contains no readable spine sections")
	}
	return docs, nil
}

func (e *EPUBSource) ReconciliationScope(file FileInfo) string {
	return normalizePath(file.Path)
}

func (e *EPUBSource) IsDocumentInScope(file FileInfo, doc *storage.Document) bool {
	if doc == nil || doc.Source != storage.SourceEPUB {
		return false
	}
	return doc.Metadata[IngestionScopeMetadata] == e.ReconciliationScope(file) ||
		normalizePath(doc.Metadata["original_path"]) == normalizePath(file.Path)
}

func isEPUBTextType(mediaType, href string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "application/xhtml+xml" || mediaType == "text/html" {
		return true
	}
	ext := strings.ToLower(path.Ext(href))
	return ext == ".xhtml" || ext == ".html" || ext == ".htm"
}

func cleanArchivePath(value string) string {
	value = strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"), "/")
	value = path.Clean(value)
	if value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return ""
	}
	return value
}
