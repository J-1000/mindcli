// Package filter defines the typed, transport-independent filters shared by
// query parsing, CLI/TUI search, smart collections, and protocol adapters.
package filter

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/J-1000/mindcli/internal/storage"
)

// Set contains explicit and convenience filters for one search. Values within
// Sources, Domains, and Kinds are alternatives; positive tags and path prefixes
// are cumulative. Before is exclusive and After is inclusive.
type Set struct {
	Sources       []storage.Source `json:"sources,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	ExcludedTags  []string         `json:"excluded_tags,omitempty"`
	Collections   []string         `json:"collections,omitempty"`
	After         *time.Time       `json:"after,omitempty"`
	Before        *time.Time       `json:"before,omitempty"`
	PathPrefixes  []string         `json:"path_prefixes,omitempty"`
	Domains       []string         `json:"domains,omitempty"`
	Kinds         []string         `json:"kinds,omitempty"`
	ExactPhrases  []string         `json:"exact_phrases,omitempty"`
	ExcludedTerms []string         `json:"excluded_terms,omitempty"`
	RelativeTime  string           `json:"relative_time,omitempty"`

	// DocumentIDs is populated internally after collection names are resolved.
	// It is deliberately omitted from serialized query surfaces.
	DocumentIDs []string `json:"-"`
}

// Empty reports whether no filters or exact/negative clauses are active.
func (s Set) Empty() bool {
	return len(s.Sources) == 0 && len(s.Tags) == 0 && len(s.ExcludedTags) == 0 &&
		len(s.Collections) == 0 && s.After == nil && s.Before == nil &&
		len(s.PathPrefixes) == 0 && len(s.Domains) == 0 && len(s.Kinds) == 0 &&
		len(s.ExactPhrases) == 0 && len(s.ExcludedTerms) == 0 && s.RelativeTime == "" &&
		len(s.DocumentIDs) == 0
}

// Labels returns concise deterministic labels suitable for CLI/TUI display.
func (s Set) Labels() []string {
	labels := make([]string, 0, 12)
	for _, source := range s.Sources {
		labels = append(labels, "source:"+string(source))
	}
	for _, tag := range s.Tags {
		labels = append(labels, "tag:"+tag)
	}
	for _, tag := range s.ExcludedTags {
		labels = append(labels, "-tag:"+tag)
	}
	for _, collection := range s.Collections {
		labels = append(labels, "collection:"+collection)
	}
	if s.After != nil {
		labels = append(labels, "after:"+s.After.Format("2006-01-02"))
	}
	if s.Before != nil {
		labels = append(labels, "before:"+s.Before.Format("2006-01-02"))
	}
	for _, path := range s.PathPrefixes {
		labels = append(labels, "path:"+path)
	}
	for _, domain := range s.Domains {
		labels = append(labels, "domain:"+domain)
	}
	for _, kind := range s.Kinds {
		labels = append(labels, "kind:"+kind)
	}
	if s.RelativeTime != "" {
		labels = append(labels, s.RelativeTime)
	}
	for _, phrase := range s.ExactPhrases {
		labels = append(labels, fmt.Sprintf("phrase:%q", phrase))
	}
	for _, term := range s.ExcludedTerms {
		labels = append(labels, "-"+term)
	}
	return labels
}

// String formats active filters for compact status output.
func (s Set) String() string {
	return strings.Join(s.Labels(), " ")
}

// ResolveRelativeTime converts a convenience time phrase into concrete bounds.
func ResolveRelativeTime(s Set, now time.Time) Set {
	if s.RelativeTime == "" || s.After != nil || s.Before != nil {
		return s
	}
	start, end, ok := relativeTimeRange(s.RelativeTime, now)
	if !ok {
		return s
	}
	s.After = &start
	s.Before = &end
	return s
}

// MatchesDocument applies all filters to a loaded document. It is used to
// enforce the same constraints on semantic results as on full-text results.
func MatchesDocument(doc *storage.Document, set Set, now time.Time) bool {
	if doc == nil {
		return false
	}
	set = ResolveRelativeTime(set, now)
	if len(set.DocumentIDs) > 0 && !containsFold(set.DocumentIDs, doc.ID) {
		return false
	}
	if set.DocumentIDs != nil && len(set.DocumentIDs) == 0 {
		return false
	}
	if len(set.Sources) > 0 {
		matched := false
		for _, source := range set.Sources {
			if doc.Source == source {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	tags := doc.Tags()
	for _, required := range set.Tags {
		if !containsFold(tags, required) {
			return false
		}
	}
	for _, excluded := range set.ExcludedTags {
		if containsFold(tags, excluded) {
			return false
		}
	}
	if set.After != nil && doc.ModifiedAt.Before(*set.After) {
		return false
	}
	if set.Before != nil && !doc.ModifiedAt.Before(*set.Before) {
		return false
	}
	lowerPath := strings.ToLower(filepath.ToSlash(doc.Path))
	for _, prefix := range set.PathPrefixes {
		if !strings.Contains(lowerPath, strings.ToLower(filepath.ToSlash(prefix))) {
			return false
		}
	}
	if len(set.Domains) > 0 && !containsDomain(set.Domains, DocumentDomain(doc)) {
		return false
	}
	if len(set.Kinds) > 0 {
		kinds := DocumentKinds(doc)
		matched := false
		for _, kind := range set.Kinds {
			if containsFold(kinds, kind) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	haystack := strings.ToLower(doc.Title + "\n" + doc.Content)
	for _, phrase := range set.ExactPhrases {
		if !strings.Contains(haystack, strings.ToLower(phrase)) {
			return false
		}
	}
	for _, term := range set.ExcludedTerms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			return false
		}
	}
	return true
}

// DocumentDomain returns normalized domain metadata for filtering/indexing.
func DocumentDomain(doc *storage.Document) string {
	if doc == nil {
		return ""
	}
	for _, key := range []string{"domain", "normalized_url", "url", "source_url"} {
		value := doc.Metadata[key]
		if value == "" {
			continue
		}
		if key == "domain" {
			return strings.ToLower(strings.TrimSuffix(value, "."))
		}
		if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
			return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		}
	}
	if parsed, err := url.Parse(doc.Path); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	}
	return ""
}

// DocumentDomains returns the host and its parent-domain suffixes so a filter
// for example.com also matches docs.example.com.
func DocumentDomains(doc *storage.Document) []string {
	host := DocumentDomain(doc)
	if host == "" {
		return nil
	}
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return []string{host}
	}
	parts := strings.Split(host, ".")
	domains := make([]string, 0, len(parts)-1)
	for index := 0; index < len(parts)-1; index++ {
		domains = append(domains, strings.Join(parts[index:], "."))
	}
	return domains
}

// DocumentKinds returns source-specific record kinds from metadata.
func DocumentKinds(doc *storage.Document) []string {
	if doc == nil {
		return nil
	}
	fields := strings.FieldsFunc(doc.Metadata["kind"], func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	kinds := make([]string, 0, len(fields))
	for _, value := range fields {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !containsFold(kinds, value) {
			kinds = append(kinds, value)
		}
	}
	return kinds
}

func relativeTimeRange(value string, now time.Time) (time.Time, time.Time, bool) {
	startOfDay := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
	startOfWeek := func(t time.Time) time.Time {
		day := startOfDay(t)
		return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	}
	firstOfMonth := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	}

	switch value {
	case "today":
		return startOfDay(now), now, true
	case "yesterday":
		end := startOfDay(now)
		return end.AddDate(0, 0, -1), end, true
	case "this week":
		return startOfWeek(now), now, true
	case "last week":
		end := startOfWeek(now)
		return end.AddDate(0, 0, -7), end, true
	case "this month":
		return firstOfMonth(now), now, true
	case "last month":
		end := firstOfMonth(now)
		return end.AddDate(0, -1, 0), end, true
	case "last year":
		return now.AddDate(-1, 0, 0), now, true
	default:
		return time.Time{}, time.Time{}, false
	}
}

func containsDomain(domains []string, actual string) bool {
	actual = strings.ToLower(strings.TrimSuffix(actual, "."))
	for _, domain := range domains {
		domain = strings.ToLower(strings.Trim(domain, "."))
		if actual == domain || strings.HasSuffix(actual, "."+domain) {
			return true
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
