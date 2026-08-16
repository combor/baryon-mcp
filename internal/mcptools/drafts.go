package mcptools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type draftAttachmentInput struct {
	Filename      string  `json:"filename,omitempty" jsonschema:"attachment filename; required with content_base64, defaults to the content_path basename"`
	ContentType   string  `json:"content_type,omitempty" jsonschema:"MIME content type, for example application/pdf; required with content_base64, inferred from the filename extension with content_path"`
	ContentBase64 *string `json:"content_base64,omitempty" jsonschema:"standard base64-encoded attachment bytes; up to 25 MB decoded"`
	ContentPath   *string `json:"content_path,omitempty" jsonschema:"path to a regular file on the server's machine, read when the draft is saved: relative to the server's attachment directory, or absolute and inside it. Each attachment takes exactly one of content_base64 or content_path; not available on Windows"`
}

type saveDraftInput struct {
	From        string                 `json:"from" jsonschema:"sender address; must be one of the Proton account addresses exposed by Bridge, which list_sender_identities reports"`
	To          []string               `json:"to,omitempty" jsonschema:"To recipient addresses"`
	Cc          []string               `json:"cc,omitempty" jsonschema:"Cc recipient addresses"`
	Bcc         []string               `json:"bcc,omitempty" jsonschema:"Bcc recipient addresses retained in the saved draft"`
	Subject     string                 `json:"subject,omitempty"`
	TextBody    string                 `json:"text_body,omitempty" jsonschema:"plain-text body, up to 50000 characters"`
	HTMLBody    string                 `json:"html_body,omitempty" jsonschema:"optional HTML alternative, up to 50000 characters"`
	InReplyTo   []string               `json:"in_reply_to,omitempty" jsonschema:"Message-IDs this draft replies to, normally just the message_id of the email being answered; angle brackets optional; when replacing a draft, omit to keep the existing header or pass an empty array to remove it"`
	References  []string               `json:"references,omitempty" jsonschema:"conversation chain: the parent email's references followed by its message_id; set together with in_reply_to so clients thread the reply; when replacing a draft, omit to keep the existing header or pass an empty array to remove it"`
	Attachments []draftAttachmentInput `json:"attachments,omitempty" jsonschema:"regular file attachments; at most 100 and 25 MB decoded in total"`
	UID         uint32                 `json:"uid,omitempty" jsonschema:"existing draft UID to replace; requires uidvalidity"`
	UIDValidity uint32                 `json:"uidvalidity,omitempty" jsonschema:"Drafts UIDVALIDITY accompanying uid"`
}

type saveDraftOutput struct {
	Folder               string `json:"folder"`
	UID                  uint32 `json:"uid" jsonschema:"UID of the newly saved draft"`
	UIDValidity          uint32 `json:"uidvalidity" jsonschema:"Drafts folder generation accompanying uid"`
	ReplacedUID          uint32 `json:"replaced_uid,omitempty"`
	PreviousDraftRemoved bool   `json:"previous_draft_removed"`
	Warning              string `json:"warning,omitempty"`
}

// draftAnnotations describes a mailbox write. destructive marks a tool that can
// remove something: save_draft can, since replacing a draft expunges the
// previous version, while one that only appends is additive.
func draftAnnotations(title string, destructive bool) *mcp.ToolAnnotations {
	closedWorld := false
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		DestructiveHint: &destructive,
		IdempotentHint:  false,
		OpenWorldHint:   &closedWorld,
	}
}

// draftAttachments loads every attachment, from inline base64 or a confined
// local path, before anything touches the mailbox: an unreadable file fails the
// call rather than leaving a half-built draft.
func draftAttachments(in []draftAttachmentInput, roots attachmentRoots) ([]bridgeclient.DraftAttachment, error) {
	// Bound conversion work before the bridge validates the decoded Draft.
	if len(in) > bridgeclient.MaxDraftAttachments {
		return nil, fmt.Errorf("a draft may contain at most %d attachments", bridgeclient.MaxDraftAttachments)
	}
	var loaded []bridgeclient.DraftAttachment
	total := 0
	for i, attachment := range in {
		if (attachment.ContentBase64 == nil) == (attachment.ContentPath == nil) {
			return nil, fmt.Errorf("attachment %d must set exactly one of content_base64 or content_path", i)
		}
		var one bridgeclient.DraftAttachment
		var err error
		if attachment.ContentPath != nil {
			one, err = readAttachmentFile(i, attachment, roots)
		} else {
			one, err = decodeAttachment(i, attachment)
		}
		if err != nil {
			return nil, err
		}
		total += len(one.Data)
		if total > bridgeclient.MaxDraftAttachmentTotalBytes {
			return nil, fmt.Errorf("attachments total %d bytes, above the %d byte limit", total, bridgeclient.MaxDraftAttachmentTotalBytes)
		}
		loaded = append(loaded, one)
	}
	return loaded, nil
}

func toDraft(in saveDraftInput, roots attachmentRoots) (bridgeclient.Draft, error) {
	if (in.UID == 0) != (in.UIDValidity == 0) {
		return bridgeclient.Draft{}, fmt.Errorf("uid and uidvalidity must be supplied together when replacing a draft")
	}

	draft := bridgeclient.Draft{
		From:       in.From,
		To:         in.To,
		Cc:         in.Cc,
		Bcc:        in.Bcc,
		Subject:    in.Subject,
		TextBody:   in.TextBody,
		HTMLBody:   in.HTMLBody,
		InReplyTo:  in.InReplyTo,
		References: in.References,
	}
	if in.UID != 0 {
		draft.Replace = &bridgeclient.DraftRef{UID: in.UID, UIDValidity: in.UIDValidity}
	}
	attachments, err := draftAttachments(in.Attachments, roots)
	if err != nil {
		return bridgeclient.Draft{}, err
	}
	draft.Attachments = attachments
	return draft, nil
}

func registerSaveDraft(server *mcp.Server, bridge bridgeclient.Bridge, roots attachmentRoots) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_draft",
		Description: "Create a complete Proton Mail draft, or replace an existing draft when uid and uidvalidity are provided. Supports plain text, an optional HTML alternative, and bounded attachments supplied either inline as base64 (content_base64) or as a path inside the server's attachment directory that it reads at save time (content_path). To answer one message, save_reply_draft derives its recipients and threading for you. To reply inside a thread here, read the message with get_email and set in_reply_to to its message_id, and references to its references followed by its message_id; when it reports no references, use its in_reply_to in their place so earlier ancestry survives. A replacement keeps the previous draft's Message-ID, plus whichever of its In-Reply-To and References the call omits; passing an empty array for either removes it, detaching the draft from its thread. Updating appends the replacement before removing the old UID; inspect warning if cleanup was incomplete.",
		Annotations: draftAnnotations("Save draft", true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveDraftInput) (*mcp.CallToolResult, saveDraftOutput, error) {
		draft, err := toDraft(in, roots)
		if err != nil {
			return nil, saveDraftOutput{}, err
		}
		saved, err := bridge.SaveDraft(ctx, draft)
		if err != nil {
			return nil, saveDraftOutput{}, err
		}
		return nil, saveDraftOutput{
			Folder:               saved.Folder,
			UID:                  saved.UID,
			UIDValidity:          saved.UIDValidity,
			ReplacedUID:          saved.ReplacedUID,
			PreviousDraftRemoved: saved.PreviousDraftRemoved,
			Warning:              saved.Warning,
		}, nil
	})
}
