package mcptools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func msgRefArgs() map[string]any {
	return map[string]any{"folder": "INBOX", "uid": 7, "uidvalidity": 42}
}

func TestGetEmailBodiesInContentOnly(t *testing.T) {
	fake := &fakeBridge{email: &bridgeclient.EmailContent{
		Summary: bridgeclient.EmailSummary{Subject: "hello", From: []string{"a@x"}, Bcc: []string{"hidden@x"}},
		Plain:   &bridgeclient.TextBody{Text: "plain body", Truncated: true},
		HTML:    &bridgeclient.TextBody{Text: "<p>html</p>", CharsetFallback: true},
		Attachments: []bridgeclient.AttachmentInfo{
			{Index: 0, Filename: "a.pdf", ContentType: "application/pdf", EncodedSize: 999},
		},
	}}
	session := newTestSession(t, fake)

	res := callTool(t, session, "get_email", msgRefArgs())
	if res.IsError {
		t.Fatalf("errored: %v", res.Content)
	}
	if len(res.Content) != 2 {
		t.Fatalf("content blocks = %d, want plain + html", len(res.Content))
	}
	first := res.Content[0].(*mcp.TextContent).Text
	second := res.Content[1].(*mcp.TextContent).Text
	if !strings.Contains(first, "plain body") || !strings.Contains(second, "<p>html</p>") {
		t.Errorf("blocks wrong or misordered: %q, %q", first, second)
	}

	raw, _ := json.Marshal(res.StructuredContent)
	var out getEmailOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.TextTruncated || !out.CharsetFallback || out.HTMLTruncated {
		t.Errorf("flags = %+v", out)
	}
	if len(out.Bcc) != 1 || out.Bcc[0] != "hidden@x" {
		t.Errorf("bcc = %+v", out.Bcc)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].Filename != "a.pdf" {
		t.Errorf("attachments = %+v", out.Attachments)
	}
	if strings.Contains(string(raw), "plain body") {
		t.Error("body leaked into structured output")
	}
}

func TestGetEmailNoTextBodies(t *testing.T) {
	fake := &fakeBridge{email: &bridgeclient.EmailContent{
		Summary: bridgeclient.EmailSummary{Subject: "img only"},
	}}
	session := newTestSession(t, fake)
	res := callTool(t, session, "get_email", msgRefArgs())
	if len(res.Content) != 1 || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "no text bodies") {
		t.Errorf("content = %#v", res.Content)
	}
}

func TestGetEmailRequiresUIDValidity(t *testing.T) {
	session := newTestSession(t, &fakeBridge{})
	res := callTool(t, session, "get_email", map[string]any{"folder": "INBOX", "uid": 7})
	if !res.IsError {
		t.Fatal("expected error without uidvalidity")
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "uidvalidity") {
		t.Errorf("error should name uidvalidity: %v", res.Content)
	}
}

func TestListAttachmentsTool(t *testing.T) {
	fake := &fakeBridge{attachments: []bridgeclient.AttachmentInfo{
		{Index: 0, Filename: "x.csv", ContentType: "text/csv", EncodedSize: 10},
	}}
	session := newTestSession(t, fake)
	res := callTool(t, session, "list_attachments", msgRefArgs())
	raw, _ := json.Marshal(res.StructuredContent)
	var out listAttachmentsOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.UID != 7 || out.UIDValidity != 42 || len(out.Attachments) != 1 {
		t.Errorf("out = %+v", out)
	}
}

func TestGetAttachmentImageContent(t *testing.T) {
	fake := &fakeBridge{attachment: &bridgeclient.AttachmentContent{
		Filename: "pic.png", ContentType: "image/png", EncodedSize: 12, Data: []byte{1, 2, 3},
	}}
	session := newTestSession(t, fake)
	args := msgRefArgs()
	args["index"] = 0
	res := callTool(t, session, "get_attachment", args)
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("content = %#v, want ImageContent", res.Content[0])
	}
	if img.MIMEType != "image/png" || len(img.Data) != 3 {
		t.Errorf("image = %+v", img)
	}
}

func TestGetAttachmentTextBase64(t *testing.T) {
	fake := &fakeBridge{attachment: &bridgeclient.AttachmentContent{
		Filename: "doc.pdf", ContentType: "application/pdf", EncodedSize: 12, Data: []byte("PDFDATA"),
	}}
	session := newTestSession(t, fake)
	args := msgRefArgs()
	args["index"] = 0
	res := callTool(t, session, "get_attachment", args)
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "doc.pdf") || !strings.Contains(text, "UERGREFUQQ==") {
		t.Errorf("text block = %q", text)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out getAttachmentOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.DecodedSizeBytes != 7 {
		t.Errorf("out = %+v", out)
	}
}
