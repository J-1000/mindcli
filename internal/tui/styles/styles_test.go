package styles

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestColorsAreDefined(t *testing.T) {
	colors := []struct {
		name  string
		color lipgloss.Color
	}{
		{"ColorPrimary", ColorPrimary},
		{"ColorSecondary", ColorSecondary},
		{"ColorMuted", ColorMuted},
		{"ColorError", ColorError},
		{"ColorWarning", ColorWarning},
		{"ColorBorder", ColorBorder},
		{"ColorHighlight", ColorHighlight},
	}

	for _, c := range colors {
		t.Run(c.name, func(t *testing.T) {
			if c.color == "" {
				t.Errorf("%s is empty", c.name)
			}
		})
	}
}

func TestStylesAreDefined(t *testing.T) {
	// Test that styles can render without panicking
	tests := []struct {
		name  string
		style lipgloss.Style
	}{
		{"AppStyle", AppStyle},
		{"TitleStyle", TitleStyle},
		{"SubtitleStyle", SubtitleStyle},
		{"PanelStyle", PanelStyle},
		{"FocusedPanelStyle", FocusedPanelStyle},
		{"PanelTitleStyle", PanelTitleStyle},
		{"SearchPromptStyle", SearchPromptStyle},
		{"SearchInputStyle", SearchInputStyle},
		{"SearchPlaceholderStyle", SearchPlaceholderStyle},
		{"ResultItemStyle", ResultItemStyle},
		{"SelectedResultStyle", SelectedResultStyle},
		{"ResultTitleStyle", ResultTitleStyle},
		{"ResultSourceStyle", ResultSourceStyle},
		{"ResultPreviewStyle", ResultPreviewStyle},
		{"PreviewTitleStyle", PreviewTitleStyle},
		{"PreviewContentStyle", PreviewContentStyle},
		{"PreviewMetadataStyle", PreviewMetadataStyle},
		{"StatusBarStyle", StatusBarStyle},
		{"StatusKeyStyle", StatusKeyStyle},
		{"StatusValueStyle", StatusValueStyle},
		{"StatusErrorStyle", StatusErrorStyle},
		{"StatusSuccessStyle", StatusSuccessStyle},
		{"HelpKeyStyle", HelpKeyStyle},
		{"HelpDescStyle", HelpDescStyle},
		{"HelpSeparatorStyle", HelpSeparatorStyle},
		{"SpinnerStyle", SpinnerStyle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Rendering should not panic
			result := tt.style.Render("test")
			if result == "" {
				t.Errorf("%s.Render() returned empty string", tt.name)
			}
		})
	}
}

func TestSourceBadge(t *testing.T) {
	sources := []string{
		"markdown",
		"pdf",
		"email",
		"browser",
		"clipboard",
		"unknown", // Should fallback to muted color
	}

	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			style := SourceBadge(source)
			result := style.Render(source)
			if result == "" {
				t.Errorf("SourceBadge(%q).Render() returned empty string", source)
			}
		})
	}
}

func TestSourceBadgeColors(t *testing.T) {
	// Verify different sources have different colors
	mdBadge := SourceBadge("markdown")
	pdfBadge := SourceBadge("pdf")

	mdResult := mdBadge.Render("md")
	pdfResult := pdfBadge.Render("pdf")

	// They should be different (different ANSI codes)
	if mdResult == pdfResult {
		t.Error("markdown and pdf badges should have different colors")
	}
}
