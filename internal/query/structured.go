package query

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/storage"
)

var knownStructuredFilters = map[string]struct{}{
	"source":     {},
	"type":       {},
	"tag":        {},
	"collection": {},
	"after":      {},
	"before":     {},
	"path":       {},
	"domain":     {},
	"kind":       {},
}

type queryToken struct {
	text   string
	quoted bool
	colon  int
}

// ParseQueryStrict parses explicit filters before applying natural-language
// intent/source/time conveniences. It returns errors for malformed syntax,
// unknown filter names, and invalid dates.
func ParseQueryStrict(raw string) (ParsedQuery, error) {
	original := strings.TrimSpace(raw)
	parsed := ParsedQuery{Original: original, Intent: IntentSearch}

	tokens, err := lexQuery(original)
	if err != nil {
		return ParsedQuery{}, err
	}

	free := make([]queryToken, 0, len(tokens))
	for _, token := range tokens {
		handled, err := applyStructuredFilter(&parsed.Filters, token)
		if err != nil {
			return ParsedQuery{}, err
		}
		if !handled {
			free = append(free, token)
		}
	}
	if parsed.Filters.After != nil && parsed.Filters.Before != nil && !parsed.Filters.After.Before(*parsed.Filters.Before) {
		return ParsedQuery{}, fmt.Errorf("after date must be earlier than before date")
	}

	freeText := renderQueryTokens(free)
	freeText = applyIntentHeuristic(&parsed, freeText)
	freeText = applyNaturalSourceHeuristic(&parsed, freeText)
	freeText = applyNaturalTimeHeuristic(&parsed, freeText)

	searchTokens, err := lexQuery(freeText)
	if err != nil {
		return ParsedQuery{}, err
	}
	positiveTerms := make([]string, 0, len(searchTokens))
	legacyTerms := make([]queryToken, 0, len(searchTokens))
	for _, token := range searchTokens {
		if token.text == "" {
			return ParsedQuery{}, fmt.Errorf("empty quoted phrase")
		}
		if strings.HasPrefix(token.text, "-") && len(token.text) > 1 {
			term := strings.TrimSpace(strings.TrimPrefix(token.text, "-"))
			if term == "" {
				return ParsedQuery{}, fmt.Errorf("empty negated term")
			}
			parsed.Filters.ExcludedTerms = appendUnique(parsed.Filters.ExcludedTerms, term)
			legacyTerms = append(legacyTerms, token)
			continue
		}
		if token.quoted {
			parsed.Filters.ExactPhrases = appendUnique(parsed.Filters.ExactPhrases, token.text)
			legacyTerms = append(legacyTerms, token)
			continue
		}
		positiveTerms = append(positiveTerms, token.text)
		legacyTerms = append(legacyTerms, token)
	}

	parsed.Text = strings.TrimSpace(strings.Join(positiveTerms, " "))
	parsed.SearchTerms = strings.TrimSpace(renderQueryTokens(legacyTerms))
	if len(parsed.Filters.Sources) > 0 {
		parsed.SourceFilter = string(parsed.Filters.Sources[0])
	}
	parsed.TimeFilter = parsed.Filters.RelativeTime
	return parsed, nil
}

func lexQuery(input string) ([]queryToken, error) {
	var tokens []queryToken
	var builder strings.Builder
	inQuote := false
	escaped := false
	started := false
	quoted := false
	colon := -1

	flush := func() {
		if !started {
			return
		}
		tokens = append(tokens, queryToken{text: builder.String(), quoted: quoted, colon: colon})
		builder.Reset()
		started = false
		quoted = false
		colon = -1
	}

	for _, r := range input {
		if escaped {
			if unicode.IsSpace(r) {
				quoted = true
			}
			builder.WriteRune(r)
			started = true
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			started = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			quoted = true
			started = true
			continue
		}
		if unicode.IsSpace(r) && !inQuote {
			flush()
			continue
		}
		if r == ':' && !inQuote && colon < 0 {
			colon = builder.Len()
		}
		builder.WriteRune(r)
		started = true
	}
	if escaped {
		return nil, fmt.Errorf("query ends with an incomplete escape")
	}
	if inQuote {
		return nil, fmt.Errorf("query contains an unterminated quote")
	}
	flush()
	return tokens, nil
}

func applyStructuredFilter(filters *filter.Set, token queryToken) (bool, error) {
	if token.colon < 0 {
		return false, nil
	}

	name := token.text[:token.colon]
	value := strings.TrimSpace(token.text[token.colon+1:])
	negated := strings.HasPrefix(name, "-")
	name = strings.ToLower(strings.TrimPrefix(name, "-"))
	if !validFilterName(name) {
		return false, nil
	}
	if _, known := knownStructuredFilters[name]; !known {
		// Keep URLs and drive-prefixed paths as ordinary search terms.
		if strings.HasPrefix(value, "//") || len(name) == 1 {
			return false, nil
		}
		return false, fmt.Errorf("unknown filter %q (valid filters: %s)", name, structuredFilterNames())
	}
	if value == "" {
		return false, fmt.Errorf("filter %q requires a value", name)
	}
	if negated && name != "tag" {
		return false, fmt.Errorf("filter %q cannot be negated", name)
	}

	switch name {
	case "source", "type":
		source := storage.Source(strings.ToLower(value))
		if !storage.IsKnownSource(source) {
			return false, fmt.Errorf("unknown source %q (valid sources: %s)", value, knownSourceNames())
		}
		filters.Sources = appendUniqueSource(filters.Sources, source)
	case "tag":
		value = strings.ToLower(value)
		if negated {
			filters.ExcludedTags = appendUnique(filters.ExcludedTags, value)
		} else {
			filters.Tags = appendUnique(filters.Tags, value)
		}
	case "collection":
		filters.Collections = appendUnique(filters.Collections, value)
	case "after":
		if filters.After != nil {
			return false, fmt.Errorf("filter %q may only be specified once", name)
		}
		parsedDate, err := parseFilterDate(value)
		if err != nil {
			return false, fmt.Errorf("invalid after date %q: use YYYY-MM-DD or RFC3339", token.text[token.colon+1:])
		}
		filters.After = &parsedDate
	case "before":
		if filters.Before != nil {
			return false, fmt.Errorf("filter %q may only be specified once", name)
		}
		parsedDate, err := parseFilterDate(value)
		if err != nil {
			return false, fmt.Errorf("invalid before date %q: use YYYY-MM-DD or RFC3339", token.text[token.colon+1:])
		}
		filters.Before = &parsedDate
	case "path":
		filters.PathPrefixes = appendUnique(filters.PathPrefixes, value)
	case "domain":
		value = strings.ToLower(strings.Trim(strings.TrimPrefix(value, "*."), "."))
		if value == "" {
			return false, fmt.Errorf("filter %q requires a domain", name)
		}
		filters.Domains = appendUnique(filters.Domains, value)
	case "kind":
		filters.Kinds = appendUnique(filters.Kinds, strings.ToLower(value))
	}
	return true, nil
}

func applyIntentHeuristic(parsed *ParsedQuery, text string) string {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "summarize "):
		parsed.Intent = IntentSummarize
		return strings.TrimSpace(trimmed[len("summarize "):])
	case strings.HasPrefix(lower, "summary of "):
		parsed.Intent = IntentSummarize
		return strings.TrimSpace(trimmed[len("summary of "):])
	case strings.HasPrefix(lower, "what "), strings.HasPrefix(lower, "how "),
		strings.HasPrefix(lower, "why "), strings.HasPrefix(lower, "when "),
		strings.HasPrefix(lower, "who "), strings.HasPrefix(lower, "tell me "):
		parsed.Intent = IntentAnswer
	}
	return trimmed
}

func applyNaturalSourceHeuristic(parsed *ParsedQuery, text string) string {
	keywords := []struct {
		phrase string
		source storage.Source
	}{
		{"from clipboard", storage.SourceClipboard},
		{"from browser", storage.SourceBrowser},
		{"in my emails", storage.SourceEmail},
		{"in my notes", storage.SourceMarkdown},
		{"in browser", storage.SourceBrowser},
		{"in emails", storage.SourceEmail},
		{"in pdfs", storage.SourcePDF},
		{"in pdf", storage.SourcePDF},
		{"in html", storage.SourceHTML},
		{"in web archives", storage.SourceHTML},
		{"in docx", storage.SourceDOCX},
		{"in ebooks", storage.SourceEPUB},
		{"in epubs", storage.SourceEPUB},
		{"in org files", storage.SourceOrg},
		{"in code", storage.SourceCode},
	}
	for _, keyword := range keywords {
		updated, found := removeFoldOnce(text, keyword.phrase)
		if !found {
			continue
		}
		if len(parsed.Filters.Sources) == 0 {
			parsed.Filters.Sources = []storage.Source{keyword.source}
		}
		return strings.TrimSpace(updated)
	}
	return strings.TrimSpace(text)
}

func knownSourceNames() string {
	values := make([]string, 0, len(storage.KnownSources()))
	for _, source := range storage.KnownSources() {
		values = append(values, string(source))
	}
	return strings.Join(values, ", ")
}

func applyNaturalTimeHeuristic(parsed *ParsedQuery, text string) string {
	keywords := []string{
		"this month", "last month", "this week", "last week",
		"yesterday", "today", "last year",
	}
	for _, keyword := range keywords {
		updated, found := removeFoldOnce(text, keyword)
		if !found {
			continue
		}
		if parsed.Filters.After == nil && parsed.Filters.Before == nil {
			parsed.Filters.RelativeTime = keyword
		}
		return strings.TrimSpace(updated)
	}
	return strings.TrimSpace(text)
}

func parseFilterDate(value string) (time.Time, error) {
	if len(value) == len("2006-01-02") {
		return time.Parse("2006-01-02", value)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func renderQueryTokens(tokens []queryToken) string {
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		text := token.text
		negated := strings.HasPrefix(text, "-") && len(text) > 1
		if negated {
			text = strings.TrimPrefix(text, "-")
		}
		if token.quoted || strings.IndexFunc(text, unicode.IsSpace) >= 0 {
			text = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
		}
		if negated {
			text = "-" + text
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

func removeFoldOnce(text, phrase string) (string, bool) {
	index := strings.Index(strings.ToLower(text), strings.ToLower(phrase))
	if index < 0 {
		return text, false
	}
	return text[:index] + text[index+len(phrase):], true
}

func validFilterName(name string) bool {
	if name == "" {
		return false
	}
	runes := []rune(name)
	if !unicode.IsLetter(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func structuredFilterNames() string {
	names := make([]string, 0, len(knownStructuredFilters))
	for name := range knownStructuredFilters {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueSource(values []storage.Source, value storage.Source) []storage.Source {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
