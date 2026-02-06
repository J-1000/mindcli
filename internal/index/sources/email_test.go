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
	tmpDir, err := os.MkdirTemp("", "email-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mboxPath := filepath.Join(tmpDir, "test.mbox")
	if err := os.WriteFile(mboxPath, []byte(mboxContent), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewEmailSource([]string{tmpDir}, nil)
	info, _ := os.Stat(mboxPath)
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
	tmpDir, err := os.MkdirTemp("", "email-single-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	emlPath := filepath.Join(tmpDir, "message.eml")
	if err := os.WriteFile(emlPath, []byte(emailContent), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewEmailSource([]string{tmpDir}, nil)
	info, _ := os.Stat(emlPath)
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
	if doc.Metadata["from"] != "alice@example.com" {
		t.Errorf("from = %q, want %q", doc.Metadata["from"], "alice@example.com")
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
