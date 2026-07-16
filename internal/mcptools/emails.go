package mcptools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type listEmailsInput struct {
	Folder     string `json:"folder" jsonschema:"folder name, as returned by list_folders"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max messages to return (default 20, max 100)"`
	Offset     int    `json:"offset,omitempty" jsonschema:"newest messages to skip, for pagination"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only return unread messages"`
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
	Limit      int    `json:"limit,omitempty" jsonschema:"max messages to return (default 20, max 100)"`
	Offset     int    `json:"offset,omitempty" jsonschema:"newest messages to skip, for pagination"`
}

type emailPageOutput struct {
	Folder      string         `json:"folder"`
	UIDValidity uint32         `json:"uidvalidity" jsonschema:"folder generation; pass to get_email and attachment tools together with uid"`
	Total       int            `json:"total" jsonschema:"total messages matching, before pagination"`
	Returned    int            `json:"returned"`
	Emails      []emailSummary `json:"emails" jsonschema:"newest first"`
}

func fetchPage(ctx context.Context, bridge bridgeclient.Bridge, folder string, crit bridgeclient.SearchCriteria, limit, offset int) (emailPageOutput, error) {
	if folder == "" {
		return emailPageOutput{}, fmt.Errorf("folder is required")
	}
	page, err := bridge.ListMessages(ctx, folder, crit, clampLimit(limit), clampOffset(offset))
	if err != nil {
		return emailPageOutput{}, err
	}
	emails := toEmailSummaries(page.Emails)
	return emailPageOutput{
		Folder:      folder,
		UIDValidity: page.UIDValidity,
		Total:       page.Total,
		Returned:    len(emails),
		Emails:      emails,
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
		Description: "List messages in a folder, newest first, with pagination. Returns envelope summaries (subject, sender, date, flags) and the folder's uidvalidity.",
		Annotations: readOnly("List emails"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listEmailsInput) (*mcp.CallToolResult, emailPageOutput, error) {
		out, err := fetchPage(ctx, bridge, in.Folder, bridgeclient.SearchCriteria{UnreadOnly: in.UnreadOnly}, in.Limit, in.Offset)
		return nil, out, err
	})
}

func registerSearchEmails(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_emails",
		Description: "Search messages in a folder by text, sender, recipient, subject, date range, or unread state. Returns envelope summaries newest first, with pagination.",
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
		out, err := fetchPage(ctx, bridge, in.Folder, crit, in.Limit, in.Offset)
		return nil, out, err
	})
}
