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
	UID         uint32           `json:"uid"`
	UIDValidity uint32           `json:"uidvalidity"`
	Attachments []attachmentMeta `json:"attachments"`
}

func registerListAttachments(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_attachments",
		Description: "List a message's attachments (filename, content type, encoded size) without transferring any content.",
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
			UID:         in.UID,
			UIDValidity: in.UIDValidity,
			Attachments: toAttachmentMetas(infos),
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
}

func registerGetAttachment(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_attachment",
		Description: "Fetch one attachment's content (up to 1 MiB encoded). Images are returned as image content; other files as base64 text. Metadata is in the structured output.",
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
		}

		// Payload goes in Content exactly once; the small Out is safe to auto-serialize.
		var block mcp.Content
		if strings.HasPrefix(att.ContentType, "image/") {
			block = &mcp.ImageContent{Data: att.Data, MIMEType: att.ContentType}
		} else {
			block = &mcp.TextContent{Text: fmt.Sprintf("%s (%s, %d bytes), base64:\n%s",
				att.Filename, att.ContentType, len(att.Data), base64.StdEncoding.EncodeToString(att.Data))}
		}
		return &mcp.CallToolResult{Content: []mcp.Content{block}}, out, nil
	})
}
