package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jankowtf/mindcli/internal/storage"
)

func TestEmailSourceName(t *testing.T) {
	src := NewEmailSource([]string{"/tmp"}, nil)
	if src.Name() != storage.SourceEmail {
		t.Errorf("Name() = %q, want %q", src.Name(), storage.SourceEmail)
	}
}

func TestParseEmailMessage(t *testing.T) {
	raw := `From: sender@example.com
To: receiver@example.com
Subject: Test Email
Date: Mon, 01 Jan 2024 12:00:00 +0000
Content-Type: text/plain

Hello, this is a test email body.
It has multiple lines.
`
	msg, err := parseEmailMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parseEmailMessage: %v", err)
	}

	if msg.Subject != "Test Email" {
		t.Errorf("Subject = %q, want %q", msg.Subject, "Test Email")
	}
	if msg.From != "sender@example.com" {
		t.Errorf("From = %q, want %q", msg.From, "sender@example.com")
	}
	if msg.To != "receiver@example.com" {
		t.Errorf("To = %q, want %q", msg.To, "receiver@example.com")
	}
	if !strings.Contains(msg.Body, "test email body") {
		t.Errorf("Body does not contain expected text: %q", msg.Body)
	}
}

func TestParseMbox(t *testing.T) {
	mboxContent := `From sender@example.com Mon Jan  1 12:00:00 2024
From: sender@example.com
To: receiver@example.com
Subject: First Message
Content-Type: text/plain

First message body.

From other@example.com Tue Jan  2 12:00:00 2024
From: other@example.com
To: receiver@example.com
Subject: Second Message
Content-Type: text/plain

Second message body.
`
	tmpDir := t.TempDir()

	mboxPath := filepath.Join(tmpDir, "test.mbox")
	if err := os.WriteFile(mboxPath, []byte(mboxContent), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewEmailSource([]string{tmpDir}, nil)
	info, err := os.Stat(mboxPath)
	if err != nil {
		t.Fatal(err)
	}
	file := FileInfo{
		Path:       mboxPath,
		ModifiedAt: info.ModTime().Unix(),
		Size:       info.Size(),
	}

	doc, err := src.Parse(context.Background(), file)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Title != "First Message" {
		t.Errorf("Title = %q, want %q", doc.Title, "First Message")
	}
	if doc.Source != storage.SourceEmail {
		t.Errorf("Source = %q, want %q", doc.Source, storage.SourceEmail)
	}
	if !strings.Contains(doc.Content, "First message body") {
		t.Error("Content missing first message body")
	}
	if !strings.Contains(doc.Content, "Second message body") {
		t.Error("Content missing second message body")
	}
}

func TestParseSingleEmail(t *testing.T) {
	emailContent := `From: alice@example.com
To: bob@example.com
Subject: Quick Note
Content-Type: text/plain

Just a quick note about the meeting tomorrow.
`
	tmpDir := t.TempDir()

	emlPath := filepath.Join(tmpDir, "message.eml")
	if err := os.WriteFile(emlPath, []byte(emailContent), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewEmailSource([]string{tmpDir}, nil)
	info, err := os.Stat(emlPath)
	if err != nil {
		t.Fatal(err)
	}
	file := FileInfo{
		Path:       emlPath,
		ModifiedAt: info.ModTime().Unix(),
		Size:       info.Size(),
	}

	doc, err := src.Parse(context.Background(), file)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Title != "Quick Note" {
		t.Errorf("Title = %q, want %q", doc.Title, "Quick Note")
	}
	if doc.Metadata["from"] != "a***@example.com" {
		t.Errorf("from = %q, want %q", doc.Metadata["from"], "a***@example.com")
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"<p>Hello <b>world</b></p>", "Hello world"},
		{"No tags here", "No tags here"},
		{"<html><body>Content</body></html>", "Content"},
	}

	for _, tt := range tests {
		got := stripHTML(tt.input)
		if got != tt.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEmailSourceIgnorePatterns(t *testing.T) {
	tmpDir := t.TempDir()

	includePath := filepath.Join(tmpDir, "inbox.eml")
	excludedDir := filepath.Join(tmpDir, "private")
	excludedPath := filepath.Join(excludedDir, "secret.eml")
	if err := os.MkdirAll(excludedDir, 0755); err != nil {
		t.Fatal(err)
	}

	raw := "From: a@example.com\nTo: b@example.com\nSubject: test\n\nbody"
	if err := os.WriteFile(includePath, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excludedPath, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewEmailSource([]string{tmpDir}, nil)
	src.SetIgnore([]string{"private"})
	files, errs := src.Scan(context.Background())

	var paths []string
	for f := range files {
		paths = append(paths, f.Path)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("unexpected scan error: %v", err)
		}
	}

	if len(paths) != 1 || paths[0] != includePath {
		t.Fatalf("Scan paths = %#v, want only %q", paths, includePath)
	}
}

func TestParseEmailMessageMultipartAttachments(t *testing.T) {
	raw := strings.Join([]string{
		"From: sender@example.com",
		"To: receiver@example.com",
		"Subject: With Attachment",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=BOUNDARY",
		"",
		"--BOUNDARY",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Body text",
		"--BOUNDARY",
		`Content-Type: application/pdf; name="invoice.pdf"`,
		"Content-Disposition: attachment; filename=\"invoice.pdf\"",
		"",
		"<binary>",
		"--BOUNDARY--",
		"",
	}, "\n")

	msg, err := parseEmailMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parseEmailMessage: %v", err)
	}

	if !strings.Contains(msg.Body, "Body text") {
		t.Fatalf("Body = %q, want to contain text part", msg.Body)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0] != "invoice.pdf" {
		t.Fatalf("Attachments = %#v, want [invoice.pdf]", msg.Attachments)
	}
}

func TestBuildEmailDocumentMasksPreviewAndMetadata(t *testing.T) {
	file := FileInfo{Path: "/mail/test.eml", ModifiedAt: 1700000000}
	messages := []emailMessage{
		{
			Subject: "Sensitive",
			From:    "alice@example.com",
			To:      "bob@example.com",
			Body:    "Contact me at alice@example.com with api_key=secret123 and 4242424242424242",
		},
	}

	doc := buildEmailDocument(file, messages, true)
	if strings.Contains(doc.Preview, "alice@example.com") {
		t.Fatalf("Preview should mask email addresses: %q", doc.Preview)
	}
	if strings.Contains(doc.Preview, "secret123") {
		t.Fatalf("Preview should mask api key-like values: %q", doc.Preview)
	}
	if strings.Contains(doc.Preview, "4242424242424242") {
		t.Fatalf("Preview should mask long numbers: %q", doc.Preview)
	}
	if doc.Metadata["from"] == "alice@example.com" {
		t.Fatalf("Metadata from should be masked, got %q", doc.Metadata["from"])
	}
}

func TestBuildEmailDocumentIncludesAttachmentsMetadata(t *testing.T) {
	file := FileInfo{Path: "/mail/attach.eml", ModifiedAt: 1700000000}
	messages := []emailMessage{
		{Subject: "A", Body: "B", Attachments: []string{"a.pdf", "b.png"}},
		{Subject: "C", Body: "D", Attachments: []string{"a.pdf"}},
	}

	doc := buildEmailDocument(file, messages, false)
	if got := doc.Metadata["attachments"]; got != "a.pdf, b.png" {
		t.Fatalf("attachments metadata = %q, want %q", got, "a.pdf, b.png")
	}
}
