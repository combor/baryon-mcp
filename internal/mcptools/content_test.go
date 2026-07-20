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

func TestGetEmailBodiesInStructuredOutput(t *testing.T) {
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
	if len(res.Content) != 0 {
		t.Errorf("content blocks = %d, want none (bodies duplicated on the wire): %#v", len(res.Content), res.Content)
	}

	raw, _ := json.Marshal(res.StructuredContent)
	var out getEmailOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.PlainBody != "plain body" || out.HTMLBody != "<p>html</p>" {
		t.Errorf("bodies = %q, %q", out.PlainBody, out.HTMLBody)
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
}

func TestGetEmailNoTextBodies(t *testing.T) {
	fake := &fakeBridge{email: &bridgeclient.EmailContent{
		Summary: bridgeclient.EmailSummary{Subject: "img only"},
	}}
	session := newTestSession(t, fake)
	res := callTool(t, session, "get_email", msgRefArgs())
	if len(res.Content) != 0 {
		t.Errorf("content = %#v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "plain_body") || strings.Contains(string(raw), "html_body") {
		t.Errorf("absent bodies should be omitted: %s", raw)
	}
}

func TestLegacyProtocol(t *testing.T) {
	for version, want := range map[string]bool{
		"2024-11-05": true,
		"2025-03-26": true,
		"2025-06-18": false,
		"2025-11-25": false,
	} {
		if got := legacyProtocol(version); got != want {
			t.Errorf("legacyProtocol(%q) = %v, want %v", version, got, want)
		}
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
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "data_base64") {
		t.Errorf("image bytes doubled into structured output: %s", raw)
	}
}

func TestGetAttachmentBase64InStructuredOutput(t *testing.T) {
	fake := &fakeBridge{attachment: &bridgeclient.AttachmentContent{
		Filename: "doc.pdf", ContentType: "application/pdf", EncodedSize: 12, Data: []byte("PDFDATA"),
	}}
	session := newTestSession(t, fake)
	args := msgRefArgs()
	args["index"] = 0
	res := callTool(t, session, "get_attachment", args)
	if len(res.Content) != 0 {
		t.Errorf("content = %#v, want none", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out getAttachmentOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Filename != "doc.pdf" || out.DataBase64 != "UERGREFUQQ==" {
		t.Errorf("out = %+v", out)
	}
	if out.DecodedSizeBytes != 7 {
		t.Errorf("out = %+v", out)
	}
}
