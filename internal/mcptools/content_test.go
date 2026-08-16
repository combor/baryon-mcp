package mcptools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestGetEmailExposesThreadHeaders(t *testing.T) {
	fake := &fakeBridge{email: &bridgeclient.EmailContent{
		Summary:    bridgeclient.EmailSummary{Subject: "Re: plans"},
		MessageID:  "parent@example.org",
		InReplyTo:  []string{"root@example.org"},
		References: []string{"root@example.org"},
	}}
	res := callTool(t, newTestSession(t, fake), "get_email", msgRefArgs())
	if res.IsError {
		t.Fatalf("errored: %v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out getEmailOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.MessageID != "parent@example.org" {
		t.Errorf("message_id = %q", out.MessageID)
	}
	if !slices.Equal(out.InReplyTo, []string{"root@example.org"}) || !slices.Equal(out.References, []string{"root@example.org"}) {
		t.Errorf("in_reply_to = %v, references = %v", out.InReplyTo, out.References)
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
		"2026-07-28": false,
	} {
		if got := legacyProtocol(version); got != want {
			t.Errorf("legacyProtocol(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestLegacyContentReadsPerRequestProtocolVersion(t *testing.T) {
	for version, want := range map[string]bool{
		"2025-03-26": true,
		"2026-07-28": false,
	} {
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
			Meta: mcp.Meta{mcp.MetaKeyProtocolVersion: version},
		}}
		if got := legacyContent(req); got != want {
			t.Errorf("legacyContent(_meta %s) = %v, want %v", version, got, want)
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

func TestSaveAttachmentWritesFileAndWithholdsBytes(t *testing.T) {
	fake := &fakeBridge{attachment: &bridgeclient.AttachmentContent{
		Filename: "big.pdf", ContentType: "application/pdf", EncodedSize: 12, Data: []byte("PDFDATA"),
	}}
	session := newTestSession(t, fake)
	dir := t.TempDir()
	out := filepath.Join(dir, "saved.pdf")
	args := msgRefArgs()
	args["index"] = 0
	args["output_path"] = out

	res := callTool(t, session, "save_attachment", args)
	if res.IsError {
		t.Fatalf("errored: %v", res.Content)
	}
	if len(res.Content) != 0 {
		t.Errorf("content = %#v, want none", res.Content)
	}
	// The whole point: bytes reach disk without passing through the reply.
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "UERGREFUQQ==") || strings.Contains(string(raw), "data_base64") {
		t.Errorf("save_attachment leaked attachment bytes into its result: %s", raw)
	}
	var o saveAttachmentOutput
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if o.SavedPath != filepath.Join(resolveDir(t, dir), "saved.pdf") {
		t.Errorf("saved_path = %q", o.SavedPath)
	}
	if o.Filename != "big.pdf" || o.DecodedSizeBytes != 7 {
		t.Errorf("out = %+v", o)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "PDFDATA" {
		t.Errorf("written file = %q, %v", got, err)
	}
}

func TestSaveAttachmentWritesImagesToDiskToo(t *testing.T) {
	fake := &fakeBridge{attachment: &bridgeclient.AttachmentContent{
		Filename: "pic.png", ContentType: "image/png", EncodedSize: 3, Data: []byte{1, 2, 3},
	}}
	session := newTestSession(t, fake)
	out := filepath.Join(t.TempDir(), "pic.png")
	args := msgRefArgs()
	args["index"] = 0
	args["output_path"] = out

	res := callTool(t, session, "save_attachment", args)
	if res.IsError {
		t.Fatalf("errored: %v", res.Content)
	}
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			t.Error("save_attachment must not also return the image inline")
		}
	}
	if got, err := os.ReadFile(out); err != nil || len(got) != 3 {
		t.Errorf("written png = %v, %v", got, err)
	}
}

// The schema rejects an absent output_path; an empty string satisfies the schema
// and has to be refused in the handler.
func TestSaveAttachmentRequiresOutputPath(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{name: "absent"},
		{name: "empty string", path: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeBridge{attachment: &bridgeclient.AttachmentContent{
				Filename: "doc.pdf", ContentType: "application/pdf", Data: []byte("x"),
			}}
			args := msgRefArgs()
			args["index"] = 0
			if tc.name != "absent" {
				args["output_path"] = tc.path
			}
			res := callTool(t, newTestSession(t, fake), "save_attachment", args)
			if !res.IsError {
				t.Fatalf("expected an error for %s output_path", tc.name)
			}
		})
	}
}

func TestAttachmentToolAnnotationsSplitReadAndWrite(t *testing.T) {
	tools, err := newTestSession(t, &fakeBridge{}).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]*mcp.Tool)
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}

	// get_attachment touches nothing but the mailbox, read-only.
	get, ok := byName["get_attachment"]
	if !ok {
		t.Fatal("get_attachment not registered")
	}
	if get.Annotations == nil || !get.Annotations.ReadOnlyHint || !get.Annotations.IdempotentHint {
		t.Errorf("get_attachment must stay read-only and idempotent: %+v", get.Annotations)
	}

	// save_attachment writes a file, and refuse-if-exists makes a repeat fail.
	save, ok := byName["save_attachment"]
	if !ok {
		t.Fatal("save_attachment not registered")
	}
	if save.Annotations == nil || save.Annotations.ReadOnlyHint {
		t.Error("save_attachment must not claim read-only: it writes a file")
	}
	if save.Annotations.IdempotentHint {
		t.Error("save_attachment must not claim idempotency: refuse-if-exists makes a repeat write fail")
	}
	if save.Annotations.DestructiveHint == nil || *save.Annotations.DestructiveHint {
		t.Error("save_attachment never overwrites, so it should not claim destructiveness")
	}
}

func TestGetEmailExposesSenderAndReplyTo(t *testing.T) {
	fake := &fakeBridge{email: &bridgeclient.EmailContent{
		Summary: bridgeclient.EmailSummary{
			Subject: "Quarterly numbers",
			From:    []string{`"Doe, Alice" <alice@example.org>`},
			Sender:  []string{"secretary@example.org"},
			ReplyTo: []string{"Replies <replies@example.org>"},
			To:      []string{"me@proton.me"},
		},
		MessageID: "parent@example.org",
	}}
	session := newTestSession(t, fake)

	res := callTool(t, session, "get_email", msgRefArgs())
	raw, _ := json.Marshal(res.StructuredContent)
	var out getEmailOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sender) != 1 || out.Sender[0] != "secretary@example.org" {
		t.Errorf("sender = %q", out.Sender)
	}
	if len(out.ReplyTo) != 1 || out.ReplyTo[0] != "Replies <replies@example.org>" {
		t.Errorf("reply_to = %q", out.ReplyTo)
	}
	if len(out.From) != 1 || out.From[0] != `"Doe, Alice" <alice@example.org>` {
		t.Errorf("from = %q, want the address as the bridge formatted it", out.From)
	}
}
