package mcptools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// newIdentitySession wires a server that knows the test identities and,
// optionally, an attachment boundary.
func newIdentitySession(t *testing.T, bridge bridgeclient.Bridge, roots ...string) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "baryon-mcp", Version: "test"}, nil)
	RegisterAll(server, bridge, Options{Identities: testIdentities, AttachmentRoots: roots})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func decodeInto[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return out
}

func parentEmail() *bridgeclient.EmailContent {
	return &bridgeclient.EmailContent{
		Summary: bridgeclient.EmailSummary{
			Subject: "Quarterly numbers",
			From:    []string{"Alice <alice@example.org>"},
			To:      []string{"Me Myself <me@proton.me>", "Bob <bob@example.org>"},
			Cc:      []string{"Carol <carol@example.org>"},
		},
		MessageID:  "parent@example.org",
		References: []string{"root@example.org"},
		Plain:      &bridgeclient.TextBody{Text: "the original body"},
	}
}

func replyArgs(extra map[string]any) map[string]any {
	args := map[string]any{"folder": "INBOX", "uid": 7, "uidvalidity": 42}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func TestSaveReplyDraftDerivesTheWholeHeader(t *testing.T) {
	fake := &fakeBridge{
		email:      parentEmail(),
		savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 11, UIDValidity: 5},
	}
	session := newIdentitySession(t, fake)

	res := callTool(t, session, "save_reply_draft", replyArgs(map[string]any{
		"reply_all": true,
		"bcc":       []string{"archive@example.org"},
		"text_body": "Thanks, noted.",
	}))
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	out := decodeInto[saveReplyDraftOutput](t, res)

	if out.UID != 11 || out.Folder != "Drafts" {
		t.Errorf("saved draft = %+v", out)
	}
	if !equalStrings(out.To, []string{"Alice <alice@example.org>"}) {
		t.Errorf("to = %q", out.To)
	}
	if !equalStrings(out.Cc, []string{"Bob <bob@example.org>", "Carol <carol@example.org>"}) {
		t.Errorf("cc = %q", out.Cc)
	}
	if out.Subject != "Re: Quarterly numbers" {
		t.Errorf("subject = %q", out.Subject)
	}
	if !equalStrings(out.References, []string{"root@example.org", "parent@example.org"}) {
		t.Errorf("references = %q", out.References)
	}
	if out.ContentTrust != contentTrustUntrusted {
		t.Errorf("content_trust = %q, want %q", out.ContentTrust, contentTrustUntrusted)
	}

	saved := fake.gotDraft
	if saved.TextBody != "Thanks, noted." {
		t.Errorf("text body = %q", saved.TextBody)
	}
	if strings.Contains(saved.TextBody, "the original body") {
		t.Error("the original body was quoted into the reply")
	}
	if len(saved.Attachments) != 0 {
		t.Error("the original's attachments were copied into the reply")
	}
	if saved.Replace != nil {
		t.Error("save_reply_draft must create a draft, never replace one")
	}
}

func TestSaveReplyDraftValidatesItsReference(t *testing.T) {
	session := newIdentitySession(t, &fakeBridge{email: parentEmail()})
	for _, args := range []map[string]any{
		{"folder": "", "uid": 7, "uidvalidity": 42},
		{"folder": "INBOX", "uid": 0, "uidvalidity": 42},
		{"folder": "INBOX", "uid": 7, "uidvalidity": 0},
	} {
		res := callTool(t, session, "save_reply_draft", args)
		if !res.IsError {
			t.Errorf("args %v: expected an error result", args)
		}
	}
}

func TestSaveReplyDraftSurfacesStaleUIDValidity(t *testing.T) {
	fake := &fakeBridge{emailErr: errors.New("folder \"INBOX\" UIDVALIDITY changed (now 43, expected 42)")}
	session := newIdentitySession(t, fake)

	res := callTool(t, session, "save_reply_draft", replyArgs(nil))
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "UIDVALIDITY") {
		t.Errorf("error %q should report the stale reference", text)
	}
	if fake.saveDraftCalls != 0 {
		t.Error("a draft was saved despite the stale reference")
	}
}

func TestSaveReplyDraftReportsBridgeFailure(t *testing.T) {
	fake := &fakeBridge{email: parentEmail(), saveDraftErr: errors.New("bridge rejected the draft")}
	session := newIdentitySession(t, fake)

	res := callTool(t, session, "save_reply_draft", replyArgs(nil))
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "bridge rejected the draft") {
		t.Errorf("error %q should surface the bridge failure", text)
	}
}

func TestSaveReplyDraftRefusesAnUnknownFrom(t *testing.T) {
	fake := &fakeBridge{email: parentEmail()}
	session := newIdentitySession(t, fake)

	res := callTool(t, session, "save_reply_draft", replyArgs(map[string]any{"from": "someone@elsewhere.test"}))
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if fake.saveDraftCalls != 0 {
		t.Error("a draft was saved with an unconfigured sender address")
	}
}

func TestSaveReplyDraftReadsAttachmentsFromTheAttachmentRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeBridge{
		email:      parentEmail(),
		savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 12, UIDValidity: 5},
	}
	session := newIdentitySession(t, fake, root)

	res := callTool(t, session, "save_reply_draft", replyArgs(map[string]any{
		"attachments": []map[string]any{{"content_path": "report.txt"}},
	}))
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	if len(fake.gotDraft.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want the relative path resolved inside the root", fake.gotDraft.Attachments)
	}
	got := fake.gotDraft.Attachments[0]
	if got.Filename != "report.txt" || string(got.Data) != "hello" {
		t.Errorf("attachment = %+v", got)
	}

	res = callTool(t, session, "save_reply_draft", replyArgs(map[string]any{
		"attachments": []map[string]any{{"content_path": "../escape.txt"}},
	}))
	if !res.IsError {
		t.Fatal("a relative path escaping the root must be refused")
	}
}

func TestSaveReplyDraftAcceptsInlineAttachments(t *testing.T) {
	fake := &fakeBridge{
		email:      parentEmail(),
		savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 13, UIDValidity: 5},
	}
	session := newIdentitySession(t, fake)

	res := callTool(t, session, "save_reply_draft", replyArgs(map[string]any{
		"attachments": []map[string]any{{
			"filename":       "note.txt",
			"content_type":   "text/plain",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("inline")),
		}},
	}))
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	if len(fake.gotDraft.Attachments) != 1 || string(fake.gotDraft.Attachments[0].Data) != "inline" {
		t.Errorf("attachments = %+v", fake.gotDraft.Attachments)
	}
}

func TestListSenderIdentities(t *testing.T) {
	session := newIdentitySession(t, &fakeBridge{})
	out := decodeInto[listSenderIdentitiesOutput](t, callTool(t, session, "list_sender_identities", nil))
	if len(out.Identities) != 2 {
		t.Fatalf("identities = %+v", out.Identities)
	}
	if out.Identities[0].Address != "me@proton.me" || !out.Identities[0].Default {
		t.Errorf("first identity = %+v, want the default", out.Identities[0])
	}
	if out.Identities[0].Name != "Me Myself" {
		t.Errorf("name = %q", out.Identities[0].Name)
	}
	if out.Identities[1].Default {
		t.Error("only the first identity is the default")
	}

	empty := decodeInto[listSenderIdentitiesOutput](t, callTool(t, newTestSession(t, &fakeBridge{}), "list_sender_identities", nil))
	if len(empty.Identities) != 0 {
		t.Errorf("unconfigured identities = %+v, want none", empty.Identities)
	}
}

func TestSaveReplyDraftIsAnnotatedAsAnAdditiveWrite(t *testing.T) {
	session := newIdentitySession(t, &fakeBridge{})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "save_reply_draft" {
			continue
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
			t.Error("save_reply_draft writes to the mailbox and must not claim to be read-only")
		}
		// It only appends, so destructiveHint must not gate it.
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Error("save_reply_draft only appends and must not be annotated destructive")
		}
		return
	}
	t.Fatal("save_reply_draft not registered")
}

// save_draft keeps the destructive hint, because replacing a draft expunges
// the previous one.
func TestSaveDraftStaysAnnotatedDestructive(t *testing.T) {
	session := newIdentitySession(t, &fakeBridge{})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "save_draft" {
			continue
		}
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Error("save_draft can replace a draft and must stay annotated destructive")
		}
		return
	}
	t.Fatal("save_draft not registered")
}
