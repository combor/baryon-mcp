// Package mcptools defines baryon-mcp's MCP tools. Read tools use EXAMINE and
// peek fetches; save_draft is the sole mailbox-mutating tool and
// save_attachment the sole one that writes to local disk.
package mcptools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// Options carries tool settings taken from server configuration.
type Options struct {
	// AttachmentRoots confines save_draft content_path reads and save_attachment
	// writes to these symlink-resolved directories; empty means unrestricted.
	// The directories are pinned by identity at registration.
	AttachmentRoots []string
}

// RegisterAll adds every tool to the server, backed by bridge. The attachment
// roots are pinned once here, at startup, so later changes to those directories
// cannot move the boundary the tools enforce.
func RegisterAll(server *mcp.Server, bridge bridgeclient.Bridge, opts Options) {
	roots := pinAttachmentRoots(opts.AttachmentRoots)
	registerListFolders(server, bridge)
	registerListEmails(server, bridge)
	registerSearchEmails(server, bridge)
	registerGetEmail(server, bridge)
	registerGetThread(server, bridge)
	registerListAttachments(server, bridge)
	registerGetAttachment(server, bridge)
	registerSaveAttachment(server, bridge, roots)
	registerSaveDraft(server, bridge, roots)
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
