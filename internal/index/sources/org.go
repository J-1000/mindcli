package sources

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
)

var (
	orgTitlePattern   = regexp.MustCompile(`(?i)^#\+title:\s*(.+)$`)
	orgHeadingPattern = regexp.MustCompile(`^(\*+)\s+(.+?)\s*$`)
	orgTagsPattern    = regexp.MustCompile(`\s+:([[:alnum:]_@#%:-]+):\s*$`)
)

// OrgSource indexes top-level Org-mode sections independently.
type OrgSource struct {
	scanner      *Scanner
	maxFileBytes int64
}

func NewOrgSource(paths, ignore []string, maxFileBytes int64) *OrgSource {
	return &OrgSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: []string{".org"},
			Ignore:     ignore,
		}),
		maxFileBytes: maxFileBytes,
	}
}

func (o *OrgSource) Name() storage.Source { return storage.SourceOrg }

func (o *OrgSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return o.scanner.Scan(ctx)
}

func (o *OrgSource) MatchesPath(path string) bool { return o.scanner.MatchesPath(path) }

func (o *OrgSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	docs, err := o.ParseDocuments(ctx, file)
	if err != nil {
		return nil, err
	}
	var content strings.Builder
	for _, doc := range docs {
		if content.Len() > 0 {
			content.WriteString("\n\n")
		}
		content.WriteString(doc.Content)
	}
	title := strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	metadata := map[string]string{
		"format":            "org",
		"original_path":     file.Path,
		"location":          "file",
		"extraction_method": "org_text",
	}
	return extractedDocument(storage.SourceOrg, file, title, content.String(), metadata), nil
}

func (o *OrgSource) ParseDocuments(ctx context.Context, file FileInfo) ([]*storage.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := readFileBounded(file.Path, o.maxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("reading Org file: %w", err)
	}
	fileTitle, sections := parseOrgSections(string(data))
	if fileTitle == "" {
		fileTitle = strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	}
	if len(sections) == 0 {
		sections = []orgSection{{Title: fileTitle, Content: strings.TrimSpace(string(data))}}
	}

	occurrences := make(map[string]int)
	docs := make([]*storage.Document, 0, len(sections))
	for position, section := range sections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(section.Content) == "" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(section.Title), " "))
		occurrences[key]++
		identity := key + "\x00" + strconv.Itoa(occurrences[key])
		title := section.Title
		if title == "" {
			title = fileTitle
		} else if !strings.EqualFold(fileTitle, title) {
			title = fileTitle + " — " + title
		}
		metadata := map[string]string{
			"format":            "org",
			"original_path":     file.Path,
			"location":          "section:" + strconv.Itoa(position+1),
			"section":           section.Title,
			"extraction_method": "org_text",
		}
		if len(section.Tags) > 0 {
			metadata["tags"] = strings.Join(section.Tags, ",")
		}
		docs = append(docs, &storage.Document{
			ID:          stableDocumentID(storage.SourceOrg, file.Path, identity),
			Source:      storage.SourceOrg,
			Path:        file.Path,
			Title:       title,
			Content:     section.Content,
			Preview:     generatePreview(section.Content, 500),
			Metadata:    metadata,
			ContentHash: hashContent(section.Content),
			IndexedAt:   time.Now(),
			ModifiedAt:  time.Unix(file.ModifiedAt, 0),
		})
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("Org file contains no readable text")
	}
	return docs, nil
}

func (o *OrgSource) ReconciliationScope(file FileInfo) string { return normalizePath(file.Path) }

func (o *OrgSource) IsDocumentInScope(file FileInfo, doc *storage.Document) bool {
	if doc == nil || doc.Source != storage.SourceOrg {
		return false
	}
	return doc.Metadata[IngestionScopeMetadata] == o.ReconciliationScope(file) ||
		normalizePath(doc.Metadata["original_path"]) == normalizePath(file.Path)
}

type orgSection struct {
	Title   string
	Content string
	Tags    []string
}

func parseOrgSections(content string) (string, []orgSection) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	fileTitle := ""
	var preamble []string
	var sections []orgSection
	var current *orgSection
	for _, line := range lines {
		if match := orgTitlePattern.FindStringSubmatch(line); len(match) > 1 && fileTitle == "" {
			fileTitle = strings.TrimSpace(match[1])
		}
		heading := orgHeadingPattern.FindStringSubmatch(line)
		if len(heading) > 2 && len(heading[1]) == 1 {
			if current != nil {
				current.Content = strings.TrimSpace(current.Content)
				sections = append(sections, *current)
			}
			title, tags := parseOrgHeading(heading[2])
			current = &orgSection{Title: title, Tags: tags, Content: line}
			continue
		}
		if current == nil {
			if !strings.HasPrefix(strings.TrimSpace(line), "#+") {
				preamble = append(preamble, line)
			}
			continue
		}
		current.Content += "\n" + line
	}
	if current != nil {
		current.Content = strings.TrimSpace(current.Content)
		sections = append(sections, *current)
	}
	if text := strings.TrimSpace(strings.Join(preamble, "\n")); text != "" {
		sections = append([]orgSection{{Title: fileTitle, Content: text}}, sections...)
	}
	return fileTitle, sections
}

func parseOrgHeading(value string) (string, []string) {
	value = strings.TrimSpace(value)
	var tags []string
	if match := orgTagsPattern.FindStringSubmatch(value); len(match) > 1 {
		value = strings.TrimSpace(value[:len(value)-len(match[0])])
		for _, tag := range strings.Split(match[1], ":") {
			if tag = strings.TrimSpace(tag); tag != "" {
				tags = append(tags, strings.ToLower(tag))
			}
		}
	}
	fields := strings.Fields(value)
	if len(fields) > 1 {
		switch fields[0] {
		case "TODO", "DONE", "NEXT", "WAITING", "CANCELLED":
			value = strings.TrimSpace(strings.TrimPrefix(value, fields[0]))
		}
	}
	return value, tags
}
