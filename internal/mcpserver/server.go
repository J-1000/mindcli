package mcpserver

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyInput struct{}

// New creates an MCP server exposing only MindCLI's read-only operations.
func New(service *Service, version string, logger *slog.Logger) *mcp.Server {
	if version == "" {
		version = "dev"
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:        "mindcli",
		Title:       "MindCLI",
		Description: "Read-only access to a private local MindCLI knowledge index",
		Version:     version,
	}, &mcp.ServerOptions{
		Instructions: "Use these read-only tools to search and inspect the user's local MindCLI knowledge index. Respect stable document IDs and the returned provenance.",
		Logger:       logger,
		Capabilities: &mcp.ServerCapabilities{},
	})

	mcp.AddTool(server, readOnlyTool(
		"search",
		"Search the local knowledge index with hybrid retrieval and typed filters. Returns bounded redacted previews, stable IDs, scores, and provenance.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		output, err := service.Search(ctx, input)
		return nil, output, err
	})

	mcp.AddTool(server, readOnlyTool(
		"ask",
		"Answer a question from retrieved local documents. Returns a bounded redacted answer, confidence, and ordered citations with stable IDs.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, input AskInput) (*mcp.CallToolResult, AskOutput, error) {
		output, err := service.Ask(ctx, input)
		return nil, output, err
	})

	mcp.AddTool(server, readOnlyTool(
		"get_document",
		"Read one document by stable ID with bounded content and source metadata.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, input GetDocumentInput) (*mcp.CallToolResult, DocumentOutput, error) {
		output, err := service.GetDocument(ctx, input)
		return nil, output, err
	})

	mcp.AddTool(server, readOnlyTool(
		"list_collections",
		"List up to 50 collections with descriptions, saved queries, and member counts.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, ListCollectionsOutput, error) {
		output, err := service.ListCollections(ctx)
		return nil, output, err
	})

	mcp.AddTool(server, readOnlyTool(
		"show_collection",
		"Return collection metadata and bounded manual or smart-query documents with stable IDs and provenance.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, input ShowCollectionInput) (*mcp.CallToolResult, ShowCollectionOutput, error) {
		output, err := service.ShowCollection(ctx, input)
		return nil, output, err
	})

	mcp.AddTool(server, readOnlyTool(
		"recent_documents",
		"Return documents indexed or modified in a bounded time range. Supports lookbacks such as 7d, 2w, or 24h.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, input RecentDocumentsInput) (*mcp.CallToolResult, RecentDocumentsOutput, error) {
		output, err := service.RecentDocuments(ctx, input)
		return nil, output, err
	})

	mcp.AddTool(server, readOnlyTool(
		"related_documents",
		"Find documents related to a stable document ID and explain semantic, lexical, tag, and link signals.",
	), func(ctx context.Context, _ *mcp.CallToolRequest, input RelatedDocumentsInput) (*mcp.CallToolResult, RelatedDocumentsOutput, error) {
		output, err := service.RelatedDocuments(ctx, input)
		return nil, output, err
	})

	return server
}

func readOnlyTool(name, description string) *mcp.Tool {
	falseValue := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title: name, ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: &falseValue, OpenWorldHint: &falseValue,
		},
	}
}
