package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

type exportDoc struct {
	Title      string            `json:"title"`
	Path       string            `json:"path"`
	Source     string            `json:"source"`
	Preview    string            `json:"preview"`
	Score      float64           `json:"score"`
	Tags       string            `json:"tags,omitempty"`
	ModifiedAt string            `json:"modified_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

func exportJSON(w io.Writer, results storage.SearchResults) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	docs := make([]exportDoc, 0, len(results))
	for _, r := range results {
		docs = append(docs, toExportDoc(r))
	}
	return enc.Encode(docs)
}

func exportCSV(w io.Writer, results storage.SearchResults) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{"title", "path", "source", "score", "tags", "modified_at"})
	for _, r := range results {
		cw.Write([]string{
			r.Document.Title,
			r.Document.Path,
			string(r.Document.Source),
			fmt.Sprintf("%.4f", r.Score),
			r.Document.Metadata["tags"],
			r.Document.ModifiedAt.Format(time.RFC3339),
		})
	}
	return cw.Error()
}

func exportMarkdown(w io.Writer, results storage.SearchResults) error {
	for i, r := range results {
		fmt.Fprintf(w, "## %d. %s\n\n", i+1, r.Document.Title)
		fmt.Fprintf(w, "- **Source:** %s\n", r.Document.Source)
		fmt.Fprintf(w, "- **Path:** %s\n", r.Document.Path)
		fmt.Fprintf(w, "- **Score:** %.4f\n", r.Score)
		if tags := r.Document.Metadata["tags"]; tags != "" {
			fmt.Fprintf(w, "- **Tags:** %s\n", tags)
		}
		fmt.Fprintf(w, "\n%s\n\n---\n\n", r.Document.Preview)
	}
	return nil
}

func toExportDoc(r *storage.SearchResult) exportDoc {
	return exportDoc{
		Title:      r.Document.Title,
		Path:       r.Document.Path,
		Source:     string(r.Document.Source),
		Preview:    r.Document.Preview,
		Score:      r.Score,
		Tags:       r.Document.Metadata["tags"],
		ModifiedAt: r.Document.ModifiedAt.Format(time.RFC3339),
		Metadata:   r.Document.Metadata,
	}
}
