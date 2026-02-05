# MindCLI

A fast, private TUI for personal knowledge management with AI-powered search.

## Features

- **Local-first**: All data stays on your machine
- **Multi-source indexing**: Notes, PDFs, emails, clipboard, browser history
- **Semantic search**: Natural language queries powered by embeddings
- **Beautiful TUI**: Keyboard-driven interface with live preview
- **Fast**: Concurrent indexing with Go

## Status

🚧 **Under Development** - Phase 1: Foundation

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

# Index a directory
mindcli index ~/notes

# Search from command line
mindcli search "my query"
```

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
```

### Project Structure

```
mindcli/
├── cmd/mindcli/     # Main entry point
├── internal/
│   ├── config/      # Configuration management
│   ├── storage/     # SQLite and vector storage
│   ├── index/       # Indexing pipeline
│   ├── query/       # Search and query engine
│   ├── embeddings/  # Embedding providers
│   └── tui/         # Terminal UI components
└── pkg/
    └── chunker/     # Text chunking utilities
```

## Roadmap

- [x] Project setup
- [ ] Configuration system
- [ ] SQLite storage layer
- [ ] Basic TUI shell
- [ ] Markdown indexing
- [ ] Full-text search (Bleve)
- [ ] Semantic search (embeddings)
- [ ] PDF support
- [ ] Email integration
- [ ] Browser history
- [ ] Clipboard tracking
- [ ] LLM query understanding

## License

MIT
