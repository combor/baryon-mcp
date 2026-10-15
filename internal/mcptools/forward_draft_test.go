package mcptools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func forwardSource() *bridgeclient.MessageSource {
	return &bridgeclient.MessageSource{
		Content: bridgeclient.EmailContent{
			Summary: bridgeclient.EmailSummary{
				Subject: "Q3 invoice",
				From:    []string{"Acme Billing <billing@acme.example>"},
				To:      []string{"Work <work@proton.me>"},
			},
			MessageID: "invoice@acme.example",
			Plain:     &bridgeclient.TextBody{Text: "Invoice attached."},
			HTML:      &bridgeclient.TextBody{Text: `<p>Invoice attached. <img src="cid:logo@acme.example"></p>`},
		},
		Attachments: []bridgeclient.DraftAttachment{
			{Filename: "logo.png", ContentType: "image/png", ContentID: "logo@acme.example", Data: []byte("logo")},
			{Filename: "invoice-Q3.pdf", ContentType: "application/pdf", Data: []byte("%PDF")},
		},
	}
}

func forwardArgs(extra map[string]any) map[string]any {
	args := map[string]any{"folder": "INBOX", "uid": 7, "uidvalidity": 42}
	for k, v := range extra {
		args[k] = v
	}
	return args
}

func TestSaveForwardDraftCarriesTheOriginal(t *testing.T) {
	fake := &fakeBridge{source: forwardSource(), savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 12, UIDValidity: 3}}
	session := newIdentitySession(t, fake)

	res := callTool(t, session, "save_forward_draft", forwardArgs(map[string]any{
		"to":        []string{"bob@example.org"},
		"text_body": "Bob, see below.",
	}))
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	if fake.sourceCalls != 1 || fake.gotSourceParts != bridgeclient.AllParts {
		t.Errorf("source calls = %d with %v, want one read of every part", fake.sourceCalls, fake.gotSourceParts)
	}

	draft := fake.gotDraft
	if draft.From != "work@proton.me" {
		t.Errorf("from = %q, want the identity the original was sent to", draft.From)
	}
	if draft.Subject != "Fwd: Q3 invoice" {
		t.Errorf("subject = %q", draft.Subject)
	}
	if len(draft.To) != 1 || draft.To[0] != "bob@example.org" {
		t.Errorf("to = %+v", draft.To)
	}
	if draft.InReplyTo != nil || draft.References != nil {
		t.Errorf("a forward must not thread into the original: in-reply-to %v, references %v", draft.InReplyTo, draft.References)
	}
	if !strings.HasPrefix(draft.TextBody, "Bob, see below.\n\n"+forwardSeparator) || !strings.HasSuffix(draft.TextBody, "Invoice attached.") {
		t.Errorf("text body = %q", draft.TextBody)
	}
	if !strings.HasSuffix(draft.HTMLBody, `<p>Invoice attached. <img src="cid:logo@acme.example"></p>`) {
		t.Errorf("html body = %q", draft.HTMLBody)
	}
	if len(draft.Attachments) != 2 || draft.Attachments[0].ContentID != "logo@acme.example" || string(draft.Attachments[1].Data) != "%PDF" {
		t.Errorf("attachments = %+v", draft.Attachments)
	}

	out := decodeInto[saveForwardDraftOutput](t, res)
	if out.UID != 12 || out.Subject != "Fwd: Q3 invoice" || out.ContentTrust != contentTrustUntrusted {
		t.Errorf("output = %+v", out)
	}
	if strings.Join(out.Attachments, ",") != "logo.png,invoice-Q3.pdf" || out.Warning != "" {
		t.Errorf("output attachments %v, warning %q", out.Attachments, out.Warning)
	}
}

func TestSaveForwardDraftReportsWhatDidNotDecode(t *testing.T) {
	source := forwardSource()
	source.Content.Plain.CharsetFallback = true
	source.Damaged = []string{"invoice-Q3.pdf"}
	fake := &fakeBridge{source: source, savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 12, UIDValidity: 3}}

	out := decodeInto[saveForwardDraftOutput](t, callTool(t, newIdentitySession(t, fake), "save_forward_draft", forwardArgs(nil)))
	if !strings.HasPrefix(out.Warning, decodeWarning+"; ") || !strings.HasSuffix(out.Warning, ": invoice-Q3.pdf") {
		t.Errorf("warning = %q, want the text and the damaged attachment reported", out.Warning)
	}
}

func TestSaveForwardDraftRefusesAnUnknownFrom(t *testing.T) {
	fake := &fakeBridge{source: forwardSource()}
	res := callTool(t, newIdentitySession(t, fake), "save_forward_draft", forwardArgs(map[string]any{"from": "someone@elsewhere.test"}))
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if fake.saveDraftCalls != 0 {
		t.Error("a draft was saved under an unconfigured identity")
	}
}

func TestSaveForwardDraftRejectsAnOversizedNoteBeforeReading(t *testing.T) {
	fake := &fakeBridge{source: forwardSource()}
	res := callTool(t, newIdentitySession(t, fake), "save_forward_draft", forwardArgs(map[string]any{
		"text_body": strings.Repeat("x", bridgeclient.MaxDraftBodyChars+1),
	}))
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if fake.sourceCalls != 0 || fake.saveDraftCalls != 0 {
		t.Errorf("source calls %d, save calls %d; an oversized note must be refused first", fake.sourceCalls, fake.saveDraftCalls)
	}
}

func TestSaveForwardDraftRequiresACompleteReference(t *testing.T) {
	fake := &fakeBridge{source: forwardSource()}
	res := callTool(t, newIdentitySession(t, fake), "save_forward_draft", map[string]any{"folder": "INBOX", "uid": 7})
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	if fake.sourceCalls != 0 {
		t.Error("the original was read without a uidvalidity")
	}
}

func TestSaveForwardDraftReportsBridgeFailures(t *testing.T) {
	for name, fake := range map[string]*fakeBridge{
		"read":  {sourceErr: errors.New("folder \"INBOX\" UIDVALIDITY changed")},
		"write": {source: forwardSource(), saveDraftErr: errors.New("bridge rejected the draft")},
	} {
		t.Run(name, func(t *testing.T) {
			res := callTool(t, newIdentitySession(t, fake), "save_forward_draft", forwardArgs(nil))
			if !res.IsError {
				t.Fatal("expected an error result")
			}
			want := "UIDVALIDITY"
			if name == "write" {
				want = "bridge rejected the draft"
			}
			if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, want) {
				t.Errorf("error %q should surface %q", text, want)
			}
		})
	}
}

func TestSaveForwardDraftIsAnnotatedAsAnAdditiveWrite(t *testing.T) {
	tools, err := newIdentitySession(t, &fakeBridge{}).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "save_forward_draft" {
			continue
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
			t.Error("save_forward_draft writes to the mailbox and must not claim to be read-only")
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Error("save_forward_draft only appends and must not be annotated destructive")
		}
		return
	}
	t.Fatal("save_forward_draft not registered")
}
