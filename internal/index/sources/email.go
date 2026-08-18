package sources

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/J-1000/mindcli/internal/storage"
)

// EmailSource indexes email archives (mbox, maildir, emlx).
type EmailSource struct {
	paths                []string
	formats              []string
	ignore               []string
	maskSensitivePreview bool
	attachmentOptions    EmailAttachmentOptions
}

// EmailAttachmentOptions bounds the optional local extraction of textual
// attachments. ArchiveDepth 1 permits one DOCX/EPUB container; zero disables
// archive-backed attachment formats.
type EmailAttachmentOptions struct {
	Enabled              bool
	MaxAttachmentBytes   int64
	MaxDecompressedBytes int64
	MaxArchiveDepth      int
}

func DefaultEmailAttachmentOptions() EmailAttachmentOptions {
	return EmailAttachmentOptions{
		MaxAttachmentBytes:   16 << 20,
		MaxDecompressedBytes: 64 << 20,
		MaxArchiveDepth:      1,
	}
}

// NewEmailSource creates a new email source.
func NewEmailSource(paths, formats []string) *EmailSource {
	if len(formats) == 0 {
		formats = []string{"mbox", "maildir", "emlx"}
	}
	return &EmailSource{
		paths:                paths,
		formats:              formats,
		maskSensitivePreview: true,
		attachmentOptions:    DefaultEmailAttachmentOptions(),
	}
}

// SetIgnore configures path exclusion patterns.
func (e *EmailSource) SetIgnore(patterns []string) {
	e.ignore = append([]string(nil), patterns...)
}

// SetMaskSensitivePreview controls redaction in preview/metadata fields.
func (e *EmailSource) SetMaskSensitivePreview(enabled bool) {
	e.maskSensitivePreview = enabled
}

// SetAttachmentOptions enables and bounds attachment extraction.
func (e *EmailSource) SetAttachmentOptions(options EmailAttachmentOptions) {
	defaults := DefaultEmailAttachmentOptions()
	if options.MaxAttachmentBytes < 1 {
		options.MaxAttachmentBytes = defaults.MaxAttachmentBytes
	}
	if options.MaxDecompressedBytes < 1 {
		options.MaxDecompressedBytes = defaults.MaxDecompressedBytes
	}
	if options.MaxArchiveDepth < 0 {
		options.MaxArchiveDepth = 0
	}
	e.attachmentOptions = options
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
			walkErr := filepath.WalkDir(path, func(fp string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				if d.IsDir() {
					return nil
				}
				if e.shouldIgnorePath(fp) {
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
			if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
				select {
				case errs <- walkErr:
				case <-ctx.Done():
					return
				}
			}
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

// ParseDocuments returns the email body plus independently searchable textual
// attachments when extraction is explicitly enabled.
func (e *EmailSource) ParseDocuments(ctx context.Context, file FileInfo) ([]*storage.Document, error) {
	if !e.attachmentOptions.Enabled {
		doc, err := e.Parse(ctx, file)
		if err != nil {
			return nil, err
		}
		return []*storage.Document{doc}, nil
	}
	messages, err := e.readMessagesWithAttachments(file)
	if err != nil {
		return nil, err
	}
	base := buildEmailDocument(file, messages, e.maskSensitivePreview)
	docs := []*storage.Document{base}
	remaining := e.attachmentOptions.MaxDecompressedBytes
	var extractionErrors []string
	for messageIndex, message := range messages {
		for attachmentIndex, attachment := range message.AttachmentParts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			doc, consumed, extractErr := e.buildAttachmentDocument(
				ctx, file, message, messageIndex, attachment, attachmentIndex, remaining,
			)
			if extractErr != nil {
				extractionErrors = append(extractionErrors, attachment.Name+": "+extractErr.Error())
				continue
			}
			remaining -= consumed
			docs = append(docs, doc)
		}
	}
	if len(extractionErrors) > 0 {
		base.Metadata["attachment_extraction_warning"] = strings.Join(extractionErrors, "; ")
		base.Metadata["attachment_extraction_failures"] = strconv.Itoa(len(extractionErrors))
	}
	base.Metadata["extracted_attachments"] = strconv.Itoa(len(docs) - 1)
	return docs, nil
}

func (e *EmailSource) ReconciliationScope(file FileInfo) string { return normalizePath(file.Path) }

func (e *EmailSource) IsDocumentInScope(file FileInfo, doc *storage.Document) bool {
	if doc == nil || doc.Source != storage.SourceEmail {
		return false
	}
	return doc.Metadata[IngestionScopeMetadata] == e.ReconciliationScope(file) ||
		normalizePath(doc.Metadata["original_path"]) == normalizePath(file.Path) ||
		normalizePath(doc.Metadata["parent_path"]) == normalizePath(file.Path) ||
		normalizePath(doc.Path) == normalizePath(file.Path)
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
	if err := scanner.Err(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("reading mbox: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("closing mbox: %w", err)
	}

	return buildEmailDocument(file, messages, e.maskSensitivePreview), nil
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

	return buildEmailDocument(file, []emailMessage{msg}, e.maskSensitivePreview), nil
}

// parseSingleEmail parses a single .eml or maildir message.
func (e *EmailSource) parseSingleEmail(file FileInfo) (*storage.Document, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return nil, fmt.Errorf("opening email: %w", err)
	}
	msg, err := parseEmailMessage(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("parsing email: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("closing email: %w", err)
	}

	return buildEmailDocument(file, []emailMessage{msg}, e.maskSensitivePreview), nil
}

// emailMessage holds parsed email data.
type emailMessage struct {
	Subject         string
	From            string
	To              string
	MessageID       string
	Date            time.Time
	Body            string
	Attachments     []string
	AttachmentParts []emailAttachment
}

type emailAttachment struct {
	Name      string
	MediaType string
	Data      []byte
	Error     string
}

func (e *EmailSource) readMessagesWithAttachments(file FileInfo) ([]emailMessage, error) {
	parse := func(reader io.Reader) (emailMessage, error) {
		return parseEmailMessageWithOptions(reader, true, e.attachmentOptions.MaxAttachmentBytes)
	}
	switch strings.ToLower(filepath.Ext(file.Path)) {
	case ".mbox":
		f, err := os.Open(file.Path)
		if err != nil {
			return nil, fmt.Errorf("opening mbox: %w", err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		var messages []emailMessage
		var current strings.Builder
		inMessage := false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "From ") && (current.Len() == 0 || inMessage) {
				if inMessage && current.Len() > 0 {
					if message, parseErr := parse(strings.NewReader(current.String())); parseErr == nil {
						messages = append(messages, message)
					}
					current.Reset()
				}
				inMessage = true
				continue
			}
			if inMessage {
				current.WriteString(line)
				current.WriteByte('\n')
			}
		}
		if current.Len() > 0 {
			if message, parseErr := parse(strings.NewReader(current.String())); parseErr == nil {
				messages = append(messages, message)
			}
		}
		scanErr := scanner.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("reading mbox: %w", scanErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing mbox: %w", closeErr)
		}
		return messages, nil
	case ".emlx":
		data, err := os.ReadFile(file.Path)
		if err != nil {
			return nil, fmt.Errorf("reading emlx: %w", err)
		}
		content := string(data)
		if index := strings.Index(content, "\n"); index >= 0 {
			content = content[index+1:]
		}
		if index := strings.Index(content, "<?xml"); index >= 0 {
			content = content[:index]
		}
		message, err := parse(strings.NewReader(content))
		if err != nil {
			return nil, fmt.Errorf("parsing emlx message: %w", err)
		}
		return []emailMessage{message}, nil
	default:
		f, err := os.Open(file.Path)
		if err != nil {
			return nil, fmt.Errorf("opening email: %w", err)
		}
		message, parseErr := parse(f)
		closeErr := f.Close()
		if parseErr != nil {
			return nil, fmt.Errorf("parsing email: %w", parseErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing email: %w", closeErr)
		}
		return []emailMessage{message}, nil
	}
}

// parseEmailMessage parses a single RFC 2822 email message.
func parseEmailMessage(r io.Reader) (emailMessage, error) {
	return parseEmailMessageWithOptions(r, false, 0)
}

func parseEmailMessageWithOptions(r io.Reader, captureAttachments bool, maxAttachmentBytes int64) (emailMessage, error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return emailMessage{}, err
	}

	var em emailMessage
	em.Subject = decodeHeader(msg.Header.Get("Subject"))
	em.From = decodeHeader(msg.Header.Get("From"))
	em.To = decodeHeader(msg.Header.Get("To"))
	em.MessageID = strings.TrimSpace(msg.Header.Get("Message-ID"))

	if dateStr := msg.Header.Get("Date"); dateStr != "" {
		em.Date, _ = mail.ParseDate(dateStr)
	}

	if captureAttachments {
		em.Body, em.Attachments, em.AttachmentParts = extractMIMEContent(msg, maxAttachmentBytes)
	} else {
		em.Body, em.Attachments = extractBodyAndAttachments(msg)
	}
	return em, nil
}

type mimeExtractResult struct {
	plain       []string
	html        []string
	names       []string
	attachments []emailAttachment
}

func extractMIMEContent(msg *mail.Message, maxAttachmentBytes int64) (string, []string, []emailAttachment) {
	result := extractMIMEEntity(textproto.MIMEHeader(msg.Header), msg.Body, maxAttachmentBytes, 0)
	parts := result.plain
	if len(parts) == 0 {
		parts = result.html
	}
	names := uniqueSortedStrings(result.names)
	return strings.TrimSpace(strings.Join(parts, "\n\n")), names, result.attachments
}

func extractMIMEEntity(header textproto.MIMEHeader, body io.Reader, maxAttachmentBytes int64, depth int) mimeExtractResult {
	if depth > 16 {
		return mimeExtractResult{}
	}
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "application/octet-stream"
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return mimeExtractResult{}
		}
		reader := multipart.NewReader(body, boundary)
		var result mimeExtractResult
		for {
			part, partErr := reader.NextPart()
			if partErr != nil {
				break
			}
			child := extractMIMEEntity(part.Header, part, maxAttachmentBytes, depth+1)
			_ = part.Close()
			result.plain = append(result.plain, child.plain...)
			result.html = append(result.html, child.html...)
			result.names = append(result.names, child.names...)
			result.attachments = append(result.attachments, child.attachments...)
		}
		return result
	}

	filename := strings.TrimSpace(decodeHeader(mimeFilename(header)))
	disposition, _, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	isAttachment := filename != "" || strings.EqualFold(disposition, "attachment")
	if isAttachment {
		if filename == "" {
			filename = "attachment" + extensionForMediaType(mediaType)
		}
		result := mimeExtractResult{names: []string{filename}}
		if !isSupportedEmailAttachment(filename, mediaType) {
			return result
		}
		data, readErr := readMIMEBodyBounded(header, body, maxAttachmentBytes)
		attachment := emailAttachment{Name: filename, MediaType: mediaType, Data: data}
		if readErr != nil {
			attachment.Error = readErr.Error()
			attachment.Data = nil
		}
		result.attachments = append(result.attachments, attachment)
		return result
	}

	if mediaType != "text/plain" && mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return mimeExtractResult{}
	}
	data, readErr := readMIMEBodyBounded(header, body, 1<<20)
	if readErr != nil {
		return mimeExtractResult{}
	}
	text := strings.TrimSpace(string(data))
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		text = stripHTML(text)
		if text == "" {
			return mimeExtractResult{}
		}
		return mimeExtractResult{html: []string{text}}
	}
	if text == "" {
		return mimeExtractResult{}
	}
	return mimeExtractResult{plain: []string{text}}
}

func readMIMEBodyBounded(header textproto.MIMEHeader, body io.Reader, maxBytes int64) ([]byte, error) {
	decoded := body
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		decoded = quotedprintable.NewReader(body)
	}
	return readBounded(decoded, maxBytes)
}

func mimeFilename(header textproto.MIMEHeader) string {
	for _, field := range []string{"Content-Disposition", "Content-Type"} {
		_, params, err := mime.ParseMediaType(header.Get(field))
		if err != nil {
			continue
		}
		for _, name := range []string{"filename", "name"} {
			if value := strings.TrimSpace(params[name]); value != "" {
				return filepath.Base(value)
			}
		}
	}
	return ""
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]string)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[strings.ToLower(value)] = value
		}
	}
	result := make([]string, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func extensionForMediaType(mediaType string) string {
	switch mediaType {
	case "text/html", "application/xhtml+xml":
		return ".html"
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/epub+zip":
		return ".epub"
	case "text/plain":
		return ".txt"
	default:
		return ""
	}
}

func isSupportedEmailAttachment(name, mediaType string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".html", ".htm", ".mhtml", ".mht", ".webarchive", ".docx", ".epub", ".org",
		".txt", ".md", ".markdown", ".pdf", ".csv", ".json", ".xml", ".yaml", ".yml":
		return true
	}
	return strings.HasPrefix(mediaType, "text/")
}

func (e *EmailSource) buildAttachmentDocument(
	ctx context.Context,
	file FileInfo,
	message emailMessage,
	messageIndex int,
	attachment emailAttachment,
	attachmentIndex int,
	remaining int64,
) (*storage.Document, int64, error) {
	if attachment.Error != "" {
		return nil, 0, fmt.Errorf("reading MIME attachment: %s", attachment.Error)
	}
	if len(attachment.Data) == 0 {
		return nil, 0, fmt.Errorf("attachment is empty")
	}
	ext := strings.ToLower(filepath.Ext(attachment.Name))
	if ext == "" {
		ext = extensionForMediaType(attachment.MediaType)
	}
	archiveDepth := 0
	consumed := int64(len(attachment.Data))
	if ext == ".docx" || ext == ".epub" || ext == ".mhtml" || ext == ".mht" || ext == ".webarchive" {
		archiveDepth = 1
		if e.attachmentOptions.MaxArchiveDepth < archiveDepth {
			return nil, 0, fmt.Errorf("archive depth %d exceeds configured maximum %d", archiveDepth, e.attachmentOptions.MaxArchiveDepth)
		}
	}
	if ext == ".docx" || ext == ".epub" {
		archive, _, err := openBoundedZIP(attachment.Data, remaining)
		if err != nil {
			return nil, 0, fmt.Errorf("opening attachment archive: %w", err)
		}
		consumed = 0
		for _, entry := range archive.File {
			if entry.UncompressedSize64 > uint64(remaining-consumed) {
				return nil, 0, fmt.Errorf("expanded archive exceeds remaining %d-byte limit", remaining)
			}
			consumed += int64(entry.UncompressedSize64)
		}
	}
	if consumed > remaining {
		return nil, 0, fmt.Errorf("content requires %d bytes; %d-byte decompression budget remains", consumed, remaining)
	}

	content, extractedMetadata, err := extractEmailAttachment(ctx, attachment, ext, remaining)
	if err != nil {
		return nil, 0, err
	}
	if e.maskSensitivePreview {
		content = maskSensitiveText(content)
	}
	messageIdentity := message.MessageID
	if messageIdentity == "" {
		messageIdentity = message.Subject + "\x00" + message.Date.UTC().Format(time.RFC3339) + "\x00" + strconv.Itoa(messageIndex)
	}
	title := attachment.Name
	if strings.TrimSpace(message.Subject) != "" {
		title = message.Subject + " — " + attachment.Name
	}
	metadata := map[string]string{
		"format":            strings.TrimPrefix(ext, "."),
		"original_path":     file.Path,
		"parent_path":       file.Path,
		"attachment_name":   attachment.Name,
		"content_type":      attachment.MediaType,
		"location":          fmt.Sprintf("message:%d/attachment:%d", messageIndex+1, attachmentIndex+1),
		"extraction_method": "attachment_text",
		"archive_depth":     strconv.Itoa(archiveDepth),
	}
	for key, value := range extractedMetadata {
		if value != "" && key != "original_path" {
			metadata[key] = value
		}
	}
	if message.MessageID != "" {
		metadata["message_id"] = message.MessageID
	}
	return &storage.Document{
		ID: stableDocumentID(
			storage.SourceEmail,
			file.Path,
			messageIdentity,
			strconv.Itoa(attachmentIndex),
			attachment.Name,
		),
		Source:      storage.SourceEmail,
		Path:        file.Path,
		Title:       title,
		Content:     content,
		Preview:     generatePreview(content, 500),
		Metadata:    metadata,
		ContentHash: hashContent(content),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}, consumed, nil
}

func extractEmailAttachment(ctx context.Context, attachment emailAttachment, ext string, maxDecompressedBytes int64) (string, map[string]string, error) {
	metadata := map[string]string{}
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".json", ".xml", ".yaml", ".yml":
		if !utf8.Valid(attachment.Data) || bytes.IndexByte(attachment.Data, 0) >= 0 {
			return "", nil, fmt.Errorf("text attachment is not valid UTF-8")
		}
		metadata["extraction_method"] = "plain_text"
		return strings.TrimSpace(string(attachment.Data)), metadata, nil
	}

	tempFile, err := os.CreateTemp("", "mindcli-attachment-*"+ext)
	if err != nil {
		return "", nil, fmt.Errorf("creating private attachment file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := tempFile.Write(attachment.Data); err != nil {
		_ = tempFile.Close()
		return "", nil, fmt.Errorf("writing private attachment file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return "", nil, fmt.Errorf("closing private attachment file: %w", err)
	}
	file := FileInfo{Path: tempPath, Size: int64(len(attachment.Data)), ModifiedAt: time.Now().Unix()}
	var doc *storage.Document
	switch ext {
	case ".html", ".htm", ".mhtml", ".mht", ".webarchive":
		doc, err = NewHTMLSource(nil, nil, int64(len(attachment.Data)), maxDecompressedBytes).Parse(ctx, file)
	case ".docx":
		doc, err = NewDOCXSource(nil, nil, int64(len(attachment.Data)), maxDecompressedBytes).Parse(ctx, file)
	case ".epub":
		doc, err = NewEPUBSource(nil, nil, int64(len(attachment.Data)), maxDecompressedBytes).Parse(ctx, file)
	case ".org":
		doc, err = NewOrgSource(nil, nil, int64(len(attachment.Data))).Parse(ctx, file)
	case ".pdf":
		doc, err = NewPDFSource(nil, nil).Parse(ctx, file)
	default:
		if strings.HasPrefix(attachment.MediaType, "text/") && utf8.Valid(attachment.Data) && bytes.IndexByte(attachment.Data, 0) < 0 {
			metadata["extraction_method"] = "plain_text"
			return strings.TrimSpace(string(attachment.Data)), metadata, nil
		}
		return "", nil, fmt.Errorf("unsupported attachment format %q", ext)
	}
	if err != nil {
		return "", nil, err
	}
	for key, value := range doc.Metadata {
		metadata[key] = value
	}
	return doc.Content, metadata, nil
}

// extractBodyAndAttachments extracts plain text and attachment names from an email body.
func extractBodyAndAttachments(msg *mail.Message) (string, []string) {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Fall back to reading body directly.
		body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20)) // 1MB limit
		return string(body), nil
	}

	if strings.HasPrefix(mediaType, "text/plain") {
		body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
		return string(body), nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
			return string(body), nil
		}
		return extractMultipartTextAndAttachments(msg.Body, boundary)
	}

	// For HTML-only or other types, read raw.
	body, _ := io.ReadAll(io.LimitReader(msg.Body, 1<<20))
	return stripHTML(string(body)), nil
}

// extractMultipartTextAndAttachments extracts text/plain parts and attachment names.
func extractMultipartTextAndAttachments(r io.Reader, boundary string) (string, []string) {
	mr := multipart.NewReader(r, boundary)
	var textParts []string
	attachments := make(map[string]struct{})

	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		ct := part.Header.Get("Content-Type")
		mediaType, _, _ := mime.ParseMediaType(ct)

		filename := strings.TrimSpace(part.FileName())
		if filename != "" {
			attachments[filename] = struct{}{}
		}

		if strings.HasPrefix(mediaType, "text/plain") {
			body, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			textParts = append(textParts, string(body))
		}
	}

	var attachmentNames []string
	for name := range attachments {
		attachmentNames = append(attachmentNames, name)
	}
	sort.Strings(attachmentNames)

	if len(textParts) > 0 {
		return strings.Join(textParts, "\n\n"), attachmentNames
	}
	return "", attachmentNames
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
func buildEmailDocument(file FileInfo, messages []emailMessage, maskSensitivePreview bool) *storage.Document {
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
	metadata["format"] = strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Path)), ".")
	if metadata["format"] == "" {
		metadata["format"] = "maildir"
	}
	metadata["original_path"] = file.Path
	metadata["location"] = "message"
	metadata["extraction_method"] = "mime_text"
	attachments := make(map[string]struct{})

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
		for _, name := range msg.Attachments {
			name = strings.TrimSpace(name)
			if name != "" {
				attachments[name] = struct{}{}
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
	if len(attachments) > 0 {
		var names []string
		for name := range attachments {
			names = append(names, name)
		}
		sort.Strings(names)
		metadata["attachments"] = strings.Join(names, ", ")
	}

	// When masking is enabled, mask the full body too — not just the preview —
	// so sensitive data (addresses, tokens) is not stored in the index.
	if maskSensitivePreview {
		content = maskSensitiveText(content)
		metadata["from"] = maskEmailMetadata(metadata["from"])
		metadata["to"] = maskEmailMetadata(metadata["to"])
	}
	preview := generatePreview(content, 500)

	return &storage.Document{
		ID:          hashPath(file.Path),
		Source:      storage.SourceEmail,
		Path:        file.Path,
		Title:       title,
		Content:     content,
		Preview:     preview,
		Metadata:    metadata,
		ContentHash: hashContent(content),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}
}

func (e *EmailSource) shouldIgnorePath(path string) bool {
	if len(e.ignore) == 0 {
		return false
	}
	lowerPath := strings.ToLower(normalizePath(path))
	for _, pattern := range e.ignore {
		pattern = strings.TrimSpace(strings.ToLower(pattern))
		if pattern == "" {
			continue
		}
		pattern = strings.TrimPrefix(pattern, "./")
		if strings.Contains(lowerPath, pattern) {
			return true
		}
	}
	return false
}

var (
	emailRe       = regexp.MustCompile(`(?i)([a-z0-9._%+\-])([a-z0-9._%+\-]{0,64})@([a-z0-9.\-]+\.[a-z]{2,})`)
	longNumberRe  = regexp.MustCompile(`\b\d{13,19}\b`)
	bearerTokenRe = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._\-]{16,}`)
	apiKeyLikeRe  = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret)\s*[:=]\s*([^\s,;]+)`)
)

func maskSensitiveText(text string) string {
	text = emailRe.ReplaceAllStringFunc(text, func(m string) string {
		return maskEmailMetadata(m)
	})
	text = longNumberRe.ReplaceAllString(text, "[redacted-number]")
	text = bearerTokenRe.ReplaceAllString(text, "Bearer [redacted-token]")
	text = apiKeyLikeRe.ReplaceAllString(text, "$1=[redacted]")
	return text
}

func maskEmailMetadata(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	return emailRe.ReplaceAllString(value, "$1***@$3")
}

func hashPath(path string) string {
	h := sha256.Sum256([]byte(path))
	return hex.EncodeToString(h[:8])
}

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}
