// Package styles provides styling for the TUI components.
package styles

import "github.com/charmbracelet/lipgloss"

// Colors used throughout the application.
var (
	ColorPrimary   = lipgloss.Color("#7C3AED") // Purple
	ColorSecondary = lipgloss.Color("#10B981") // Green
	ColorMuted     = lipgloss.Color("#6B7280") // Gray
	ColorError     = lipgloss.Color("#EF4444") // Red
	ColorWarning   = lipgloss.Color("#F59E0B") // Yellow
	ColorBorder    = lipgloss.Color("#374151") // Dark gray
	ColorHighlight = lipgloss.Color("#F3F4F6") // Light gray
)

// App-level styles.
var (
	AppStyle = lipgloss.NewStyle().
			Padding(1, 2)

	TitleStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			MarginBottom(1)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginBottom(1)
)

// Panel styles.
var (
	PanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	FocusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1)

	PanelTitleStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			Padding(0, 1)
)

// Search input styles.
var (
	SearchPromptStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true)

	SearchInputStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF"))

	SearchPlaceholderStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)
)

// Results list styles.
var (
	ResultItemStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	SelectedResultStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true).
				PaddingLeft(1).
				BorderLeft(true).
				BorderStyle(lipgloss.ThickBorder()).
				BorderForeground(ColorPrimary)

	ResultTitleStyle = lipgloss.NewStyle().
				Bold(true)

	ResultSourceStyle = lipgloss.NewStyle().
				Foreground(ColorSecondary).
				Italic(true)

	ResultPreviewStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)
)

// Preview panel styles.
var (
	PreviewTitleStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true).
				MarginBottom(1)

	PreviewContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E5E7EB"))

	PreviewMetadataStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				MarginTop(1)
)

// Status bar styles.
var (
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(0, 1)

	StatusKeyStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	StatusValueStyle = lipgloss.NewStyle().
				Foreground(ColorMuted)

	StatusErrorStyle = lipgloss.NewStyle().
				Foreground(ColorError)

	StatusSuccessStyle = lipgloss.NewStyle().
				Foreground(ColorSecondary)
)

// Help styles.
var (
	HelpKeyStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	HelpSeparatorStyle = lipgloss.NewStyle().
				Foreground(ColorBorder)
)

// Spinner style.
var SpinnerStyle = lipgloss.NewStyle().
	Foreground(ColorPrimary)

// TagBadge renders a tag as a colored badge.
func TagBadge(tag string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("62")).
		Padding(0, 1).
		Render("#" + tag)
}

// CollectionBadge renders a collection name as a colored badge.
func CollectionBadge(name string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("117")).
		Background(lipgloss.Color("24")).
		Padding(0, 1).
		Render("@" + name)
}

// Badge styles for source types.
func SourceBadge(source string) lipgloss.Style {
	colors := map[string]lipgloss.Color{
		"markdown":  lipgloss.Color("#3B82F6"), // Blue
		"pdf":       lipgloss.Color("#EF4444"), // Red
		"email":     lipgloss.Color("#F59E0B"), // Yellow
		"browser":   lipgloss.Color("#10B981"), // Green
		"clipboard": lipgloss.Color("#8B5CF6"), // Purple
	}

	color, ok := colors[source]
	if !ok {
		color = ColorMuted
	}

	return lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Padding(0, 1)
}
