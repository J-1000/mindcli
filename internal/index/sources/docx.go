package sources

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/J-1000/mindcli/internal/storage"
)

// DOCXSource indexes bounded Office Open XML word-processing documents.
type DOCXSource struct {
	scanner              *Scanner
	maxFileBytes         int64
	maxDecompressedBytes int64
}

func NewDOCXSource(paths, ignore []string, maxFileBytes, maxDecompressedBytes int64) *DOCXSource {
	return &DOCXSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: []string{".docx"},
			Ignore:     ignore,
		}),
		maxFileBytes:         maxFileBytes,
		maxDecompressedBytes: maxDecompressedBytes,
	}
}

func (d *DOCXSource) Name() storage.Source { return storage.SourceDOCX }

func (d *DOCXSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return d.scanner.Scan(ctx)
}

func (d *DOCXSource) MatchesPath(path string) bool { return d.scanner.MatchesPath(path) }

func (d *DOCXSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := readFileBounded(file.Path, d.maxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("reading DOCX: %w", err)
	}
	archive, budget, err := openBoundedZIP(data, d.maxDecompressedBytes)
	if err != nil {
		return nil, fmt.Errorf("opening DOCX: %w", err)
	}
	documentEntry := findZIPEntry(archive, "word/document.xml")
	if documentEntry == nil {
		return nil, fmt.Errorf("DOCX has no word/document.xml")
	}
	documentXML, err := readZIPEntry(documentEntry, budget)
	if err != nil {
		return nil, fmt.Errorf("reading DOCX document: %w", err)
	}
	content, headings, err := extractDOCXText(documentXML)
	if err != nil {
		return nil, fmt.Errorf("parsing DOCX document: %w", err)
	}

	title := ""
	if coreEntry := findZIPEntry(archive, "docProps/core.xml"); coreEntry != nil {
		coreXML, readErr := readZIPEntry(coreEntry, budget)
		if readErr == nil {
			title = extractXMLTextElement(coreXML, "title")
		}
	}
	if title == "" && len(headings) > 0 {
		title = headings[0]
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	}
	metadata := map[string]string{
		"format":            "docx",
		"original_path":     file.Path,
		"location":          "document",
		"extraction_method": "ooxml_text",
	}
	if len(headings) > 0 {
		metadata["sections"] = strings.Join(headings, ", ")
	}
	return extractedDocument(storage.SourceDOCX, file, title, content, metadata), nil
}

func extractDOCXText(data []byte) (string, []string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var paragraphs []string
	var headings []string
	var paragraph strings.Builder
	inParagraph := false
	heading := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "p":
				inParagraph = true
				paragraph.Reset()
				heading = false
			case "pStyle":
				for _, attr := range value.Attr {
					if attr.Name.Local == "val" && strings.HasPrefix(strings.ToLower(attr.Value), "heading") {
						heading = true
					}
				}
			case "tab":
				if inParagraph {
					paragraph.WriteByte('\t')
				}
			case "br", "cr":
				if inParagraph {
					paragraph.WriteByte('\n')
				}
			}
		case xml.CharData:
			if inParagraph {
				paragraph.Write(value)
			}
		case xml.EndElement:
			if value.Name.Local != "p" || !inParagraph {
				continue
			}
			text := normalizeExtractedText(paragraph.String())
			if text != "" {
				paragraphs = append(paragraphs, text)
				if heading {
					headings = append(headings, strings.Join(strings.Fields(text), " "))
				}
			}
			inParagraph = false
		}
	}
	return strings.TrimSpace(strings.Join(paragraphs, "\n\n")), headings, nil
}

func extractXMLTextElement(data []byte, localName string) string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != localName {
			continue
		}
		var value string
		if decoder.DecodeElement(&value, &start) == nil {
			return strings.Join(strings.Fields(value), " ")
		}
	}
}
