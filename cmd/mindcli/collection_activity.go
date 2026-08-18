package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/query"
	"github.com/J-1000/mindcli/internal/storage"
)

type collectionDocumentSet struct {
	Members []*storage.Document
	Matches []*storage.Document
	All     []*storage.Document
}

func loadCollectionDocumentSet(ctx context.Context, s *stores, collection *storage.Collection) (collectionDocumentSet, error) {
	members, err := s.db.GetCollectionDocuments(ctx, collection.ID)
	if err != nil {
		return collectionDocumentSet{}, err
	}
	set := collectionDocumentSet{Members: members}
	seen := make(map[string]bool, len(members))
	for _, document := range members {
		if document != nil && !seen[document.ID] {
			seen[document.ID] = true
			set.All = append(set.All, document)
		}
	}
	if strings.TrimSpace(collection.Query) == "" {
		return set, nil
	}
	parsed, err := query.ParseQueryStrict(collection.Query)
	if err != nil {
		return collectionDocumentSet{}, fmt.Errorf("invalid saved query for collection %q: %w", collection.Name, err)
	}
	results, err := searchResults(ctx, s, parsed, s.cfg.Search.ResultsLimit)
	if err != nil {
		return collectionDocumentSet{}, fmt.Errorf("searching collection %q: %w", collection.Name, err)
	}
	for _, result := range results {
		document := result.Document
		if document == nil || seen[document.ID] {
			continue
		}
		seen[document.ID] = true
		set.Matches = append(set.Matches, document)
		set.All = append(set.All, document)
	}
	return set, nil
}

func collectionDocumentIDs(documents []*storage.Document) []string {
	ids := make([]string, 0, len(documents))
	for _, document := range documents {
		if document != nil {
			ids = append(ids, document.ID)
		}
	}
	return ids
}

func collectionUnseenCount(ctx context.Context, db *storage.DB, collection *storage.Collection, currentIDs []string) (int, error) {
	if strings.TrimSpace(collection.Query) != "" {
		unseen, err := db.FilterUnseenCollectionDocumentIDs(ctx, collection.ID, currentIDs)
		return len(unseen), err
	}
	after := time.Time{}
	if collection.LastViewedAt != nil {
		after = *collection.LastViewedAt
	}
	return db.CountCollectionDocumentsAddedAfter(ctx, collection.ID, after)
}
