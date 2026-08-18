package sources

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/J-1000/mindcli/internal/storage"
)

func TestHTMLSourceParsesPageAndSavedArchives(t *testing.T) {
	ctx := context.Background()
	articlePath := filepath.Join("testdata", "article.html")
	source := NewHTMLSource([]string{"testdata"}, nil, 1<<20, 1<<20)
	article, err := source.Parse(ctx, fixtureFileInfo(t, articlePath))
	if err != nil {
		t.Fatalf("Parse(article): %v", err)
	}
	if article.Title != "Local Research" || !strings.Contains(article.Content, "stays on this computer") {
		t.Fatalf("article = %#v", article)
	}
	if strings.Contains(article.Content, "secret-script") {
		t.Fatalf("script content was indexed: %q", article.Content)
	}
	if article.Metadata["format"] != "html" || article.Metadata["extraction_method"] != "html_text" {
		t.Fatalf("metadata = %#v", article.Metadata)
	}

	mhtmlPath := filepath.Join("testdata", "saved.mhtml")
	saved, err := source.Parse(ctx, fixtureFileInfo(t, mhtmlPath))
	if err != nil {
		t.Fatalf("Parse(mhtml): %v", err)
	}
	if saved.Title != "Saved Page" || saved.Metadata["source_url"] != "https://example.test/saved" {
		t.Fatalf("saved archive = %#v", saved)
	}

	webarchiveHTML := "<html><head><title>Safari Save</title></head><body>Plist archive text</body></html>"
	webarchive := `<plist><dict><key>WebMainResource</key><dict>` +
		`<key>WebResourceData</key><data>` + base64.StdEncoding.EncodeToString([]byte(webarchiveHTML)) + `</data>` +
		`<key>WebResourceURL</key><string>https://example.test/safari?a=1&amp;b=2</string>` +
		`</dict></dict></plist>`
	webarchivePath := filepath.Join(t.TempDir(), "saved.webarchive")
	mustWriteFixture(t, webarchivePath, []byte(webarchive))
	safari, err := source.Parse(ctx, fixtureFileInfo(t, webarchivePath))
	if err != nil {
		t.Fatalf("Parse(webarchive): %v", err)
	}
	if safari.Title != "Safari Save" || safari.Metadata["source_url"] != "https://example.test/safari?a=1&b=2" {
		t.Fatalf("Safari archive = %#v", safari)
	}
}

func TestDOCXSourceExtractsTitleHeadingsAndText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.docx")
	writeZIPFixture(t, path, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"docProps/core.xml":   `<?xml version="1.0"?><cp:coreProperties xmlns:cp="core" xmlns:dc="dc"><dc:title>Fixture Report</dc:title></cp:coreProperties>`,
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="word"><w:body>` +
			`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Overview</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Bounded DOCX text.</w:t></w:r><w:r><w:tab/><w:t>Second cell.</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	source := NewDOCXSource([]string{filepath.Dir(path)}, nil, 1<<20, 1<<20)
	doc, err := source.Parse(context.Background(), fixtureFileInfo(t, path))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Title != "Fixture Report" || !strings.Contains(doc.Content, "Bounded DOCX text") {
		t.Fatalf("doc = %#v", doc)
	}
	if doc.Metadata["sections"] != "Overview" || doc.Metadata["extraction_method"] != "ooxml_text" {
		t.Fatalf("metadata = %#v", doc.Metadata)
	}

	limited := NewDOCXSource(nil, nil, 1<<20, 16)
	if _, err := limited.Parse(context.Background(), fixtureFileInfo(t, path)); err == nil || !strings.Contains(err.Error(), "expansion budget") {
		t.Fatalf("limited Parse error = %v", err)
	}
}

func TestEPUBSourceReturnsStableSpineDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.epub")
	writeZIPFixture(t, path, map[string]string{
		"mimetype": `application/epub+zip`,
		"META-INF/container.xml": `<?xml version="1.0"?><container><rootfiles>` +
			`<rootfile full-path="OEBPS/content.opf"/>` +
			`</rootfiles></container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?><package xmlns:dc="dc"><metadata>` +
			`<dc:title>Local Book</dc:title><dc:creator>A. Reader</dc:creator><dc:language>en</dc:language>` +
			`</metadata><manifest>` +
			`<item id="one" href="one.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="two" href="two.xhtml" media-type="application/xhtml+xml"/>` +
			`</manifest><spine><itemref idref="one"/><itemref idref="two"/></spine></package>`,
		"OEBPS/one.xhtml": `<html><head><title>Opening</title></head><body><p>First chapter text.</p></body></html>`,
		"OEBPS/two.xhtml": `<html><head><title>Methods</title></head><body><p>Second chapter text.</p></body></html>`,
	})
	source := NewEPUBSource([]string{filepath.Dir(path)}, nil, 1<<20, 1<<20)
	file := fixtureFileInfo(t, path)
	docs, err := source.ParseDocuments(context.Background(), file)
	if err != nil {
		t.Fatalf("ParseDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("documents = %d, want 2", len(docs))
	}
	if docs[0].ID == docs[1].ID || docs[0].Path != path || docs[0].Metadata["location"] != "spine:1" {
		t.Fatalf("documents = %#v", docs)
	}
	again, err := source.ParseDocuments(context.Background(), file)
	if err != nil || again[0].ID != docs[0].ID || again[1].ID != docs[1].ID {
		t.Fatalf("stable IDs changed: first=%v second=%v err=%v", []string{docs[0].ID, docs[1].ID}, []string{again[0].ID, again[1].ID}, err)
	}
	if !source.IsDocumentInScope(file, docs[0]) {
		t.Fatal("EPUB document was not recognized as part of its artifact scope")
	}
}

func TestOrgSourceReturnsTopLevelSections(t *testing.T) {
	path := filepath.Join("testdata", "research.org")
	source := NewOrgSource([]string{"testdata"}, nil, 1<<20)
	file := fixtureFileInfo(t, path)
	docs, err := source.ParseDocuments(context.Background(), file)
	if err != nil {
		t.Fatalf("ParseDocuments: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("documents = %d, want preamble + 2 sections", len(docs))
	}
	if docs[1].Title != "Research Notebook — First Topic" || docs[1].Metadata["tags"] != "work,local" {
		t.Fatalf("first section = %#v", docs[1])
	}
	if !strings.Contains(docs[1].Content, "Nested content") || strings.Contains(docs[1].Content, "second section") {
		t.Fatalf("first section content = %q", docs[1].Content)
	}
}

func TestEmailSourceExtractsBoundedTextAttachment(t *testing.T) {
	attachment := base64.StdEncoding.EncodeToString([]byte("Roadmap attachment\nlocal search details"))
	raw := strings.Join([]string{
		"From: alice@example.com", "To: bob@example.com", "Message-ID: <fixture@example.com>",
		"Subject: Project Mail", "MIME-Version: 1.0", "Content-Type: multipart/mixed; boundary=BOUND", "",
		"--BOUND", "Content-Type: text/plain", "", "Message body", "--BOUND",
		`Content-Type: text/plain; name="roadmap.txt"`, `Content-Disposition: attachment; filename="roadmap.txt"`,
		"Content-Transfer-Encoding: base64", "", attachment, "--BOUND--", "",
	}, "\r\n")
	path := filepath.Join(t.TempDir(), "message.eml")
	mustWriteFixture(t, path, []byte(raw))
	source := NewEmailSource([]string{filepath.Dir(path)}, nil)
	source.SetMaskSensitivePreview(false)
	source.SetAttachmentOptions(EmailAttachmentOptions{
		Enabled: true, MaxAttachmentBytes: 1024, MaxDecompressedBytes: 2048, MaxArchiveDepth: 1,
	})
	docs, err := source.ParseDocuments(context.Background(), fixtureFileInfo(t, path))
	if err != nil {
		t.Fatalf("ParseDocuments: %v", err)
	}
	if len(docs) != 2 || docs[1].Title != "Project Mail — roadmap.txt" {
		t.Fatalf("documents = %#v", docs)
	}
	if docs[1].Content != "Roadmap attachment\nlocal search details" || docs[1].Metadata["parent_path"] != path {
		t.Fatalf("attachment = %#v", docs[1])
	}
	if docs[0].Metadata["extracted_attachments"] != "1" {
		t.Fatalf("base metadata = %#v", docs[0].Metadata)
	}

	source.SetAttachmentOptions(EmailAttachmentOptions{
		Enabled: true, MaxAttachmentBytes: 4, MaxDecompressedBytes: 2048, MaxArchiveDepth: 1,
	})
	limited, err := source.ParseDocuments(context.Background(), fixtureFileInfo(t, path))
	if err != nil {
		t.Fatalf("limited ParseDocuments: %v", err)
	}
	if len(limited) != 1 || limited[0].Metadata["attachment_extraction_failures"] != "1" {
		t.Fatalf("limited documents = %#v", limited)
	}
}

func TestCodeSourceScansRepositoryAndPreservesMetadata(t *testing.T) {
	root := t.TempDir()
	mustMkdirFixture(t, filepath.Join(root, ".git"))
	mustMkdirFixture(t, filepath.Join(root, "vendor"))
	mustMkdirFixture(t, filepath.Join(root, "src"))
	mustWriteFixture(t, filepath.Join(root, ".env"), []byte("TOKEN=do-not-index"))
	mustWriteFixture(t, filepath.Join(root, "vendor", "ignored.go"), []byte("package ignored"))
	codePath := filepath.Join(root, "src", "main.go")
	mustWriteFixture(t, codePath, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"))
	source := NewCodeSource([]string{root}, []string{".git", "vendor"}, 1024, 10)
	files, errs := source.Scan(context.Background())
	var scanned []FileInfo
	for file := range files {
		scanned = append(scanned, file)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
	}
	if len(scanned) != 1 || scanned[0].Path != codePath {
		t.Fatalf("scanned = %#v", scanned)
	}
	doc, err := source.Parse(context.Background(), scanned[0])
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Source != storage.SourceCode || doc.Metadata["language"] != "go" || doc.Metadata["relative_path"] != "src/main.go" {
		t.Fatalf("document = %#v", doc)
	}
}

func fixtureFileInfo(t *testing.T, path string) FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return FileInfo{Path: path, ModifiedAt: info.ModTime().Unix(), Size: info.Size()}
}

func writeZIPFixture(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	mustWriteFixture(t, path, data.Bytes())
}

func mustWriteFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
