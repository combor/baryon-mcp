// Package mcptools defines baryon-mcp's MCP tools. Read tools use EXAMINE and
// peek fetches; save_draft and save_reply_draft are the only mailbox-mutating
// tools and save_attachment the only one that writes to local disk.
package mcptools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
)

// Options carries tool settings taken from server configuration.
type Options struct {
	// AttachmentRoots confines save_draft content_path reads and save_attachment
	// writes to these symlink-resolved directories; empty means unrestricted.
	// The directories are pinned by identity at registration.
	AttachmentRoots []string
	// Identities are the addresses a draft may be sent from, most preferred
	// first. save_reply_draft refuses any other From.
	Identities []config.Identity
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
	registerListSenderIdentities(server, opts.Identities)
	registerSaveDraft(server, bridge, roots)
	registerSaveReplyDraft(server, bridge, roots, opts.Identities)
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
