// Package mcpserver exposes MindCLI's read-only knowledge operations through
// bounded, redacted transport-neutral methods and an MCP adapter.
package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/query"
	"github.com/J-1000/mindcli/internal/storage"
)

const (
	DefaultResultLimit       = 10
	MaxResultLimit           = 50
	MaxQueryBytes            = 4096
	MaxFilterValues          = 50
	MaxFilterValueBytes      = 512
	MaxDocumentContentBytes  = 20 * 1024
	MaxTitleBytes            = 4096
	MaxPathBytes             = 8192
	MaxPreviewBytes          = 1024
	MaxMetadataFields        = 50
	MaxMetadataValueBytes    = 1024
	MaxAnswerBytes           = 32 * 1024
	MaxAskSources            = 5
	MaxAskContextBytesPerDoc = 4096
)

// Searcher is the read-only retrieval behavior required by the MCP service.
type Searcher interface {
	SearchParsed(context.Context, query.ParsedQuery, int) (storage.SearchResults, error)
	Related(context.Context, string, int) ([]query.RelatedResult, error)
}

// AnswerGenerator is the non-streaming LLM behavior used by the ask tool.
type AnswerGenerator interface {
	GenerateAnswer(context.Context, string, []string) (string, error)
}

// Service implements the bounded read-only operations exposed as MCP tools.
type Service struct {
	db       *storage.DB
	searcher Searcher
	llm      AnswerGenerator
	redactor privacy.Redactor
	now      func() time.Time
}

// NewService creates a read-only service over an existing MindCLI index.
func NewService(db *storage.DB, searcher Searcher, llm AnswerGenerator, redactor privacy.Redactor) *Service {
	return &Service{db: db, searcher: searcher, llm: llm, redactor: redactor, now: time.Now}
}

// FilterInput is the typed MCP representation of MindCLI's structured filters.
type FilterInput struct {
	Sources       []string `json:"sources,omitempty" jsonschema:"document sources: markdown, pdf, email, browser, or clipboard"`
	Tags          []string `json:"tags,omitempty" jsonschema:"tags every result must contain"`
	ExcludedTags  []string `json:"excluded_tags,omitempty" jsonschema:"tags results must not contain"`
	Collections   []string `json:"collections,omitempty" jsonschema:"collection names; multiple names form a union"`
	After         string   `json:"after,omitempty" jsonschema:"inclusive YYYY-MM-DD or RFC3339 timestamp"`
	Before        string   `json:"before,omitempty" jsonschema:"exclusive YYYY-MM-DD or RFC3339 timestamp"`
	Paths         []string `json:"paths,omitempty" jsonschema:"case-insensitive path fragments every result must contain"`
	Domains       []string `json:"domains,omitempty" jsonschema:"domains or parent domains to match"`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"source-specific record kinds such as bookmark"`
	ExactPhrases  []string `json:"exact_phrases,omitempty" jsonschema:"exact phrases every result must contain"`
	ExcludedTerms []string `json:"excluded_terms,omitempty" jsonschema:"words or phrases results must not contain"`
	RelativeTime  string   `json:"relative_time,omitempty" jsonschema:"today, yesterday, this week, last week, this month, last month, or last year"`
}

type SearchInput struct {
	Query   string      `json:"query,omitempty" jsonschema:"search text, optionally including MindCLI structured query syntax"`
	Limit   int         `json:"limit,omitempty" jsonschema:"maximum results from 1 to 50"`
	Filters FilterInput `json:"filters,omitempty" jsonschema:"typed filters combined with filters in query"`
}

type GetDocumentInput struct {
	ID              string `json:"id" jsonschema:"stable document ID"`
	MaxContentBytes int    `json:"max_content_bytes,omitempty" jsonschema:"content bound from 1 to 20480 bytes"`
}

type ShowCollectionInput struct {
	Name  string `json:"name" jsonschema:"collection name"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum documents from 1 to 50"`
}

type RecentDocumentsInput struct {
	Since  string `json:"since,omitempty" jsonschema:"lookback such as 7d, 2w, or 24h, or an inclusive timestamp"`
	Before string `json:"before,omitempty" jsonschema:"exclusive YYYY-MM-DD or RFC3339 timestamp; defaults to now"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum documents from 1 to 50"`
}

type RelatedDocumentsInput struct {
	ID    string `json:"id" jsonschema:"stable source document ID"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum related documents from 1 to 50"`
}

type AskInput struct {
	Question string      `json:"question" jsonschema:"question to answer from the local knowledge index"`
	Limit    int         `json:"limit,omitempty" jsonschema:"maximum retrieved documents from 1 to 50"`
	Filters  FilterInput `json:"filters,omitempty" jsonschema:"typed filters applied before answer generation"`
}

type DocumentOutput struct {
	ID         string            `json:"id"`
	Source     string            `json:"source"`
	Path       string            `json:"path"`
	Title      string            `json:"title"`
	Content    string            `json:"content,omitempty"`
	Preview    string            `json:"preview,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	IndexedAt  time.Time         `json:"indexed_at"`
	ModifiedAt time.Time         `json:"modified_at"`
	Truncated  bool              `json:"truncated,omitempty"`
}

type SearchResultOutput struct {
	Document   DocumentOutput `json:"document"`
	Score      float64        `json:"score"`
	Highlights []string       `json:"highlights,omitempty"`
}

type SearchOutput struct {
	Results       []SearchResultOutput `json:"results"`
	ActiveFilters []string             `json:"active_filters,omitempty"`
}

type CollectionOutput struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Query         string    `json:"query,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	DocumentCount int       `json:"document_count,omitempty"`
}

type ListCollectionsOutput struct {
	Collections []CollectionOutput `json:"collections"`
}

type ShowCollectionOutput struct {
	Collection CollectionOutput `json:"collection"`
	Documents  []DocumentOutput `json:"documents"`
}

type RecentDocumentsOutput struct {
	After     time.Time        `json:"after"`
	Before    time.Time        `json:"before"`
	Documents []DocumentOutput `json:"documents"`
}

type RelatedResultOutput struct {
	Document DocumentOutput         `json:"document"`
	Score    float64                `json:"score"`
	Reasons  []query.RelationReason `json:"reasons"`
}

type RelatedDocumentsOutput struct {
	Source  DocumentOutput        `json:"source"`
	Results []RelatedResultOutput `json:"results"`
}

type AskOutput struct {
	Answer     string                 `json:"answer"`
	Confidence query.AnswerConfidence `json:"confidence"`
	Citations  []DocumentOutput       `json:"citations"`
}

func (s *Service) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	if s == nil || s.searcher == nil {
		return SearchOutput{}, fmt.Errorf("search is unavailable")
	}
	parsed, err := parseSearchInput(input.Query, input.Filters)
	if err != nil {
		return SearchOutput{}, err
	}
	limit, err := boundedLimit(input.Limit)
	if err != nil {
		return SearchOutput{}, err
	}
	results, err := s.searcher.SearchParsed(ctx, parsed, limit)
	if err != nil {
		return SearchOutput{}, err
	}
	output := SearchOutput{Results: make([]SearchResultOutput, 0, len(results)), ActiveFilters: redactStrings(s.redactor, parsed.Filters.Labels(), MaxFilterValueBytes)}
	for _, result := range results {
		highlights := redactStrings(s.redactor, result.Highlights, MaxPreviewBytes)
		output.Results = append(output.Results, SearchResultOutput{
			Document: s.documentOutput(result.Document, false, MaxDocumentContentBytes),
			Score:    result.Score, Highlights: highlights,
		})
	}
	return output, nil
}

func (s *Service) GetDocument(ctx context.Context, input GetDocumentInput) (DocumentOutput, error) {
	if strings.TrimSpace(input.ID) == "" {
		return DocumentOutput{}, fmt.Errorf("id is required")
	}
	maxBytes := input.MaxContentBytes
	if maxBytes == 0 {
		maxBytes = MaxDocumentContentBytes
	}
	if maxBytes < 1 || maxBytes > MaxDocumentContentBytes {
		return DocumentOutput{}, fmt.Errorf("max_content_bytes must be between 1 and %d", MaxDocumentContentBytes)
	}
	doc, err := s.db.GetDocument(ctx, input.ID)
	if err != nil {
		return DocumentOutput{}, fmt.Errorf("document %q not found", input.ID)
	}
	return s.documentOutput(doc, true, maxBytes), nil
}

func (s *Service) ListCollections(ctx context.Context) (ListCollectionsOutput, error) {
	collections, err := s.db.ListCollections(ctx)
	if err != nil {
		return ListCollectionsOutput{}, err
	}
	if len(collections) > MaxResultLimit {
		collections = collections[:MaxResultLimit]
	}
	output := ListCollectionsOutput{Collections: make([]CollectionOutput, 0, len(collections))}
	for _, collection := range collections {
		count, err := s.db.CountCollectionDocuments(ctx, collection.ID)
		if err != nil {
			return ListCollectionsOutput{}, err
		}
		output.Collections = append(output.Collections, s.collectionOutput(collection, count))
	}
	return output, nil
}

func (s *Service) ShowCollection(ctx context.Context, input ShowCollectionInput) (ShowCollectionOutput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ShowCollectionOutput{}, fmt.Errorf("name is required")
	}
	limit, err := boundedLimit(input.Limit)
	if err != nil {
		return ShowCollectionOutput{}, err
	}
	collection, err := s.db.GetCollectionByName(ctx, name)
	if err != nil {
		return ShowCollectionOutput{}, fmt.Errorf("collection %q not found", name)
	}
	members, err := s.db.GetCollectionDocuments(ctx, collection.ID)
	if err != nil {
		return ShowCollectionOutput{}, err
	}

	documents := make([]*storage.Document, 0, limit)
	seen := make(map[string]struct{})
	appendDocument := func(doc *storage.Document) {
		if len(documents) >= limit {
			return
		}
		if _, exists := seen[doc.ID]; exists {
			return
		}
		seen[doc.ID] = struct{}{}
		documents = append(documents, doc)
	}
	for _, doc := range members {
		appendDocument(doc)
	}
	if strings.TrimSpace(collection.Query) != "" && len(documents) < limit {
		parsed, err := query.ParseQueryStrict(collection.Query)
		if err != nil {
			return ShowCollectionOutput{}, fmt.Errorf("invalid saved query for collection %q: %w", name, err)
		}
		matches, err := s.searcher.SearchParsed(ctx, parsed, limit)
		if err != nil {
			return ShowCollectionOutput{}, err
		}
		for _, result := range matches {
			appendDocument(result.Document)
		}
	}

	output := ShowCollectionOutput{Collection: s.collectionOutput(collection, len(documents)), Documents: make([]DocumentOutput, 0, len(documents))}
	for _, doc := range documents {
		output.Documents = append(output.Documents, s.documentOutput(doc, false, MaxDocumentContentBytes))
	}
	return output, nil
}

func (s *Service) RecentDocuments(ctx context.Context, input RecentDocumentsInput) (RecentDocumentsOutput, error) {
	limit, err := boundedLimit(input.Limit)
	if err != nil {
		return RecentDocumentsOutput{}, err
	}
	before := s.now().UTC()
	if strings.TrimSpace(input.Before) != "" {
		before, err = parseDate(input.Before)
		if err != nil {
			return RecentDocumentsOutput{}, fmt.Errorf("invalid before value %q: use YYYY-MM-DD or RFC3339", input.Before)
		}
	}
	since := strings.TrimSpace(input.Since)
	if since == "" {
		since = "7d"
	}
	after, err := parseSince(since, before)
	if err != nil {
		return RecentDocumentsOutput{}, err
	}
	if !after.Before(before) {
		return RecentDocumentsOutput{}, fmt.Errorf("since must resolve before the before bound")
	}
	docs, err := s.db.ListRecentDocuments(ctx, after, before, limit)
	if err != nil {
		return RecentDocumentsOutput{}, err
	}
	output := RecentDocumentsOutput{After: after, Before: before, Documents: make([]DocumentOutput, 0, len(docs))}
	for _, doc := range docs {
		output.Documents = append(output.Documents, s.documentOutput(doc, false, MaxDocumentContentBytes))
	}
	return output, nil
}

func (s *Service) RelatedDocuments(ctx context.Context, input RelatedDocumentsInput) (RelatedDocumentsOutput, error) {
	if strings.TrimSpace(input.ID) == "" {
		return RelatedDocumentsOutput{}, fmt.Errorf("id is required")
	}
	limit, err := boundedLimit(input.Limit)
	if err != nil {
		return RelatedDocumentsOutput{}, err
	}
	source, err := s.db.GetDocument(ctx, input.ID)
	if err != nil {
		return RelatedDocumentsOutput{}, fmt.Errorf("document %q not found", input.ID)
	}
	results, err := s.searcher.Related(ctx, source.ID, limit)
	if err != nil {
		return RelatedDocumentsOutput{}, err
	}
	output := RelatedDocumentsOutput{Source: s.documentOutput(source, false, MaxDocumentContentBytes), Results: make([]RelatedResultOutput, 0, len(results))}
	for _, result := range results {
		reasons := make([]query.RelationReason, len(result.Reasons))
		for index, reason := range result.Reasons {
			reason.Values = redactStrings(s.redactor, reason.Values, MaxFilterValueBytes)
			reasons[index] = reason
		}
		output.Results = append(output.Results, RelatedResultOutput{
			Document: s.documentOutput(result.Document, false, MaxDocumentContentBytes), Score: result.Score, Reasons: reasons,
		})
	}
	return output, nil
}

func (s *Service) Ask(ctx context.Context, input AskInput) (AskOutput, error) {
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return AskOutput{}, fmt.Errorf("question is required")
	}
	if len(question) > MaxQueryBytes {
		return AskOutput{}, fmt.Errorf("question exceeds %d bytes", MaxQueryBytes)
	}
	if s.llm == nil {
		return AskOutput{}, fmt.Errorf("answer generation is unavailable: no LLM provider is configured")
	}
	searchOutput, err := s.Search(ctx, SearchInput{Query: question, Limit: input.Limit, Filters: input.Filters})
	if err != nil {
		return AskOutput{}, err
	}
	if len(searchOutput.Results) == 0 {
		return AskOutput{Answer: "No relevant documents found.", Confidence: query.EstimateAnswerConfidence(question, nil)}, nil
	}

	contexts := make([]string, 0, MaxAskSources)
	citations := make([]DocumentOutput, 0, MaxAskSources)
	for index, result := range searchOutput.Results {
		if index >= MaxAskSources {
			break
		}
		doc, err := s.db.GetDocument(ctx, result.Document.ID)
		if err != nil {
			continue
		}
		contextText, _ := truncateUTF8(doc.Content, MaxAskContextBytesPerDoc)
		contexts = append(contexts, contextText)
		citations = append(citations, s.documentOutput(doc, false, MaxDocumentContentBytes))
	}
	answer, err := s.llm.GenerateAnswer(ctx, question, contexts)
	if err != nil {
		return AskOutput{}, err
	}
	answer, _ = truncateUTF8(s.redactor.Redact(answer), MaxAnswerBytes)
	return AskOutput{
		Answer: answer, Confidence: query.EstimateAnswerConfidence(question, contexts), Citations: citations,
	}, nil
}

func (s *Service) documentOutput(doc *storage.Document, includeContent bool, maxContentBytes int) DocumentOutput {
	if doc == nil {
		return DocumentOutput{}
	}
	preview, _ := truncateUTF8(s.redactor.Redact(doc.Preview), MaxPreviewBytes)
	output := DocumentOutput{
		ID: doc.ID, Source: string(doc.Source),
		Path:    boundedRedactedString(s.redactor, doc.Path, MaxPathBytes),
		Title:   boundedRedactedString(s.redactor, doc.Title, MaxTitleBytes),
		Preview: preview, Metadata: redactMetadata(s.redactor, doc.Metadata),
		IndexedAt: doc.IndexedAt, ModifiedAt: doc.ModifiedAt,
	}
	if includeContent {
		output.Content, output.Truncated = truncateUTF8(s.redactor.Redact(doc.Content), maxContentBytes)
	}
	return output
}

func (s *Service) collectionOutput(collection *storage.Collection, count int) CollectionOutput {
	return CollectionOutput{
		ID: collection.ID, Name: s.redactor.Redact(collection.Name),
		Description: s.redactor.Redact(collection.Description), Query: s.redactor.Redact(collection.Query),
		CreatedAt: collection.CreatedAt, DocumentCount: count,
	}
}

func boundedLimit(value int) (int, error) {
	if value == 0 {
		return DefaultResultLimit, nil
	}
	if value < 1 || value > MaxResultLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", MaxResultLimit)
	}
	return value, nil
}

func parseSearchInput(raw string, input FilterInput) (query.ParsedQuery, error) {
	if len(raw) > MaxQueryBytes {
		return query.ParsedQuery{}, fmt.Errorf("query exceeds %d bytes", MaxQueryBytes)
	}
	parsed, err := query.ParseQueryStrict(raw)
	if err != nil {
		return query.ParsedQuery{}, fmt.Errorf("invalid query: %w", err)
	}
	typed, err := input.toFilterSet()
	if err != nil {
		return query.ParsedQuery{}, err
	}
	parsed.Filters = mergeFilters(parsed.Filters, typed)
	if parsed.Filters.After != nil && parsed.Filters.Before != nil && !parsed.Filters.After.Before(*parsed.Filters.Before) {
		return query.ParsedQuery{}, fmt.Errorf("after must be earlier than before")
	}
	if strings.TrimSpace(parsed.Text) == "" && parsed.Filters.Empty() {
		return query.ParsedQuery{}, fmt.Errorf("query or filters are required")
	}
	return parsed, nil
}

func (input FilterInput) toFilterSet() (filter.Set, error) {
	if len(input.RelativeTime) > MaxFilterValueBytes {
		return filter.Set{}, fmt.Errorf("relative_time exceeds %d bytes", MaxFilterValueBytes)
	}
	groups := [][]string{input.Sources, input.Tags, input.ExcludedTags, input.Collections, input.Paths, input.Domains, input.Kinds, input.ExactPhrases, input.ExcludedTerms}
	for _, values := range groups {
		if len(values) > MaxFilterValues {
			return filter.Set{}, fmt.Errorf("a filter field exceeds %d values", MaxFilterValues)
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return filter.Set{}, fmt.Errorf("filter values must not be empty")
			}
			if len(value) > MaxFilterValueBytes {
				return filter.Set{}, fmt.Errorf("filter value exceeds %d bytes", MaxFilterValueBytes)
			}
		}
	}
	set := filter.Set{
		Tags: uniqueLower(input.Tags), ExcludedTags: uniqueLower(input.ExcludedTags),
		Collections: uniqueValues(input.Collections), PathPrefixes: uniqueValues(input.Paths),
		Domains: uniqueLower(input.Domains), Kinds: uniqueLower(input.Kinds),
		ExactPhrases: uniqueValues(input.ExactPhrases), ExcludedTerms: uniqueValues(input.ExcludedTerms),
	}
	for _, value := range input.Sources {
		source := storage.Source(strings.ToLower(strings.TrimSpace(value)))
		switch source {
		case storage.SourceMarkdown, storage.SourcePDF, storage.SourceEmail, storage.SourceBrowser, storage.SourceClipboard:
			set.Sources = appendUniqueSource(set.Sources, source)
		default:
			return filter.Set{}, fmt.Errorf("unknown source %q", value)
		}
	}
	var err error
	if strings.TrimSpace(input.After) != "" {
		value, parseErr := parseDate(input.After)
		if parseErr != nil {
			return filter.Set{}, fmt.Errorf("invalid after value %q: use YYYY-MM-DD or RFC3339", input.After)
		}
		set.After = &value
	}
	if strings.TrimSpace(input.Before) != "" {
		value, parseErr := parseDate(input.Before)
		if parseErr != nil {
			return filter.Set{}, fmt.Errorf("invalid before value %q: use YYYY-MM-DD or RFC3339", input.Before)
		}
		set.Before = &value
	}
	set.RelativeTime = strings.ToLower(strings.TrimSpace(input.RelativeTime))
	if set.RelativeTime != "" && !validRelativeTime(set.RelativeTime) {
		return filter.Set{}, fmt.Errorf("unknown relative_time %q", input.RelativeTime)
	}
	if set.After != nil && set.Before != nil && !set.After.Before(*set.Before) {
		err = fmt.Errorf("after must be earlier than before")
	}
	return set, err
}

func mergeFilters(base, extra filter.Set) filter.Set {
	base.Sources = appendUniqueSources(base.Sources, extra.Sources...)
	base.Tags = appendUniqueStrings(base.Tags, extra.Tags...)
	base.ExcludedTags = appendUniqueStrings(base.ExcludedTags, extra.ExcludedTags...)
	base.Collections = appendUniqueStrings(base.Collections, extra.Collections...)
	base.PathPrefixes = appendUniqueStrings(base.PathPrefixes, extra.PathPrefixes...)
	base.Domains = appendUniqueStrings(base.Domains, extra.Domains...)
	base.Kinds = appendUniqueStrings(base.Kinds, extra.Kinds...)
	base.ExactPhrases = appendUniqueStrings(base.ExactPhrases, extra.ExactPhrases...)
	base.ExcludedTerms = appendUniqueStrings(base.ExcludedTerms, extra.ExcludedTerms...)
	if extra.After != nil {
		base.After = extra.After
	}
	if extra.Before != nil {
		base.Before = extra.Before
	}
	if extra.RelativeTime != "" {
		base.RelativeTime = extra.RelativeTime
	}
	if base.After != nil || base.Before != nil {
		base.RelativeTime = ""
	}
	return base
}

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if len(value) == len("2006-01-02") {
		return time.Parse("2006-01-02", value)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseSince(value string, before time.Time) (time.Time, error) {
	if timestamp, err := parseDate(value); err == nil {
		return timestamp, nil
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	if len(lower) < 2 {
		return time.Time{}, fmt.Errorf("invalid since value %q: use a duration such as 7d or an RFC3339 timestamp", value)
	}
	unit := lower[len(lower)-1]
	count, err := strconv.Atoi(lower[:len(lower)-1])
	if err != nil || count <= 0 {
		return time.Time{}, fmt.Errorf("invalid since value %q: use a duration such as 7d or an RFC3339 timestamp", value)
	}
	switch unit {
	case 'h':
		if count > 24*3660 {
			return time.Time{}, fmt.Errorf("since duration is too large")
		}
		return before.Add(-time.Duration(count) * time.Hour), nil
	case 'd':
		if count > 3660 {
			return time.Time{}, fmt.Errorf("since duration is too large")
		}
		return before.AddDate(0, 0, -count), nil
	case 'w':
		if count > 520 {
			return time.Time{}, fmt.Errorf("since duration is too large")
		}
		return before.AddDate(0, 0, -7*count), nil
	default:
		return time.Time{}, fmt.Errorf("invalid since value %q: supported suffixes are h, d, and w", value)
	}
}

func validRelativeTime(value string) bool {
	switch value {
	case "today", "yesterday", "this week", "last week", "this month", "last month", "last year":
		return true
	default:
		return false
	}
}

func uniqueLower(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = appendUniqueStrings(result, strings.ToLower(strings.TrimSpace(value)))
	}
	return result
}

func uniqueValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = appendUniqueStrings(result, strings.TrimSpace(value))
	}
	return result
}

func appendUniqueStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if strings.EqualFold(value, addition) {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}

func appendUniqueSource(values []storage.Source, addition storage.Source) []storage.Source {
	for _, value := range values {
		if value == addition {
			return values
		}
	}
	return append(values, addition)
}

func appendUniqueSources(values []storage.Source, additions ...storage.Source) []storage.Source {
	for _, addition := range additions {
		values = appendUniqueSource(values, addition)
	}
	return values
}

func redactMetadata(redactor privacy.Redactor, metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > MaxMetadataFields {
		keys = keys[:MaxMetadataFields]
	}
	output := make(map[string]string, len(keys))
	for _, key := range keys {
		value, _ := truncateUTF8(redactor.Redact(metadata[key]), MaxMetadataValueBytes)
		output[key] = value
	}
	return output
}

func redactStrings(redactor privacy.Redactor, values []string, maxBytes int) []string {
	if len(values) == 0 {
		return nil
	}
	output := make([]string, 0, len(values))
	for _, value := range values {
		value, _ = truncateUTF8(redactor.Redact(value), maxBytes)
		output = append(output, value)
	}
	return output
}

func boundedRedactedString(redactor privacy.Redactor, value string, maxBytes int) string {
	value, _ = truncateUTF8(redactor.Redact(value), maxBytes)
	return value
}

func truncateUTF8(value string, maxBytes int) (string, bool) {
	if maxBytes < 0 || len(value) <= maxBytes {
		return value, false
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}
