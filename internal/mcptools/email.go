package mcptools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type messageRef struct {
	Folder      string `json:"folder" jsonschema:"folder name, as returned by list_folders"`
	UID         uint32 `json:"uid" jsonschema:"message uid from list_emails or search_emails"`
	UIDValidity uint32 `json:"uidvalidity" jsonschema:"uidvalidity value returned alongside the uid; detects stale uids"`
}

func (r messageRef) validate() error {
	if r.Folder == "" {
		return fmt.Errorf("folder is required")
	}
	if r.UID == 0 {
		return fmt.Errorf("uid is required")
	}
	if r.UIDValidity == 0 {
		return fmt.Errorf("uidvalidity is required; use the value returned by list_emails or search_emails")
	}
	return nil
}

type attachmentMeta struct {
	Index            int    `json:"index" jsonschema:"pass to get_attachment"`
	Filename         string `json:"filename"`
	ContentType      string `json:"content_type"`
	EncodedSizeBytes uint32 `json:"encoded_size_bytes" jsonschema:"transfer-encoded size; decoded content is ~25% smaller for base64 parts"`
}

func toAttachmentMetas(in []bridgeclient.AttachmentInfo) []attachmentMeta {
	out := make([]attachmentMeta, 0, len(in))
	for _, a := range in {
		out = append(out, attachmentMeta{
			Index:            a.Index,
			Filename:         a.Filename,
			ContentType:      a.ContentType,
			EncodedSizeBytes: a.EncodedSize,
		})
	}
	return out
}

type getEmailOutput struct {
	UID             uint32           `json:"uid"`
	UIDValidity     uint32           `json:"uidvalidity"`
	Subject         string           `json:"subject"`
	From            []string         `json:"from,omitempty"`
	To              []string         `json:"to,omitempty"`
	Cc              []string         `json:"cc,omitempty"`
	Bcc             []string         `json:"bcc,omitempty" jsonschema:"Bcc recipients; present when retained in the message envelope"`
	Date            string           `json:"date,omitempty" jsonschema:"send date, RFC 3339"`
	Seen            bool             `json:"seen"`
	Flagged         bool             `json:"flagged,omitempty"`
	Answered        bool             `json:"answered,omitempty"`
	TextTruncated   bool             `json:"text_truncated,omitempty" jsonschema:"plain text body was cut short"`
	HTMLTruncated   bool             `json:"html_truncated,omitempty" jsonschema:"html body was cut short"`
	CharsetFallback bool             `json:"charset_fallback,omitempty" jsonschema:"a body used an unknown charset; undecodable bytes were replaced"`
	Attachments     []attachmentMeta `json:"attachments,omitempty"`
}

func registerGetEmail(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_email",
		Description: "Read one message: envelope metadata and attachment list as structured output, with the decoded plain-text and HTML bodies returned as content blocks.",
		Annotations: readOnly("Get email"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in messageRef) (*mcp.CallToolResult, getEmailOutput, error) {
		if err := in.validate(); err != nil {
			return nil, getEmailOutput{}, err
		}
		email, err := bridge.GetEmail(ctx, in.Folder, in.UID, in.UIDValidity)
		if err != nil {
			return nil, getEmailOutput{}, err
		}

		s := email.Summary
		out := getEmailOutput{
			UID:         in.UID,
			UIDValidity: in.UIDValidity,
			Subject:     s.Subject,
			From:        s.From,
			To:          s.To,
			Cc:          s.Cc,
			Bcc:         s.Bcc,
			Seen:        s.Seen,
			Flagged:     s.Flagged,
			Answered:    s.Answered,
			Attachments: toAttachmentMetas(email.Attachments),
		}
		if !s.Date.IsZero() {
			out.Date = s.Date.Format(time.RFC3339)
		}

		// Bodies go in Content only; putting them in the structured output too
		// would double the payload (the SDK serializes Out into content when unset).
		var blocks []mcp.Content
		if email.Plain != nil {
			out.TextTruncated = email.Plain.Truncated
			out.CharsetFallback = out.CharsetFallback || email.Plain.CharsetFallback
			blocks = append(blocks, &mcp.TextContent{Text: "Plain text body:\n" + email.Plain.Text})
		}
		if email.HTML != nil {
			out.HTMLTruncated = email.HTML.Truncated
			out.CharsetFallback = out.CharsetFallback || email.HTML.CharsetFallback
			blocks = append(blocks, &mcp.TextContent{Text: "HTML body:\n" + email.HTML.Text})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, &mcp.TextContent{Text: "This message has no text bodies; see the attachments list in the structured output."})
		}
		return &mcp.CallToolResult{Content: blocks}, out, nil
	})
}
