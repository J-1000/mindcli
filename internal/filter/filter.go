// Package filter defines the typed, transport-independent filters shared by
// query parsing, CLI/TUI search, smart collections, and protocol adapters.
package filter

import (
	"fmt"
	"strings"
	"time"

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
