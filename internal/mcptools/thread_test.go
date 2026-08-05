package mcptools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func fakeThread() *bridgeclient.Thread {
	return &bridgeclient.Thread{
		Folder:      "All Mail",
		UIDValidity: 99,
		Total:       2,
		Messages: []bridgeclient.ThreadMessage{
			{
				Summary: bridgeclient.EmailSummary{
					UID: 4, Subject: "Plans", From: []string{"a@x"},
					Date: time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
				},
				MessageID: "root@test",
				Body:      "first",
			},
			{
				Summary: bridgeclient.EmailSummary{
					UID: 9, Subject: "Re: Plans", From: []string{"b@x"}, Seen: true,
					Date: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
				},
				MessageID:     "reply@test",
				Body:          "<p>second</p>",
				BodyIsHTML:    true,
				BodyTruncated: true,
			},
		},
	}
}

func decodeThread(t *testing.T, res *mcp.CallToolResult) getThreadOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out getThreadOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGetThreadReturnsConversation(t *testing.T) {
	fake := &fakeBridge{thread: fakeThread()}
	args := msgRefArgs()
	args["search_folder"] = "All Mail"
	args["include_bodies"] = true

	res := callTool(t, newTestSession(t, fake), "get_thread", args)
	if res.IsError {
		t.Fatalf("errored: %v", res.Content)
	}
	if len(res.Content) != 0 {
		t.Errorf("content = %#v, want none", res.Content)
	}

	out := decodeThread(t, res)
	if out.Folder != "All Mail" || out.UIDValidity != 99 {
		t.Errorf("folder/uidvalidity = %q/%d", out.Folder, out.UIDValidity)
	}
	if out.Total != 2 || out.Returned != 2 {
		t.Errorf("total=%d returned=%d", out.Total, out.Returned)
	}
	if out.Messages[0].MessageID != "root@test" || out.Messages[1].MessageID != "reply@test" {
		t.Errorf("messages = %+v", out.Messages)
	}
	if out.Messages[0].Date != "2026-07-01T10:00:00Z" {
		t.Errorf("date = %q", out.Messages[0].Date)
	}
	if !out.Messages[1].BodyIsHTML || !out.Messages[1].BodyTruncated || !out.Messages[1].Seen {
		t.Errorf("flags lost: %+v", out.Messages[1])
	}
}

func TestGetThreadPassesRequestToBridge(t *testing.T) {
	fake := &fakeBridge{thread: fakeThread()}
	args := msgRefArgs()
	args["search_folder"] = "All Mail"
	args["include_bodies"] = true
	callTool(t, newTestSession(t, fake), "get_thread", args)

	want := bridgeclient.ThreadRef{
		Folder: "INBOX", SearchFolder: "All Mail",
		UID: 7, UIDValidity: 42, IncludeBodies: true,
	}
	if fake.gotThread != want {
		t.Errorf("bridge got %+v, want %+v", fake.gotThread, want)
	}
}

// An omitted search_folder reaches the bridge empty, which is what makes it
// default to the starting message's folder.
func TestGetThreadDefaultsSearchFolder(t *testing.T) {
	fake := &fakeBridge{thread: fakeThread()}
	callTool(t, newTestSession(t, fake), "get_thread", msgRefArgs())

	if fake.gotThread.SearchFolder != "" {
		t.Errorf("search_folder = %q, want empty", fake.gotThread.SearchFolder)
	}
	if fake.gotThread.IncludeBodies {
		t.Error("include_bodies should default to false")
	}
}

func TestGetThreadOmitsBodiesByDefault(t *testing.T) {
	thread := fakeThread()
	for i := range thread.Messages {
		thread.Messages[i].Body = ""
		thread.Messages[i].BodyIsHTML = false
		thread.Messages[i].BodyTruncated = false
	}
	res := callTool(t, newTestSession(t, &fakeBridge{thread: thread}), "get_thread", msgRefArgs())

	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), `"body"`) {
		t.Errorf("body present without include_bodies: %s", raw)
	}
}

func TestGetThreadRequiresUIDValidity(t *testing.T) {
	res := callTool(t, newTestSession(t, &fakeBridge{}), "get_thread",
		map[string]any{"folder": "INBOX", "uid": 7})
	if !res.IsError {
		t.Fatal("expected error without uidvalidity")
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "uidvalidity") {
		t.Errorf("error should name uidvalidity: %v", res.Content)
	}
}

func TestGetThreadIsRegisteredReadOnly(t *testing.T) {
	tools, err := newTestSession(t, &fakeBridge{}).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "get_thread" {
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Error("get_thread must carry ReadOnlyHint: true")
		}
		return
	}
	t.Fatal("get_thread not registered")
}
