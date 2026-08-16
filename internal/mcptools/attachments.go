package mcptools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type listAttachmentsOutput struct {
	UID          uint32           `json:"uid"`
	UIDValidity  uint32           `json:"uidvalidity"`
	ContentTrust string           `json:"content_trust" jsonschema:"always untrusted_email: filenames and content types are chosen by the sender"`
	Attachments  []attachmentMeta `json:"attachments"`
}

func registerListAttachments(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_attachments",
		Description: "List a message's attachments (filename, content type, encoded size) without transferring any content." + untrustedNote,
		Annotations: readOnly("List attachments"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in messageRef) (*mcp.CallToolResult, listAttachmentsOutput, error) {
		if err := in.validate(); err != nil {
			return nil, listAttachmentsOutput{}, err
		}
		infos, err := bridge.ListAttachments(ctx, in.Folder, in.UID, in.UIDValidity)
		if err != nil {
			return nil, listAttachmentsOutput{}, err
		}
		return nil, listAttachmentsOutput{
			UID:          in.UID,
			UIDValidity:  in.UIDValidity,
			ContentTrust: contentTrustUntrusted,
			Attachments:  toAttachmentMetas(infos),
		}, nil
	})
}

type getAttachmentInput struct {
	messageRef
	Index int `json:"index" jsonschema:"attachment index from list_attachments or get_email"`
}

type getAttachmentOutput struct {
	Filename         string `json:"filename"`
	ContentType      string `json:"content_type"`
	EncodedSizeBytes uint32 `json:"encoded_size_bytes"`
	DecodedSizeBytes int    `json:"decoded_size_bytes"`
	ContentTrust     string `json:"content_trust" jsonschema:"always untrusted_email: the filename, content type and bytes all come from the sender"`
	DataBase64       string `json:"data_base64,omitempty" jsonschema:"attachment bytes, base64; absent for images, which arrive as image content"`
}

func registerGetAttachment(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_attachment",
		Description: "Fetch one attachment's content into the conversation (up to 25 MB decoded). Images are returned as image content; other files as base64 in the structured output alongside the metadata. For an attachment too large to be worth reading inline, use save_attachment instead." + untrustedNote,
		Annotations: readOnly("Get attachment"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getAttachmentInput) (*mcp.CallToolResult, getAttachmentOutput, error) {
		if err := in.validate(); err != nil {
			return nil, getAttachmentOutput{}, err
		}
		att, err := bridge.GetAttachment(ctx, in.Folder, in.UID, in.UIDValidity, in.Index)
		if err != nil {
			return nil, getAttachmentOutput{}, err
		}

		out := getAttachmentOutput{
			Filename:         att.Filename,
			ContentType:      att.ContentType,
			EncodedSizeBytes: att.EncodedSize,
			DecodedSizeBytes: len(att.Data),
			ContentTrust:     contentTrustUntrusted,
		}

		// Images render via content blocks; anything else goes in the structured
		// output, since clients that prefer structuredContent drop text blocks.
		if strings.HasPrefix(att.ContentType, "image/") {
			block := &mcp.ImageContent{Data: att.Data, MIMEType: att.ContentType}
			// A legacy peer cannot read content_trust, and an image carries
			// whatever the sender drew in it. Label it before the image.
			if legacyContent(req) {
				label := fenceUntrusted("EMAIL ATTACHMENT", fmt.Sprintf("%s (%s, %d bytes) follows as image content",
					att.Filename, att.ContentType, len(att.Data)))
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: label}, block}}, out, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{block}}, out, nil
		}
		out.DataBase64 = base64.StdEncoding.EncodeToString(att.Data)
		// Empty non-nil Content stops the SDK echoing the JSON into a redundant text block.
		res := &mcp.CallToolResult{Content: []mcp.Content{}}
		if legacyContent(req) {
			res.Content = append(res.Content, &mcp.TextContent{Text: fenceUntrusted("EMAIL ATTACHMENT", fmt.Sprintf("%s (%s, %d bytes), base64:\n%s",
				att.Filename, att.ContentType, len(att.Data), out.DataBase64))})
		}
		return res, out, nil
	})
}

type saveAttachmentInput struct {
	messageRef
	Index      int    `json:"index" jsonschema:"attachment index from list_attachments or get_email"`
	OutputPath string `json:"output_path" jsonschema:"where to write the decoded attachment on the server's machine: a path relative to the server's attachment directory, or an absolute path inside it. The parent directory must already exist and the file must not; not available on Windows"`
}

type saveAttachmentOutput struct {
	Filename         string `json:"filename" jsonschema:"the attachment's own filename, which is not where it was written"`
	ContentType      string `json:"content_type"`
	EncodedSizeBytes uint32 `json:"encoded_size_bytes"`
	DecodedSizeBytes int    `json:"decoded_size_bytes" jsonschema:"bytes written to disk"`
	SavedPath        string `json:"saved_path" jsonschema:"symlink-resolved path the attachment was written to"`
	ContentTrust     string `json:"content_trust" jsonschema:"always untrusted_email: the file now on disk holds bytes the sender chose"`
}

// saveAttachmentAnnotations claims neither read-only nor idempotent: this tool
// creates a file, and refusing to overwrite makes a repeated call fail.
func saveAttachmentAnnotations() *mcp.ToolAnnotations {
	destructive := false
	closedWorld := false
	return &mcp.ToolAnnotations{
		Title:           "Save attachment",
		ReadOnlyHint:    false,
		DestructiveHint: &destructive,
		IdempotentHint:  false,
		OpenWorldHint:   &closedWorld,
	}
}

func registerSaveAttachment(server *mcp.Server, bridge bridgeclient.Bridge, roots attachmentRoots) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_attachment",
		Description: "Write one attachment (up to 25 MB decoded) to a local file on the server's machine and return only its path, keeping the bytes out of the conversation. Use this for attachments too large to read inline with get_attachment. output_path may be relative, in which case it resolves inside the server's attachment directory. An existing file is never overwritten and no directories are created." + untrustedNote,
		Annotations: saveAttachmentAnnotations(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveAttachmentInput) (*mcp.CallToolResult, saveAttachmentOutput, error) {
		if err := in.validate(); err != nil {
			return nil, saveAttachmentOutput{}, err
		}
		if in.OutputPath == "" {
			return nil, saveAttachmentOutput{}, fmt.Errorf("output_path is required; use get_attachment to read an attachment inline instead")
		}
		att, err := bridge.GetAttachment(ctx, in.Folder, in.UID, in.UIDValidity, in.Index)
		if err != nil {
			return nil, saveAttachmentOutput{}, err
		}
		// Don't leave a file behind for a caller that already gave up.
		if err := ctx.Err(); err != nil {
			return nil, saveAttachmentOutput{}, err
		}
		saved, err := writeAttachmentFile(in.OutputPath, att.Data, roots)
		if err != nil {
			return nil, saveAttachmentOutput{}, err
		}

		out := saveAttachmentOutput{
			Filename:         att.Filename,
			ContentType:      att.ContentType,
			EncodedSizeBytes: att.EncodedSize,
			DecodedSizeBytes: len(att.Data),
			SavedPath:        saved,
			ContentTrust:     contentTrustUntrusted,
		}
		// Empty non-nil Content stops the SDK echoing the JSON into a redundant text block.
		res := &mcp.CallToolResult{Content: []mcp.Content{}}
		if legacyContent(req) {
			// The filename and content type are the sender's words.
			res.Content = append(res.Content, &mcp.TextContent{Text: fenceUntrusted("EMAIL ATTACHMENT", fmt.Sprintf("%s (%s, %d bytes) written to %s",
				att.Filename, att.ContentType, len(att.Data), saved))})
		}
		return res, out, nil
	})
}
