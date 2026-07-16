package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func decodePage(t *testing.T, res *mcp.CallToolResult) emailPageOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out emailPageOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestListEmailsClampsPagination(t *testing.T) {
	fake := &fakeBridge{}
	session := newTestSession(t, fake)

	callTool(t, session, "list_emails", map[string]any{"folder": "INBOX"})
	if fake.gotQuery.limit != defaultLimit || fake.gotQuery.offset != 0 {
		t.Errorf("defaults: got limit=%d offset=%d", fake.gotQuery.limit, fake.gotQuery.offset)
	}

	callTool(t, session, "list_emails", map[string]any{"folder": "INBOX", "limit": 1000, "offset": -3})
	if fake.gotQuery.limit != maxLimit || fake.gotQuery.offset != 0 {
		t.Errorf("clamped: got limit=%d offset=%d, want %d and 0", fake.gotQuery.limit, fake.gotQuery.offset, maxLimit)
	}
}

func TestListEmailsMapsPage(t *testing.T) {
	date := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fake := &fakeBridge{page: &bridgeclient.MessagePage{
		UIDValidity: 42,
		Total:       7,
		Emails: []bridgeclient.EmailSummary{
			{UID: 9, Subject: "hi", From: []string{"Alice <a@x>"}, Date: date, Seen: true},
		},
	}}
	session := newTestSession(t, fake)

	out := decodePage(t, callTool(t, session, "list_emails", map[string]any{"folder": "INBOX", "unread_only": true}))
	if out.UIDValidity != 42 || out.Total != 7 || out.Returned != 1 {
		t.Errorf("got %+v", out)
	}
	if out.Emails[0].Date != "2026-07-01T10:00:00Z" {
		t.Errorf("date = %q", out.Emails[0].Date)
	}
	if !fake.gotQuery.criteria.UnreadOnly {
		t.Error("unread_only not passed through")
	}
}

func TestSearchEmailsBuildsCriteria(t *testing.T) {
	fake := &fakeBridge{}
	session := newTestSession(t, fake)

	callTool(t, session, "search_emails", map[string]any{
		"folder": "INBOX", "query": "invoice", "from": "billing@x", "subject": "urgent",
		"since": "2026-01-01", "before": "2026-07-01",
	})
	c := fake.gotQuery.criteria
	if c.Query != "invoice" || c.From != "billing@x" || c.Subject != "urgent" {
		t.Errorf("criteria = %+v", c)
	}
	if c.Since.Format("2006-01-02") != "2026-01-01" || c.Before.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("dates = %v / %v", c.Since, c.Before)
	}
}

func TestSearchEmailsRejectsBadDate(t *testing.T) {
	session := newTestSession(t, &fakeBridge{})
	res := callTool(t, session, "search_emails", map[string]any{"folder": "INBOX", "since": "01/02/2026"})
	if !res.IsError {
		t.Fatal("expected error for bad date")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "YYYY-MM-DD") {
		t.Errorf("error %q should name the expected format", text)
	}
}

func TestListEmailsRequiresFolder(t *testing.T) {
	session := newTestSession(t, &fakeBridge{})
	res := callTool(t, session, "list_emails", map[string]any{"folder": ""})
	if !res.IsError {
		t.Fatal("expected error for empty folder")
	}
}
