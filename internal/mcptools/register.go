// Package mcptools defines baryon-mcp's MCP tools. Every tool is read-only:
// mailboxes are opened with EXAMINE and fetches peek, so no tool call can
// mutate mail.
package mcptools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// RegisterAll adds every tool to the server, backed by bridge.
func RegisterAll(server *mcp.Server, bridge bridgeclient.Bridge) {
	registerListFolders(server, bridge)
}

// readOnly returns the annotations shared by all baryon-mcp tools.
func readOnly(title string) *mcp.ToolAnnotations {
	closedWorld := false
	return &mcp.ToolAnnotations{
		Title:          title,
		ReadOnlyHint:   true,
		IdempotentHint: true,
		OpenWorldHint:  &closedWorld,
	}
}
