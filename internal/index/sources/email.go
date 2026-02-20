package sources

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

// EmailSource indexes email archives (mbox, maildir, emlx).
type EmailSource struct {
	paths   []string
	formats []string
	ignore  []string
}

// NewEmailSource creates a new email source.
func NewEmailSource(paths, formats []string) *EmailSource {
	if len(formats) == 0 {
		formats = []string{"mbox", "maildir", "emlx"}
	}
	return &EmailSource{
		paths:   paths,
		formats: formats,
	}
}

// Name returns the source name.
func (e *EmailSource) Name() storage.Source {
	return storage.SourceEmail
}

// Scan walks configured paths and returns email files to index.
func (e *EmailSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	files := make(chan FileInfo, 100)
	errs := make(chan error, 10)

	go func() {
		defer close(files)
		defer close(errs)

		for _, basePath := range e.paths {
			path := expandPath(basePath)
			info, err := os.Stat(path)
			if err != nil {
				if !os.IsNotExist(err) {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			if !info.IsDir() {
				// Single mbox file
				if e.isEmailFile(path) {
					select {
					case files <- FileInfo{
						Path:       path,
						ModifiedAt: info.ModTime().Unix(),
						Size:       info.Size(),
					}:
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			// Walk directory for email files
			filepath.WalkDir(path, func(fp string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				if d.IsDir() {
					return nil
				}
				if !e.isEmailFile(fp) {
					return nil
				}
				fi, err := d.Info()
				if err != nil {
					return nil
				}
				select {
				case files <- FileInfo{
					Path:       fp,
					ModifiedAt: fi.ModTime().Unix(),
					Size:       fi.Size(),
				}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			})
		}
	}()

	return files, errs
}

// MatchesPath reports whether this source is configured to handle the path.
func (e *EmailSource) MatchesPath(path string) bool {
	filePath := normalizePath(path)
	if !e.isEmailFile(filePath) {
		return false
	}

	for _, p := range e.paths {
		if pathWithin(filePath, normalizePath(expandPath(p))) {
			return true
		}
	}

	return false
}

// Parse reads an email file and returns the parsed document.
// For mbox files, the first message is used as the document.
func (e *EmailSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	ext := strings.ToLower(filepath.Ext(file.Path))

	switch ext {
	case ".mbox":
		return e.parseMbox(file)
	case ".emlx":
		return e.parseEmlx(file)
	default:
		// Try parsing as a single email message (maildir or .eml)
		return e.parseSingleEmail(file)
	}
}

func (e *EmailSource) isEmailFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mbox", ".eml", ".emlx":
		return true
	}
	// Maildir files typically have no extension
	dir := filepath.Base(filepath.Dir(path))
	return dir == "cur" || dir == "new"
}

// parseMbox parses an mbox file and creates a document from its messages.
func (e *EmailSource) parseMbox(file FileInfo) (*storage.Document, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return nil, fmt.Errorf("opening mbox: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	var messages []emailMessage
	var currentMsg strings.Builder
	inMessage := false

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "From ") && (currentMsg.Len() == 0 || inMessage) {
			if inMessage && currentMsg.Len() > 0 {
				msg, err := parseEmailMessage(strings.NewReader(currentMsg.String()))
				if err == nil {
					messages = append(messages, msg)
				}
				currentMsg.Reset()
			}
			inMessage = true
			continue
		}

		if inMessage {
			currentMsg.WriteString(line)
			currentMsg.WriteByte('\n')
		}
	}

	// Parse last message
	if currentMsg.Len() > 0 {
		msg, err := parseEmailMessage(strings.NewReader(currentMsg.String()))
		if err == nil {
			messages = append(messages, msg)
		}
	}

	return buildEmailDocument(file, messages), nil
}

// parseEmlx parses an Apple Mail .emlx file.
func (e *EmailSource) parseEmlx(file FileInfo) (*storage.Document, error) {
	data, err := os.ReadFile(file.Path)
	if err != nil {
		return nil, fmt.Errorf("reading emlx: %w", err)
	}

	content := string(data)
	// .emlx files start with a byte count on the first line, followed by the RFC 2822 message.
	if idx := strings.Index(content, "\n"); idx != -1 {
		content = content[idx+1:]
	}
	// Trim trailing Apple plist metadata
	if idx := strings.Index(content, "<?xml"); idx != -1 {
		content = content[:idx]
	}

	msg, err := parseEmailMessage(strings.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("parsing emlx message: %w", err)
	}

	return buildEmailDocument(file, []emailMessage{msg}), nil
}

// parseSingleEmail parses a single .eml or maildir message.
func (e *EmailSource) parseSingleEmail(file FileInfo) (*storage.Document, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return nil, fmt.Errorf("opening email: %w", err)
	}
	defer f.Close()

	msg, err := parseEmailMessage(f)
	if err != nil {
		return nil, fmt.Errorf("parsing email: %w", err)
	}

	return buildEmailDocument(file, []emailMessage{msg}), nil
}

// emailMessage holds parsed email data.
type emailMessage struct {
	Subject string
	From    string
	To      string
	Date    time.Time
	Body    string
}

// parseEmailMessage parses a single RFC 2822 email message.
func parseEmailMessage(r io.Reader) (emailMessage, error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return emailMessage{}, err
	}

	var em emailMessage
	em.Subject = decodeHeader(msg.Header.Get("Subject"))
	em.From = decodeHeader(msg.Header.Get("From"))
	em.To = decodeHeader(msg.Header.Get("To"))

	if dateStr := msg.Header.Get("Date"); dateStr != "" {
		em.Date, _ = mail.ParseDate(dateStr)
	}

	em.Body = extractBody(msg)
	return em, nil
}

// extractBody extracts plain text from an email message body.
func extractBody(msg *mail.Message) string {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Fall back to reading body directly.
		body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20)) // 1MB limit
		return string(body)
	}

	if strings.HasPrefix(mediaType, "text/plain") {
		body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
		return string(body)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
			return string(body)
		}
		return extractMultipartText(msg.Body, boundary)
	}

	// For HTML-only or other types, read raw.
	body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
	return stripHTML(string(body))
}

// extractMultipartText extracts text/plain parts from a multipart message.
func extractMultipartText(r io.Reader, boundary string) string {
	mr := multipart.NewReader(r, boundary)
	var textParts []string

	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		ct := part.Header.Get("Content-Type")
		mediaType, _, _ := mime.ParseMediaType(ct)

		if strings.HasPrefix(mediaType, "text/plain") {
			body, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			textParts = append(textParts, string(body))
		}
	}

	if len(textParts) > 0 {
		return strings.Join(textParts, "\n\n")
	}
	return ""
}

// stripHTML removes HTML tags from text (basic implementation).
func stripHTML(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}

// decodeHeader decodes MIME-encoded header values.
func decodeHeader(s string) string {
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}

// buildEmailDocument creates a Document from parsed email messages.
func buildEmailDocument(file FileInfo, messages []emailMessage) *storage.Document {
	if len(messages) == 0 {
		return &storage.Document{
			ID:          hashPath(file.Path),
			Source:      storage.SourceEmail,
			Path:        file.Path,
			Title:       filepath.Base(file.Path),
			Content:     "",
			Preview:     "",
			ContentHash: hashContent(""),
			IndexedAt:   time.Now(),
			ModifiedAt:  time.Unix(file.ModifiedAt, 0),
		}
	}

	// Use first message for title, combine all bodies for content.
	var sb strings.Builder
	var title string
	metadata := make(map[string]string)

	for i, msg := range messages {
		if i == 0 {
			title = msg.Subject
			if title == "" {
				title = filepath.Base(file.Path)
			}
			metadata["from"] = msg.From
			metadata["to"] = msg.To
			if !msg.Date.IsZero() {
				metadata["date"] = msg.Date.Format(time.RFC3339)
			}
		}

		if msg.Body != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			if msg.Subject != "" {
				sb.WriteString("Subject: ")
				sb.WriteString(msg.Subject)
				sb.WriteString("\n\n")
			}
			sb.WriteString(msg.Body)
		}
	}

	content := sb.String()

	return &storage.Document{
		ID:          hashPath(file.Path),
		Source:      storage.SourceEmail,
		Path:        file.Path,
		Title:       title,
		Content:     content,
		Preview:     generatePreview(content, 500),
		Metadata:    metadata,
		ContentHash: hashContent(content),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}
}

func hashPath(path string) string {
	h := sha256.Sum256([]byte(path))
	return hex.EncodeToString(h[:8])
}

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}
