# MindCLI

A fast, private TUI for personal knowledge management with AI-powered search.

## Features

- **Local-first**: All data stays on your machine
- **Multi-source indexing**: Notes, PDFs, emails, clipboard, browser history
- **Semantic search**: Natural language queries powered by embeddings
- **Beautiful TUI**: Keyboard-driven interface with live preview
- **Fast**: Concurrent indexing with Go

## Status

Phase 2 complete - Markdown indexing with Bleve full-text search.

## Installation

```bash
# Build from source
go install github.com/jankowtf/mindcli/cmd/mindcli@latest

# Or clone and build
git clone https://github.com/jankowtf/mindcli.git
cd mindcli
make build
```

## Usage

```bash
# Start the TUI
mindcli

# Index a directory (coming soon)
mindcli index ~/notes

# Search from command line (coming soon)
mindcli search "my query"
```

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `/` | Focus search |
| `Enter` | Execute search / Select |
| `j/k` or `Arrow` | Navigate results |
| `Tab` | Cycle panels |
| `?` | Toggle help |
| `q` | Quit |

## Configuration

MindCLI looks for configuration in `~/.config/mindcli/config.yaml`:

```yaml
sources:
  markdown:
    enabled: true
    paths:
      - ~/notes
      - ~/Documents

embeddings:
  provider: ollama
  model: nomic-embed-text
```

## Development

### Prerequisites

- Go 1.21+
- Make (optional)

### Building

```bash
# Build binary
make build

# Run tests
make test

# Run with race detector
make test-race

# Generate coverage report
make test-coverage
```

### Project Structure

```
mindcli/
├── cmd/mindcli/          # Main entry point
├── internal/
│   ├── config/           # Configuration management
│   ├── storage/          # SQLite database layer
│   │   ├── models.go     # Document, Chunk, SearchResult
│   │   └── sqlite.go     # Database operations
│   ├── tui/              # Terminal UI
│   │   ├── app.go        # Main Bubble Tea model
│   │   ├── keys.go       # Keybindings
│   │   └── styles/       # Lip Gloss styles
│   ├── index/            # Indexing pipeline (planned)
│   ├── query/            # Search engine (planned)
│   └── embeddings/       # Embedding providers (planned)
└── pkg/
    └── chunker/          # Text chunking (planned)
```

## Roadmap

- [x] Project setup
- [x] Configuration system
- [x] SQLite storage layer
- [x] Basic TUI shell
- [x] Markdown indexing
- [x] Full-text search (Bleve)
- [ ] Semantic search (embeddings)
- [ ] PDF support
- [ ] Email integration
- [ ] Browser history
- [ ] Clipboard tracking
- [ ] LLM query understanding

## License

MIT
