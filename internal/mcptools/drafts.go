package mcptools

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type draftAttachmentInput struct {
	Filename      string `json:"filename" jsonschema:"attachment filename"`
	ContentType   string `json:"content_type" jsonschema:"MIME content type, for example application/pdf"`
	ContentBase64 string `json:"content_base64" jsonschema:"standard base64-encoded attachment bytes"`
}

type saveDraftInput struct {
	From        string                 `json:"from" jsonschema:"sender address; must be one of the Proton account addresses exposed by Bridge"`
	To          []string               `json:"to,omitempty" jsonschema:"To recipient addresses"`
	Cc          []string               `json:"cc,omitempty" jsonschema:"Cc recipient addresses"`
	Bcc         []string               `json:"bcc,omitempty" jsonschema:"Bcc recipient addresses retained in the saved draft"`
	Subject     string                 `json:"subject,omitempty"`
	TextBody    string                 `json:"text_body,omitempty" jsonschema:"plain-text body, up to 50000 characters"`
	HTMLBody    string                 `json:"html_body,omitempty" jsonschema:"optional HTML alternative, up to 50000 characters"`
	Attachments []draftAttachmentInput `json:"attachments,omitempty" jsonschema:"regular file attachments; at most 10"`
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

func saveDraftAnnotations() *mcp.ToolAnnotations {
	destructive := true
	closedWorld := false
	return &mcp.ToolAnnotations{
		Title:           "Save draft",
		ReadOnlyHint:    false,
		DestructiveHint: &destructive,
		IdempotentHint:  false,
		OpenWorldHint:   &closedWorld,
	}
}

func toDraft(in saveDraftInput) (bridgeclient.Draft, error) {
	if (in.UID == 0) != (in.UIDValidity == 0) {
		return bridgeclient.Draft{}, fmt.Errorf("uid and uidvalidity must be supplied together when replacing a draft")
	}
	// Bound conversion work before the bridge validates the decoded Draft.
	if len(in.Attachments) > bridgeclient.MaxDraftAttachments {
		return bridgeclient.Draft{}, fmt.Errorf("a draft may contain at most %d attachments", bridgeclient.MaxDraftAttachments)
	}

	draft := bridgeclient.Draft{
		From:     in.From,
		To:       in.To,
		Cc:       in.Cc,
		Bcc:      in.Bcc,
		Subject:  in.Subject,
		TextBody: in.TextBody,
		HTMLBody: in.HTMLBody,
	}
	if in.UID != 0 {
		draft.Replace = &bridgeclient.DraftRef{UID: in.UID, UIDValidity: in.UIDValidity}
	}
	totalAttachmentBytes := 0
	for i, attachment := range in.Attachments {
		if len(attachment.ContentBase64) > base64.StdEncoding.EncodedLen(bridgeclient.MaxDraftAttachmentBytes) {
			return bridgeclient.Draft{}, fmt.Errorf("attachment %d encoded content is above the %d byte decoded limit", i, bridgeclient.MaxDraftAttachmentBytes)
		}
		data, err := base64.StdEncoding.DecodeString(attachment.ContentBase64)
		if err != nil {
			return bridgeclient.Draft{}, fmt.Errorf("attachment %d content_base64 is invalid: %w", i, err)
		}
		// EncodedLen is shared by several neighboring decoded sizes, so enforce
		// the exact per-attachment limit after decoding as well.
		if len(data) > bridgeclient.MaxDraftAttachmentBytes {
			return bridgeclient.Draft{}, fmt.Errorf("attachment %d decoded content is above the %d byte limit", i, bridgeclient.MaxDraftAttachmentBytes)
		}
		totalAttachmentBytes += len(data)
		if totalAttachmentBytes > bridgeclient.MaxDraftAttachmentTotalBytes {
			return bridgeclient.Draft{}, fmt.Errorf("attachments total %d bytes, above the %d byte limit", totalAttachmentBytes, bridgeclient.MaxDraftAttachmentTotalBytes)
		}
		draft.Attachments = append(draft.Attachments, bridgeclient.DraftAttachment{
			Filename: attachment.Filename, ContentType: attachment.ContentType, Data: data,
		})
	}
	return draft, nil
}

func registerSaveDraft(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_draft",
		Description: "Create a complete Proton Mail draft, or replace an existing draft when uid and uidvalidity are provided. Supports plain text, an optional HTML alternative, and bounded base64 attachments. Updating appends the replacement before removing the old UID; drafts with reply-thread headers are refused because Bridge cannot preserve them. Inspect warning if cleanup was incomplete.",
		Annotations: saveDraftAnnotations(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveDraftInput) (*mcp.CallToolResult, saveDraftOutput, error) {
		draft, err := toDraft(in)
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
