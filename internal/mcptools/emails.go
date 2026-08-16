package mcptools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// pageInput is the pagination half of both listing tools: a legacy offset, or
// the UID cursor that survives mail arriving between calls.
type pageInput struct {
	Limit       int    `json:"limit,omitempty" jsonschema:"max messages to return (default 20, max 100)"`
	Offset      int    `json:"offset,omitempty" jsonschema:"newest messages to skip; simple but unstable, since mail arriving between calls shifts it. Prefer before_uid"`
	BeforeUID   uint32 `json:"before_uid,omitempty" jsonschema:"stable cursor: return only messages below this uid, taking next_before_uid from the previous page. Requires uidvalidity and cannot be combined with offset"`
	UIDValidity uint32 `json:"uidvalidity,omitempty" jsonschema:"folder generation the cursor belongs to, as returned alongside it; the call fails rather than paging a replaced mailbox"`
}

func (p pageInput) toRequest() (bridgeclient.PageRequest, error) {
	if (p.BeforeUID == 0) != (p.UIDValidity == 0) {
		return bridgeclient.PageRequest{}, fmt.Errorf("before_uid and uidvalidity must be supplied together; use the next_before_uid and uidvalidity of the previous page")
	}
	if p.BeforeUID != 0 && p.Offset != 0 {
		return bridgeclient.PageRequest{}, fmt.Errorf("before_uid and offset cannot be combined; page with one or the other")
	}
	return bridgeclient.PageRequest{
		Limit:       clampLimit(p.Limit),
		Offset:      clampOffset(p.Offset),
		BeforeUID:   p.BeforeUID,
		UIDValidity: p.UIDValidity,
	}, nil
}

type listEmailsInput struct {
	Folder     string `json:"folder" jsonschema:"folder name, as returned by list_folders"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only return unread messages"`
	pageInput
}

type searchEmailsInput struct {
	Folder     string `json:"folder" jsonschema:"folder name, as returned by list_folders"`
	Query      string `json:"query,omitempty" jsonschema:"free-text search over message headers and body"`
	From       string `json:"from,omitempty" jsonschema:"match sender address or name"`
	To         string `json:"to,omitempty" jsonschema:"match recipient address or name"`
	Subject    string `json:"subject,omitempty" jsonschema:"match subject text"`
	Since      string `json:"since,omitempty" jsonschema:"received on or after this date, YYYY-MM-DD"`
	Before     string `json:"before,omitempty" jsonschema:"received strictly before this date, YYYY-MM-DD"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only return unread messages"`
	pageInput
}

type emailPageOutput struct {
	Folder        string         `json:"folder"`
	UIDValidity   uint32         `json:"uidvalidity" jsonschema:"folder generation; pass to get_email and attachment tools together with uid"`
	Total         int            `json:"total" jsonschema:"total messages matching, before pagination"`
	Returned      int            `json:"returned"`
	NextBeforeUID uint32         `json:"next_before_uid,omitempty" jsonschema:"pass as before_uid, with uidvalidity, for the next page; absent at the end of the results"`
	ContentTrust  string         `json:"content_trust" jsonschema:"always untrusted_email: subjects and addresses below are written by the sender"`
	Emails        []emailSummary `json:"emails" jsonschema:"newest first"`
}

func fetchPage(ctx context.Context, bridge bridgeclient.Bridge, folder string, crit bridgeclient.SearchCriteria, page pageInput) (emailPageOutput, error) {
	if folder == "" {
		return emailPageOutput{}, fmt.Errorf("folder is required")
	}
	req, err := page.toRequest()
	if err != nil {
		return emailPageOutput{}, err
	}
	result, err := bridge.ListMessages(ctx, folder, crit, req)
	if err != nil {
		return emailPageOutput{}, err
	}
	emails := toEmailSummaries(result.Emails)
	return emailPageOutput{
		Folder:        folder,
		UIDValidity:   result.UIDValidity,
		Total:         result.Total,
		Returned:      len(emails),
		NextBeforeUID: result.NextBeforeUID,
		ContentTrust:  contentTrustUntrusted,
		Emails:        emails,
	}, nil
}

func parseDay(name, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a YYYY-MM-DD date, got %q", name, v)
	}
	return t, nil
}

func registerListEmails(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_emails",
		Description: "List messages in a folder, newest first, with pagination. Returns envelope summaries (subject, sender, date, message_id, flags) and the folder's uidvalidity. Page with next_before_uid rather than offset so newly arrived mail cannot shift the results." + untrustedNote,
		Annotations: readOnly("List emails"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listEmailsInput) (*mcp.CallToolResult, emailPageOutput, error) {
		out, err := fetchPage(ctx, bridge, in.Folder, bridgeclient.SearchCriteria{UnreadOnly: in.UnreadOnly}, in.pageInput)
		return nil, out, err
	})
}

func registerSearchEmails(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_emails",
		Description: "Search messages in a folder by text, sender, recipient, subject, date range, or unread state. Returns envelope summaries newest first, with pagination. Search the All Mail folder to look across the whole mailbox at once." + untrustedNote,
		Annotations: readOnly("Search emails"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchEmailsInput) (*mcp.CallToolResult, emailPageOutput, error) {
		since, err := parseDay("since", in.Since)
		if err != nil {
			return nil, emailPageOutput{}, err
		}
		before, err := parseDay("before", in.Before)
		if err != nil {
			return nil, emailPageOutput{}, err
		}
		crit := bridgeclient.SearchCriteria{
			Query:      in.Query,
			From:       in.From,
			To:         in.To,
			Subject:    in.Subject,
			Since:      since,
			Before:     before,
			UnreadOnly: in.UnreadOnly,
		}
		out, err := fetchPage(ctx, bridge, in.Folder, crit, in.pageInput)
		return nil, out, err
	})
}
