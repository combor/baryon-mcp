// Package mcptools defines baryon-mcp's MCP tools. Read tools use EXAMINE and
// peek fetches; save_draft is the sole mailbox-mutating tool.
package mcptools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// Options carries tool settings taken from server configuration.
type Options struct {
	// AttachmentRoots limits save_draft content_path reads to these
	// symlink-resolved directories; empty means unrestricted.
	AttachmentRoots []string
}

// RegisterAll adds every tool to the server, backed by bridge.
func RegisterAll(server *mcp.Server, bridge bridgeclient.Bridge, opts Options) {
	registerListFolders(server, bridge)
	registerListEmails(server, bridge)
	registerSearchEmails(server, bridge)
	registerGetEmail(server, bridge)
	registerListAttachments(server, bridge)
	registerGetAttachment(server, bridge)
	registerSaveDraft(server, bridge, opts.AttachmentRoots)
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
