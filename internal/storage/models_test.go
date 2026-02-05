package storage

import (
	"sort"
	"testing"
)

func TestDocumentMetadataJSON(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		want     string
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			want:     "{}",
		},
		{
			name:     "empty metadata",
			metadata: map[string]string{},
			want:     "{}",
		},
		{
			name:     "single field",
			metadata: map[string]string{"key": "value"},
			want:     `{"key":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &Document{Metadata: tt.metadata}
			got := doc.MetadataJSON()
			if got != tt.want {
				t.Errorf("MetadataJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDocumentSetMetadataFromJSON(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantErr  bool
		wantNil  bool
		wantKey  string
		wantVal  string
	}{
		{
			name:    "empty string",
			json:    "",
			wantErr: false,
			wantNil: true,
		},
		{
			name:    "empty object",
			json:    "{}",
			wantErr: false,
			wantNil: true,
		},
		{
			name:    "valid json",
			json:    `{"author":"test"}`,
			wantErr: false,
			wantNil: false,
			wantKey: "author",
			wantVal: "test",
		},
		{
			name:    "invalid json",
			json:    "not json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &Document{}
			err := doc.SetMetadataFromJSON(tt.json)

			if (err != nil) != tt.wantErr {
				t.Errorf("SetMetadataFromJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if tt.wantNil {
				if doc.Metadata != nil {
					t.Errorf("Expected nil metadata, got %v", doc.Metadata)
				}
				return
			}

			if doc.Metadata[tt.wantKey] != tt.wantVal {
				t.Errorf("Metadata[%s] = %q, want %q", tt.wantKey, doc.Metadata[tt.wantKey], tt.wantVal)
			}
		})
	}
}

func TestSearchResultsSorting(t *testing.T) {
	results := SearchResults{
		{Score: 0.5},
		{Score: 0.9},
		{Score: 0.3},
		{Score: 0.7},
	}

	sort.Sort(results)

	// Should be in descending order
	expected := []float64{0.9, 0.7, 0.5, 0.3}
	for i, r := range results {
		if r.Score != expected[i] {
			t.Errorf("results[%d].Score = %f, want %f", i, r.Score, expected[i])
		}
	}
}

func TestSearchResultsLen(t *testing.T) {
	results := SearchResults{
		{Score: 0.1},
		{Score: 0.2},
		{Score: 0.3},
	}

	if results.Len() != 3 {
		t.Errorf("Len() = %d, want 3", results.Len())
	}
}

func TestSourceConstants(t *testing.T) {
	// Verify source constants have expected values
	if SourceMarkdown != "markdown" {
		t.Errorf("SourceMarkdown = %q, want 'markdown'", SourceMarkdown)
	}
	if SourcePDF != "pdf" {
		t.Errorf("SourcePDF = %q, want 'pdf'", SourcePDF)
	}
	if SourceEmail != "email" {
		t.Errorf("SourceEmail = %q, want 'email'", SourceEmail)
	}
	if SourceBrowser != "browser" {
		t.Errorf("SourceBrowser = %q, want 'browser'", SourceBrowser)
	}
	if SourceClipboard != "clipboard" {
		t.Errorf("SourceClipboard = %q, want 'clipboard'", SourceClipboard)
	}
}
