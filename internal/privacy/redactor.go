package privacy

import (
	"fmt"
	"regexp"
)

const RedactionPlaceholder = "[REDACTED]"

// PatternError represents a bad redact pattern.
type PatternError struct {
	Pattern string
	Err     error
}

func (e PatternError) Error() string {
	return fmt.Sprintf("invalid redact pattern %q: %v", e.Pattern, e.Err)
}

// Redactor replaces configured patterns in text.
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor compiles patterns and returns any errors for invalid entries.
func NewRedactor(patterns []string) (Redactor, []error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	var errs []error
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			errs = append(errs, PatternError{Pattern: pattern, Err: err})
			continue
		}
		compiled = append(compiled, re)
	}
	return Redactor{patterns: compiled}, errs
}

// Redact replaces all occurrences of configured patterns with a placeholder.
func (r Redactor) Redact(text string) string {
	if text == "" || len(r.patterns) == 0 {
		return text
	}
	for _, re := range r.patterns {
		text = re.ReplaceAllString(text, RedactionPlaceholder)
	}
	return text
}
