// Package tui provides the terminal user interface for MindCLI.
package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines the keybindings for the application.
type KeyMap struct {
	Search    key.Binding
	Enter     key.Binding
	Up        key.Binding
	Down      key.Binding
	Tab       key.Binding
	ShiftTab  key.Binding
	Open      key.Binding
	Copy      key.Binding
	Refresh   key.Binding
	Help      key.Binding
	Quit      key.Binding
	Escape    key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	HalfUp    key.Binding
	HalfDown  key.Binding
	GotoStart key.Binding
	GotoEnd   key.Binding
}

// DefaultKeyMap returns the default keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("k/up", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("j/down", "down"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next panel"),
		),
		ShiftTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev panel"),
		),
		Open: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open"),
		),
		Copy: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "copy path"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "ctrl+b"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+f"),
			key.WithHelp("pgdn", "page down"),
		),
		HalfUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "half page up"),
		),
		HalfDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("ctrl+d", "half page down"),
		),
		GotoStart: key.NewBinding(
			key.WithKeys("home", "g"),
			key.WithHelp("g", "go to start"),
		),
		GotoEnd: key.NewBinding(
			key.WithKeys("end", "G"),
			key.WithHelp("G", "go to end"),
		),
	}
}

// ShortHelp returns keybindings to be shown in the mini help view.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Search, k.Help, k.Quit}
}

// FullHelp returns keybindings for the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Search, k.Enter, k.Escape},
		{k.Up, k.Down, k.Tab},
		{k.Open, k.Copy, k.Refresh},
		{k.Help, k.Quit},
	}
}
