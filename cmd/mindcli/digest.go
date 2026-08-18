package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/storage"
)

const (
	defaultDigestSince    = "7d"
	maxDigestDocuments    = 100
	maxDigestContextRunes = 1000
	maxDigestSummaryRunes = 32768
)

type digestAnswerer interface {
	GenerateAnswer(context.Context, string, []string) (string, error)
}

type digestItem struct {
	Document *storage.Document
	Activity time.Time
	Reasons  []string
}

type digestReport struct {
	Collection string
	After      time.Time
	Before     time.Time
	Items      []digestItem
	Summary    string
	Generated  bool
}

func runDigest(args []string) error {
	fs := flag.NewFlagSet("digest", flag.ContinueOnError)
	since := fs.String("since", defaultDigestSince, "Lookback such as 7d, 2w, 24h, YYYY-MM-DD, or RFC3339")
	collectionName := fs.String("collection", "", "Limit activity to a collection")
	output := fs.String("output", "", "Write private Markdown to a file (default: stdout)")
	limit := fs.Int("limit", 50, "Maximum documents (1-100)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: mindcli digest [--since 7d] [--collection NAME] [--limit N] [--output digest.md]")
	}
	if *limit < 1 || *limit > maxDigestDocuments {
		return fmt.Errorf("digest limit must be between 1 and %d", maxDigestDocuments)
	}
	before := time.Now().UTC()
	after, err := parseDigestSince(*since, before)
	if err != nil {
		return err
	}

	s, err := openStores(openOpts{vectors: true, embedder: true, llm: true, hybrid: true})
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()
	report, collection, currentIDs, err := buildDigestReport(ctx, s, strings.TrimSpace(*collectionName), after, before, *limit)
	if err != nil {
		return err
	}
	if s.llm != nil && len(report.Items) > 0 {
		summary, summaryErr := generateDigestSummary(ctx, s.llm, report)
		if summaryErr != nil {
			fmt.Fprintf(os.Stderr, "warning: digest synthesis unavailable; using deterministic summary: %v\n", summaryErr)
		} else {
			report.Summary = truncateDigestRunes(summary, maxDigestSummaryRunes)
			report.Generated = true
		}
	}
	if report.Summary == "" {
		report.Summary = deterministicDigestSummary(report)
	}

	writer, closeWriter, err := privateOutputWriter(*output)
	if err != nil {
		return err
	}
	if err := writeDigestMarkdown(writer, report, buildRedactor(s.cfg)); err != nil {
		if closeWriter != nil {
			_ = closeWriter()
		}
		return fmt.Errorf("writing digest: %w", err)
	}
	if closeWriter != nil {
		if err := closeWriter(); err != nil {
			return fmt.Errorf("closing digest: %w", err)
		}
	}
	if collection != nil {
		if err := s.db.MarkCollectionViewed(ctx, collection.ID, currentIDs, before); err != nil {
			return fmt.Errorf("recording collection digest view: %w", err)
		}
	}
	return nil
}

func parseDigestSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultDigestSince
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		if !parsed.Before(now) {
			return time.Time{}, errorsSinceBeforeNow(value)
		}
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		if !parsed.Before(now) {
			return time.Time{}, errorsSinceBeforeNow(value)
		}
		return parsed.UTC(), nil
	}
	lookback := strings.ToLower(value)
	if len(lookback) < 2 {
		return time.Time{}, fmt.Errorf("invalid --since %q: use 7d, 2w, 24h, YYYY-MM-DD, or RFC3339", value)
	}
	amount, err := strconv.Atoi(lookback[:len(lookback)-1])
	if err != nil || amount < 1 {
		return time.Time{}, fmt.Errorf("invalid --since %q: use 7d, 2w, 24h, YYYY-MM-DD, or RFC3339", value)
	}
	var duration time.Duration
	switch lookback[len(lookback)-1] {
	case 'h':
		if amount > 10*365*24 {
			return time.Time{}, fmt.Errorf("--since lookback must not exceed 10 years")
		}
		duration = time.Duration(amount) * time.Hour
	case 'd':
		if amount > 10*365 {
			return time.Time{}, fmt.Errorf("--since lookback must not exceed 10 years")
		}
		duration = time.Duration(amount) * 24 * time.Hour
	case 'w':
		if amount > 520 {
			return time.Time{}, fmt.Errorf("--since lookback must not exceed 10 years")
		}
		duration = time.Duration(amount) * 7 * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("invalid --since %q: use 7d, 2w, 24h, YYYY-MM-DD, or RFC3339", value)
	}
	return now.Add(-duration), nil
}

func errorsSinceBeforeNow(value string) error {
	return fmt.Errorf("--since %q must resolve before now", value)
}

func buildDigestReport(ctx context.Context, s *stores, collectionName string, after, before time.Time, limit int) (digestReport, *storage.Collection, []string, error) {
	report := digestReport{Collection: collectionName, After: after, Before: before}
	if collectionName == "" {
		documents, err := s.db.ListRecentDocuments(ctx, after, before, limit)
		if err != nil {
			return report, nil, nil, err
		}
		for _, document := range documents {
			report.Items = append(report.Items, digestItemForDocument(document, after, before, false))
		}
		return report, nil, nil, nil
	}

	collection, err := s.db.GetCollectionByName(ctx, collectionName)
	if err != nil {
		return report, nil, nil, fmt.Errorf("collection %q not found", collectionName)
	}
	documentSet, err := loadCollectionDocumentSet(ctx, s, collection)
	if err != nil {
		return report, nil, nil, err
	}
	currentIDs := collectionDocumentIDs(documentSet.All)
	newIDs := make(map[string]bool)
	if strings.TrimSpace(collection.Query) != "" {
		unseen, err := s.db.FilterUnseenCollectionDocumentIDs(ctx, collection.ID, currentIDs)
		if err != nil {
			return report, nil, nil, err
		}
		for _, id := range unseen {
			newIDs[id] = true
		}
	} else {
		added, err := s.db.ListCollectionDocumentIDsAddedAfter(ctx, collection.ID, after)
		if err != nil {
			return report, nil, nil, err
		}
		for _, id := range added {
			newIDs[id] = true
		}
	}
	for _, document := range documentSet.All {
		activity := latestDocumentActivity(document)
		isActive := !activity.Before(after) && activity.Before(before)
		if !isActive && !newIDs[document.ID] {
			continue
		}
		item := digestItemForDocument(document, after, before, newIDs[document.ID])
		if newIDs[document.ID] && item.Activity.Before(after) {
			item.Activity = after
		}
		report.Items = append(report.Items, item)
	}
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Activity.Equal(report.Items[j].Activity) {
			return strings.ToLower(report.Items[i].Document.Title) < strings.ToLower(report.Items[j].Document.Title)
		}
		return report.Items[i].Activity.After(report.Items[j].Activity)
	})
	if len(report.Items) > limit {
		report.Items = report.Items[:limit]
	}
	return report, collection, currentIDs, nil
}

func digestItemForDocument(document *storage.Document, after, before time.Time, isNew bool) digestItem {
	item := digestItem{Document: document, Activity: latestDocumentActivity(document)}
	if isNew {
		item.Reasons = append(item.Reasons, "new in collection")
	}
	if !document.IndexedAt.Before(after) && document.IndexedAt.Before(before) {
		item.Reasons = append(item.Reasons, "indexed")
	}
	if !document.ModifiedAt.Before(after) && document.ModifiedAt.Before(before) {
		item.Reasons = append(item.Reasons, "modified")
	}
	if len(item.Reasons) == 0 {
		item.Reasons = append(item.Reasons, "recent activity")
	}
	return item
}

func latestDocumentActivity(document *storage.Document) time.Time {
	if document.IndexedAt.After(document.ModifiedAt) {
		return document.IndexedAt
	}
	return document.ModifiedAt
}

func generateDigestSummary(ctx context.Context, answerer digestAnswerer, report digestReport) (string, error) {
	contexts := make([]string, 0, len(report.Items))
	for _, item := range report.Items {
		content := truncateDigestRunes(item.Document.Content, maxDigestContextRunes)
		contexts = append(contexts, fmt.Sprintf("%s\nActivity: %s\n%s", item.Document.Title, strings.Join(item.Reasons, ", "), content))
	}
	scope := "the knowledge base"
	if report.Collection != "" {
		scope = fmt.Sprintf("collection %q", report.Collection)
	}
	question := fmt.Sprintf("Summarize the important new or changed information in %s since %s. Distinguish source facts from inference and cite sources as [1], [2], etc.", scope, report.After.Format(time.RFC3339))
	return answerer.GenerateAnswer(ctx, question, contexts)
}

func deterministicDigestSummary(report digestReport) string {
	if len(report.Items) == 0 {
		return "No new or changed documents were found in this time range."
	}
	newCount, modifiedCount, indexedCount := 0, 0, 0
	for _, item := range report.Items {
		for _, reason := range item.Reasons {
			switch reason {
			case "new in collection":
				newCount++
			case "modified":
				modifiedCount++
			case "indexed":
				indexedCount++
			}
		}
	}
	return fmt.Sprintf("%d documents had activity: %d new to the collection, %d modified, and %d indexed.", len(report.Items), newCount, modifiedCount, indexedCount)
}

func writeDigestMarkdown(w io.Writer, report digestReport, redactor privacy.Redactor) error {
	redact := redactor.Redact
	title := "MindCLI activity digest"
	if report.Collection != "" {
		title = "Collection digest: " + redact(report.Collection)
	}
	if _, err := fmt.Fprintf(w, "# %s\n\n", title); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "- **From:** %s\n- **To:** %s\n- **Documents:** %d\n\n", report.After.Format(time.RFC3339), report.Before.Format(time.RFC3339), len(report.Items)); err != nil {
		return err
	}
	if report.Generated {
		if _, err := fmt.Fprint(w, "## Generated summary\n\n> This synthesis is generated from the first five bounded document excerpts listed below.\n\n"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprint(w, "## Activity summary\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\n\n## Documents and citations\n", redact(report.Summary)); err != nil {
		return err
	}
	if len(report.Items) == 0 {
		_, err := fmt.Fprintln(w, "\nNo documents matched the activity window.")
		return err
	}
	for index, item := range report.Items {
		document := item.Document
		if _, err := fmt.Fprintf(w, "\n### [%d] %s\n\n", index+1, redact(document.Title)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "- **ID:** `%s`\n- **Source:** %s\n- **Path:** `%s`\n- **Activity:** %s (%s)\n", document.ID, document.Source, redact(document.Path), item.Activity.Format(time.RFC3339), strings.Join(item.Reasons, ", ")); err != nil {
			return err
		}
		if tags := document.TagsString(); tags != "" {
			if _, err := fmt.Fprintf(w, "- **Tags:** %s\n", redact(tags)); err != nil {
				return err
			}
		}
		preview := strings.TrimSpace(document.Preview)
		if preview == "" {
			preview = truncateDigestRunes(strings.TrimSpace(document.Content), maxDigestContextRunes)
		}
		if _, err := fmt.Fprintf(w, "\n%s\n", redact(preview)); err != nil {
			return err
		}
	}
	return nil
}

func truncateDigestRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
