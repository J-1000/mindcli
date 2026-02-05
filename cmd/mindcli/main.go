package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/storage"
	"github.com/jankowtf/mindcli/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Ensure data directory exists
	if _, err := cfg.DataDir(); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	// Open database
	dbPath, err := cfg.DatabasePath()
	if err != nil {
		return fmt.Errorf("getting database path: %w", err)
	}

	db, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Create and run the TUI
	model := tui.New(db)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}
