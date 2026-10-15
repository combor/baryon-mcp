package mcptools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
)

type saveForwardDraftInput struct {
	messageRef
	From     string   `json:"from,omitempty" jsonschema:"sender address; must be one from list_sender_identities. Omit to let the server choose the address the message was sent to, falling back to the default identity"`
	To       []string `json:"to,omitempty" jsonschema:"recipients of the forward; they can also be added in Proton Mail before sending"`
	Cc       []string `json:"cc,omitempty"`
	Bcc      []string `json:"bcc,omitempty"`
	TextBody string   `json:"text_body,omitempty" jsonschema:"note placed above the forwarded message, up to 50000 characters"`
	HTMLBody string   `json:"html_body,omitempty" jsonschema:"optional HTML version of the note, up to 50000 characters"`
}

type saveForwardDraftOutput struct {
	Folder       string   `json:"folder" jsonschema:"the Drafts folder the forward was saved in"`
	UID          uint32   `json:"uid" jsonschema:"UID of the saved draft"`
	UIDValidity  uint32   `json:"uidvalidity" jsonschema:"Drafts folder generation accompanying uid"`
	From         string   `json:"from"`
	To           []string `json:"to,omitempty"`
	Cc           []string `json:"cc,omitempty"`
	Bcc          []string `json:"bcc,omitempty"`
	Subject      string   `json:"subject"`
	Attachments  []string `json:"attachments,omitempty" jsonschema:"filenames carried from the original, inline images included"`
	Warning      string   `json:"warning,omitempty"`
	ContentTrust string   `json:"content_trust" jsonschema:"always untrusted_email: the subject, body and files carried into the draft were written by the original's sender. Review the draft before it is sent"`
}

// registerSaveForwardDraft adds the forward helper. The original is read and
// written back inside the server, so its bytes never reach the conversation.
func registerSaveForwardDraft(server *mcp.Server, bridge bridgeclient.Bridge, identities []config.Identity) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "save_forward_draft",
		Description: "Save a forward of one message as a new draft. The original's text and HTML are copied whole below a \"Forwarded message\" header block, and every attachment travels with it, embedded images still inline; none of it passes through this conversation. text_body adds a note above the original, and the subject gains \"Fwd: \". Name recipients here or add them in Proton Mail. The draft is only saved: sending it stays with you. An original whose body is above 2 MiB, or whose attachments exceed 25 MB or 100 files, is refused rather than cut short." + untrustedNote,
		Annotations: draftAnnotations("Save forward draft", false),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveForwardDraftInput) (*mcp.CallToolResult, saveForwardDraftOutput, error) {
		if err := in.validate(); err != nil {
			return nil, saveForwardDraftOutput{}, err
		}
		if err := checkBodyLengths(in.TextBody, in.HTMLBody); err != nil {
			return nil, saveForwardDraftOutput{}, err
		}
		source, err := bridge.GetMessageSource(ctx, in.Folder, in.UID, in.UIDValidity, bridgeclient.AllParts)
		if err != nil {
			return nil, saveForwardDraftOutput{}, err
		}
		if source == nil {
			return nil, saveForwardDraftOutput{}, fmt.Errorf("bridge returned no message for uid %d", in.UID)
		}

		original := &source.Content
		from, err := selectFrom(replySource{
			From: original.Summary.From,
			To:   original.Summary.To,
			Cc:   original.Summary.Cc,
			Bcc:  original.Summary.Bcc,
		}, in.From, identities, selfAddresses(identities))
		if err != nil {
			return nil, saveForwardDraftOutput{}, err
		}
		plain, html := forwardBodies(original, in.TextBody, in.HTMLBody)
		draft := bridgeclient.Draft{
			From:        from,
			To:          in.To,
			Cc:          in.Cc,
			Bcc:         in.Bcc,
			Subject:     forwardSubject(original.Summary.Subject),
			TextBody:    plain,
			HTMLBody:    html,
			Attachments: source.Attachments,
		}

		saved, err := bridge.SaveDraft(ctx, draft)
		if err != nil {
			return nil, saveForwardDraftOutput{}, err
		}
		out := saveForwardDraftOutput{
			Folder:       saved.Folder,
			UID:          saved.UID,
			UIDValidity:  saved.UIDValidity,
			From:         draft.From,
			To:           draft.To,
			Cc:           draft.Cc,
			Bcc:          draft.Bcc,
			Subject:      draft.Subject,
			Warning:      carriedWarning(source),
			ContentTrust: contentTrustUntrusted,
		}
		for _, attachment := range draft.Attachments {
			out.Attachments = append(out.Attachments, attachment.Filename)
		}
		return nil, out, nil
	})
}
