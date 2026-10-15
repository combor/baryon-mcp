package mcptools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
)

type saveReplyDraftInput struct {
	messageRef
	From          string                 `json:"from,omitempty" jsonschema:"sender address; must be one from list_sender_identities. Omit to let the server choose the address the message was sent to, falling back to the default identity"`
	ReplyAll      bool                   `json:"reply_all,omitempty" jsonschema:"also copy the original To and Cc recipients, excluding this account's own addresses"`
	Bcc           []string               `json:"bcc,omitempty" jsonschema:"Bcc recipients to add; the original message's Bcc is never carried over"`
	TextBody      string                 `json:"text_body,omitempty" jsonschema:"plain-text body, up to 50000 characters; set quote_original to have the original quoted below it"`
	HTMLBody      string                 `json:"html_body,omitempty" jsonschema:"optional HTML alternative, up to 50000 characters"`
	Attachments   []draftAttachmentInput `json:"attachments,omitempty" jsonschema:"files to attach; the original message's attachments are not copied, apart from the inline images quote_original carries"`
	QuoteOriginal bool                   `json:"quote_original,omitempty" jsonschema:"quote the original whole below the reply, under an On <date>, <sender> wrote: line, with the images its HTML shows inline"`
}

type saveReplyDraftOutput struct {
	Folder       string   `json:"folder" jsonschema:"the Drafts folder the reply was saved in"`
	UID          uint32   `json:"uid" jsonschema:"UID of the saved draft"`
	UIDValidity  uint32   `json:"uidvalidity" jsonschema:"Drafts folder generation accompanying uid"`
	From         string   `json:"from"`
	To           []string `json:"to" jsonschema:"derived from the original Reply-To, From or Sender"`
	Cc           []string `json:"cc,omitempty" jsonschema:"present for a reply_all"`
	Bcc          []string `json:"bcc,omitempty" jsonschema:"only what this call supplied"`
	Subject      string   `json:"subject"`
	InReplyTo    []string `json:"in_reply_to"`
	References   []string `json:"references"`
	Warning      string   `json:"warning,omitempty"`
	ContentTrust string   `json:"content_trust" jsonschema:"always untrusted_email: the recipients and subject are derived from headers the sender wrote. Review them before the draft is sent"`
}

// registerSaveReplyDraft adds the reply helper. It creates drafts only;
// replacing one stays with save_draft, which takes the complete desired state.
func registerSaveReplyDraft(server *mcp.Server, bridge bridgeclient.Bridge, roots attachmentRoots, identities []config.Identity) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_reply_draft",
		Description: "Save a reply to one message as a new draft, deriving recipients, subject and threading headers from it so they are correct without reconstructing them. Set reply_all to copy the original To and Cc as well; this account's own addresses are always removed, and the original Bcc is never carried over. The original is quoted only when quote_original is set, which copies it whole below the reply together with the images its HTML shows inline; its other attachments are never copied. The draft is only saved: sending it stays with you. To replace a draft, or to control every header yourself, use save_draft." + untrustedNote,
		Annotations: draftAnnotations("Save reply draft", false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveReplyDraftInput) (*mcp.CallToolResult, saveReplyDraftOutput, error) {
		if err := in.validate(); err != nil {
			return nil, saveReplyDraftOutput{}, err
		}
		if err := checkBodyLengths(in.TextBody, in.HTMLBody); err != nil {
			return nil, saveReplyDraftOutput{}, err
		}
		source, err := replyParent(ctx, bridge, in)
		if err != nil {
			return nil, saveReplyDraftOutput{}, err
		}
		if source == nil {
			return nil, saveReplyDraftOutput{}, fmt.Errorf("bridge returned no message for uid %d", in.UID)
		}
		parent := &source.Content

		draft, err := buildReply(replySource{
			Subject:    parent.Summary.Subject,
			From:       parent.Summary.From,
			Sender:     parent.Summary.Sender,
			ReplyTo:    parent.Summary.ReplyTo,
			To:         parent.Summary.To,
			Cc:         parent.Summary.Cc,
			Bcc:        parent.Summary.Bcc,
			MessageID:  parent.MessageID,
			InReplyTo:  parent.InReplyTo,
			References: parent.References,
		}, replyOptions{From: in.From, ReplyAll: in.ReplyAll, Bcc: in.Bcc}, identities)
		if err != nil {
			return nil, saveReplyDraftOutput{}, err
		}
		draft.TextBody = in.TextBody
		draft.HTMLBody = in.HTMLBody
		var warning string
		if in.QuoteOriginal {
			draft.TextBody, draft.HTMLBody = quotedReplyBodies(parent, in.TextBody, in.HTMLBody)
			warning = carriedWarning(source)
		}
		if draft.Attachments, err = draftAttachments(in.Attachments, roots); err != nil {
			return nil, saveReplyDraftOutput{}, err
		}
		draft.Attachments = append(draft.Attachments, source.Attachments...)

		saved, err := bridge.SaveDraft(ctx, draft)
		if err != nil {
			return nil, saveReplyDraftOutput{}, err
		}
		return nil, saveReplyDraftOutput{
			Folder:       saved.Folder,
			UID:          saved.UID,
			UIDValidity:  saved.UIDValidity,
			From:         draft.From,
			To:           draft.To,
			Cc:           draft.Cc,
			Bcc:          draft.Bcc,
			Subject:      draft.Subject,
			InReplyTo:    draft.InReplyTo,
			References:   draft.References,
			Warning:      warning,
			ContentTrust: contentTrustUntrusted,
		}, nil
	})
}

// replyParent reads the message being answered. Quoting needs it whole, with
// the inline parts its HTML shows; a plain reply needs only its headers.
func replyParent(ctx context.Context, bridge bridgeclient.Bridge, in saveReplyDraftInput) (*bridgeclient.MessageSource, error) {
	if in.QuoteOriginal {
		return bridge.GetMessageSource(ctx, in.Folder, in.UID, in.UIDValidity, bridgeclient.ReferencedInlineParts)
	}
	parent, err := bridge.GetEmail(ctx, in.Folder, in.UID, in.UIDValidity)
	if err != nil || parent == nil {
		return nil, err
	}
	return &bridgeclient.MessageSource{Content: *parent}, nil
}
