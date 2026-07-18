// Package mcptools defines baryon-mcp's MCP tools. Read tools use EXAMINE and
// peek fetches; save_draft is the sole mailbox-mutating tool.
package mcptools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// RegisterAll adds every tool to the server, backed by bridge.
func RegisterAll(server *mcp.Server, bridge bridgeclient.Bridge) {
	registerListFolders(server, bridge)
	registerListEmails(server, bridge)
	registerSearchEmails(server, bridge)
	registerGetEmail(server, bridge)
	registerListAttachments(server, bridge)
	registerGetAttachment(server, bridge)
	registerSaveDraft(server, bridge)
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
