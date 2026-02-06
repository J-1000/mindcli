package query

import "testing"

func TestParseQuery(t *testing.T) {
	tests := []struct {
		query        string
		wantIntent   QueryIntent
		wantSource   string
		wantTime     string
	}{
		{
			query:      "golang concurrency",
			wantIntent: IntentSearch,
		},
		{
			query:      "summarize my notes on testing",
			wantIntent: IntentSummarize,
		},
		{
			query:      "what did I write about Go last week",
			wantIntent: IntentAnswer,
			wantTime:   "last week",
		},
		{
			query:      "meetings in my emails",
			wantIntent: IntentSearch,
			wantSource: "email",
		},
		{
			query:      "articles from browser last month",
			wantIntent: IntentSearch,
			wantSource: "browser",
			wantTime:   "last month",
		},
		{
			query:      "how does authentication work in pdfs",
			wantIntent: IntentAnswer,
			wantSource: "pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			parsed := ParseQuery(tt.query)

			if parsed.Intent != tt.wantIntent {
				t.Errorf("Intent = %q, want %q", parsed.Intent, tt.wantIntent)
			}
			if parsed.SourceFilter != tt.wantSource {
				t.Errorf("SourceFilter = %q, want %q", parsed.SourceFilter, tt.wantSource)
			}
			if parsed.TimeFilter != tt.wantTime {
				t.Errorf("TimeFilter = %q, want %q", parsed.TimeFilter, tt.wantTime)
			}
			if parsed.SearchTerms == "" {
				t.Error("SearchTerms should not be empty")
			}
		})
	}
}

func TestParseQueryOriginalPreserved(t *testing.T) {
	query := "  some query with spaces  "
	parsed := ParseQuery(query)

	if parsed.Original != "some query with spaces" {
		t.Errorf("Original = %q, want trimmed input", parsed.Original)
	}
}
