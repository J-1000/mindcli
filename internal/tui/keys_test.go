package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultKeyMap(t *testing.T) {
	km := DefaultKeyMap()

	// Test that all keybindings are defined
	bindings := []struct {
		name    string
		binding key.Binding
	}{
		{"Search", km.Search},
		{"Enter", km.Enter},
		{"Up", km.Up},
		{"Down", km.Down},
		{"Tab", km.Tab},
		{"ShiftTab", km.ShiftTab},
		{"Open", km.Open},
		{"Copy", km.Copy},
		{"Refresh", km.Refresh},
		{"Help", km.Help},
		{"Quit", km.Quit},
		{"Escape", km.Escape},
		{"PageUp", km.PageUp},
		{"PageDown", km.PageDown},
		{"HalfUp", km.HalfUp},
		{"HalfDown", km.HalfDown},
		{"GotoStart", km.GotoStart},
		{"GotoEnd", km.GotoEnd},
	}

	for _, b := range bindings {
		t.Run(b.name, func(t *testing.T) {
			if len(b.binding.Keys()) == 0 {
				t.Errorf("%s has no keys defined", b.name)
			}
		})
	}
}

func TestKeyMapShortHelp(t *testing.T) {
	km := DefaultKeyMap()
	help := km.ShortHelp()

	if len(help) == 0 {
		t.Error("ShortHelp() returned empty slice")
	}

	// Should include search, help, and quit
	if len(help) != 3 {
		t.Errorf("ShortHelp() returned %d items, want 3", len(help))
	}
}

func TestKeyMapFullHelp(t *testing.T) {
	km := DefaultKeyMap()
	help := km.FullHelp()

	if len(help) == 0 {
		t.Error("FullHelp() returned empty slice")
	}

	// Should have multiple groups
	if len(help) < 3 {
		t.Errorf("FullHelp() returned %d groups, want at least 3", len(help))
	}

	// Each group should have bindings
	for i, group := range help {
		if len(group) == 0 {
			t.Errorf("FullHelp() group %d is empty", i)
		}
	}
}

func TestKeyMatches(t *testing.T) {
	km := DefaultKeyMap()

	tests := []struct {
		name    string
		msg     tea.KeyMsg
		binding key.Binding
		want    bool
	}{
		{"slash matches search", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}, km.Search, true},
		{"q matches quit", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, km.Quit, true},
		{"j matches down", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}, km.Down, true},
		{"k matches up", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}, km.Up, true},
		{"x does not match quit", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}, km.Quit, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := key.Matches(tt.msg, tt.binding)
			if got != tt.want {
				t.Errorf("key.Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}
