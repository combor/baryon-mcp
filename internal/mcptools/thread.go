package mcptools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type getThreadInput struct {
	messageRef
	SearchFolder  string `json:"search_folder,omitempty" jsonschema:"folder to gather the conversation from; defaults to folder. Pass the All Mail folder to include replies filed elsewhere, such as your own in Sent"`
	IncludeBodies bool   `json:"include_bodies,omitempty" jsonschema:"also return each message's body, trimmed far shorter than get_email returns"`
}

type threadMessageOutput struct {
	UID           uint32   `json:"uid" jsonschema:"pass with uidvalidity to get_email for the full message"`
	Subject       string   `json:"subject"`
	From          []string `json:"from,omitempty"`
	To            []string `json:"to,omitempty"`
	Date          string   `json:"date,omitempty" jsonschema:"send date, RFC 3339"`
	Seen          bool     `json:"seen"`
	MessageID     string   `json:"message_id,omitempty" jsonschema:"RFC 5322 Message-ID without angle brackets"`
	Body          string   `json:"body,omitempty" jsonschema:"present only when include_bodies was set"`
	BodyIsHTML    bool     `json:"body_is_html,omitempty" jsonschema:"the body is HTML because the message carries no plain text part"`
	BodyTruncated bool     `json:"body_truncated,omitempty" jsonschema:"body was cut short; use get_email for the whole message"`
}

type getThreadOutput struct {
	Folder       string                `json:"folder" jsonschema:"folder the conversation was gathered from; the uids below belong to it"`
	UIDValidity  uint32                `json:"uidvalidity" jsonschema:"generation of folder, to pass alongside a uid"`
	Total        int                   `json:"total" jsonschema:"messages in the conversation before the return limit"`
	Returned     int                   `json:"returned"`
	ContentTrust string                `json:"content_trust" jsonschema:"always untrusted_email: every subject, address and body below was written by whoever sent that message"`
	Messages     []threadMessageOutput `json:"messages" jsonschema:"oldest first"`
}

func toThreadMessages(in []bridgeclient.ThreadMessage) []threadMessageOutput {
	out := make([]threadMessageOutput, 0, len(in))
	for _, m := range in {
		msg := threadMessageOutput{
			UID:           m.Summary.UID,
			Subject:       m.Summary.Subject,
			From:          m.Summary.From,
			To:            m.Summary.To,
			Seen:          m.Summary.Seen,
			MessageID:     m.MessageID,
			Body:          m.Body,
			BodyIsHTML:    m.BodyIsHTML,
			BodyTruncated: m.BodyTruncated,
		}
		if !m.Summary.Date.IsZero() {
			msg.Date = m.Summary.Date.Format(time.RFC3339)
		}
		out = append(out, msg)
	}
	return out
}

func registerGetThread(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_thread",
		Description: "Read a whole conversation from one of its messages, oldest first. Returns each message's envelope and message_id, and with include_bodies a shortened body for each. A conversation is usually split across folders, with your own replies in Sent, so pass the All Mail folder as search_folder to gather all of it. Use the returned uid and uidvalidity with get_email for a message in full." + untrustedNote,
		Annotations: readOnly("Get thread"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getThreadInput) (*mcp.CallToolResult, getThreadOutput, error) {
		if err := in.validate(); err != nil {
			return nil, getThreadOutput{}, err
		}
		thread, err := bridge.GetThread(ctx, bridgeclient.ThreadRef{
			Folder:        in.Folder,
			SearchFolder:  in.SearchFolder,
			UID:           in.UID,
			UIDValidity:   in.UIDValidity,
			IncludeBodies: in.IncludeBodies,
		})
		if err != nil {
			return nil, getThreadOutput{}, err
		}
		messages := toThreadMessages(thread.Messages)
		out := getThreadOutput{
			Folder:       thread.Folder,
			UIDValidity:  thread.UIDValidity,
			Total:        thread.Total,
			Returned:     len(messages),
			ContentTrust: contentTrustUntrusted,
			Messages:     messages,
		}
		// Older peers read only content blocks; leaving Content nil has the SDK
		// serialize the structured output into one for them.
		if legacyContent(req) {
			return nil, out, nil
		}
		// Empty non-nil Content stops the SDK echoing the JSON into a redundant text block.
		return &mcp.CallToolResult{Content: []mcp.Content{}}, out, nil
	})
}
