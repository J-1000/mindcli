package query

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/storage"
)

// RelationKind identifies one independently observable reason that two
// documents are related.
type RelationKind string

const (
	RelationSemantic RelationKind = "semantic"
	RelationLexical  RelationKind = "lexical"
	RelationTags     RelationKind = "shared_tags"
	RelationLinks    RelationKind = "shared_links"
)

// RelationReason explains one signal contributing to a related-document rank.
type RelationReason struct {
	Kind   RelationKind `json:"kind"`
	Score  float64      `json:"score"`
	Values []string     `json:"values,omitempty"`
}

// Label returns a compact user-facing explanation of a relation signal.
func (r RelationReason) Label() string {
	switch r.Kind {
	case RelationSemantic:
		return fmt.Sprintf("semantic similarity %.0f%%", r.Score*100)
	case RelationLexical:
		return "lexical similarity"
	case RelationTags:
		return "shared tags: " + strings.Join(r.Values, ", ")
	case RelationLinks:
		if len(r.Values) == 1 {
			return "1 shared link"
		}
		return fmt.Sprintf("%d shared links", len(r.Values))
	default:
		return string(r.Kind)
	}
}

// RelatedResult contains a ranked document and the evidence for its relation
// to the selected source document.
type RelatedResult struct {
	Document *storage.Document `json:"document"`
	Score    float64           `json:"score"`
	Reasons  []RelationReason  `json:"reasons"`
}

// Related finds documents related to documentID. Semantic similarity is used
// when the vector store and embedder are available; lexical similarity, tags,
// and links remain available as a local fallback.
func (h *HybridSearcher) Related(ctx context.Context, documentID string, limit int) ([]RelatedResult, error) {
	if h == nil || h.db == nil {
		return nil, fmt.Errorf("related-document search is unavailable")
	}
	if limit <= 0 {
		return nil, nil
	}
	if limit > 100 {
		limit = 100
	}

	source, err := h.db.GetDocument(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("loading source document: %w", err)
	}
	documents, err := h.db.ListDocuments(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("loading related-document candidates: %w", err)
	}
	documentsByID := make(map[string]*storage.Document, len(documents))
	for _, doc := range documents {
		documentsByID[doc.ID] = doc
	}

	type evidence struct {
		semantic    float64
		lexical     float64
		sharedTags  []string
		sharedLinks []string
	}
	candidates := make(map[string]*evidence)
	evidenceFor := func(id string) *evidence {
		candidate := candidates[id]
		if candidate == nil {
			candidate = &evidence{}
			candidates[id] = candidate
		}
		return candidate
	}

	candidateLimit := relatedCandidateLimit(limit)
	if h.vectors != nil && h.embedder != nil && h.vectors.Len() > 0 {
		if embedding, embedErr := h.embedder.Embed(ctx, relatedSemanticText(source)); embedErr == nil {
			for _, result := range h.vectors.Search(embedding, candidateLimit) {
				id := extractDocID(result.Key)
				if id == source.ID || documentsByID[id] == nil || result.Similarity <= 0 {
					continue
				}
				candidate := evidenceFor(id)
				if result.Similarity > candidate.semantic {
					candidate.semantic = result.Similarity
				}
			}
		}
	}

	if h.bleve != nil {
		seed := relatedLexicalText(source)
		if seed != "" {
			results, searchErr := h.bleve.SearchFiltered(ctx, seed, filter.Set{}, candidateLimit)
			if searchErr != nil {
				return nil, fmt.Errorf("finding lexically related documents: %w", searchErr)
			}
			rank := 0
			for _, result := range results {
				if result.ID == source.ID || documentsByID[result.ID] == nil {
					continue
				}
				rank++
				evidenceFor(result.ID).lexical = 1 / float64(rank)
			}
		}
	}

	sourceTags := source.Tags()
	sourceLinks := documentLinks(source)
	for _, doc := range documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if doc.ID == source.ID {
			continue
		}
		sharedTags := sharedValues(sourceTags, doc.Tags())
		sharedLinks := sharedValues(sourceLinks, documentLinks(doc))
		if len(sharedTags) == 0 && len(sharedLinks) == 0 {
			continue
		}
		candidate := evidenceFor(doc.ID)
		candidate.sharedTags = sharedTags
		candidate.sharedLinks = sharedLinks
	}

	results := make([]RelatedResult, 0, len(candidates))
	for id, candidate := range candidates {
		doc := documentsByID[id]
		if doc == nil {
			continue
		}
		reasons := make([]RelationReason, 0, 4)
		score := 0.0
		if candidate.semantic > 0 {
			reasons = append(reasons, RelationReason{Kind: RelationSemantic, Score: candidate.semantic})
			score += 0.50 * candidate.semantic
		}
		if len(candidate.sharedTags) > 0 {
			tagScore := overlapScore(sourceTags, doc.Tags(), candidate.sharedTags)
			reasons = append(reasons, RelationReason{Kind: RelationTags, Score: tagScore, Values: candidate.sharedTags})
			score += 0.15 * tagScore
		}
		if len(candidate.sharedLinks) > 0 {
			linkScore := overlapScore(sourceLinks, documentLinks(doc), candidate.sharedLinks)
			reasons = append(reasons, RelationReason{Kind: RelationLinks, Score: linkScore, Values: candidate.sharedLinks})
			score += 0.10 * linkScore
		}
		if candidate.lexical > 0 {
			reasons = append(reasons, RelationReason{Kind: RelationLexical, Score: candidate.lexical})
			score += 0.25 * candidate.lexical
		}
		if score > 0 {
			results = append(results, RelatedResult{Document: doc, Score: score, Reasons: reasons})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if !results[i].Document.ModifiedAt.Equal(results[j].Document.ModifiedAt) {
			return results[i].Document.ModifiedAt.After(results[j].Document.ModifiedAt)
		}
		return results[i].Document.ID < results[j].Document.ID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func relatedCandidateLimit(limit int) int {
	candidates := limit * 10
	if candidates < 100 {
		candidates = 100
	}
	if candidates > 1000 {
		candidates = 1000
	}
	return candidates
}

func relatedSemanticText(doc *storage.Document) string {
	return truncateText(strings.TrimSpace(doc.Title+"\n"+doc.Content), 4000)
}

func relatedLexicalText(doc *storage.Document) string {
	parts := []string{doc.Title, doc.Metadata["headings"], strings.Join(doc.Tags(), " "), truncateText(doc.Content, 1200)}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func truncateText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

func documentLinks(doc *storage.Document) []string {
	if doc == nil || doc.Metadata == nil {
		return nil
	}
	values := strings.FieldsFunc(doc.Metadata["links"], func(r rune) bool {
		return r == ',' || r == '\n'
	})
	links := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !containsValueFold(links, value) {
			links = append(links, value)
		}
	}
	return links
}

func sharedValues(left, right []string) []string {
	shared := make([]string, 0, min(len(left), len(right)))
	for _, value := range left {
		if containsValueFold(right, value) && !containsValueFold(shared, value) {
			shared = append(shared, value)
		}
	}
	sort.Slice(shared, func(i, j int) bool {
		return strings.ToLower(shared[i]) < strings.ToLower(shared[j])
	})
	return shared
}

func containsValueFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func overlapScore(left, right, shared []string) float64 {
	union := len(left) + len(right) - len(shared)
	if union <= 0 {
		return 0
	}
	return float64(len(shared)) / float64(union)
}
