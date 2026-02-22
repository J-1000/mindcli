package privacy

import "testing"

func TestRedactorRedacts(t *testing.T) {
	redactor, errs := NewRedactor([]string{`token-[0-9]+`, `secret-[a-z]+`})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}

	input := "token-123 secret-abc keep"
	want := RedactionPlaceholder + " " + RedactionPlaceholder + " keep"
	if got := redactor.Redact(input); got != want {
		t.Fatalf("Redact() = %q, want %q", got, want)
	}
}

func TestRedactorSkipsInvalidPatterns(t *testing.T) {
	redactor, errs := NewRedactor([]string{`(`, `ok`})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}

	if got := redactor.Redact("ok"); got != RedactionPlaceholder {
		t.Fatalf("Redact() = %q, want %q", got, RedactionPlaceholder)
	}
}

func TestRedactorNoPatterns(t *testing.T) {
	redactor, errs := NewRedactor(nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}

	input := "no redaction"
	if got := redactor.Redact(input); got != input {
		t.Fatalf("Redact() = %q, want %q", got, input)
	}
}
