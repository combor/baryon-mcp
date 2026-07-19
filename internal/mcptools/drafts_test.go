package mcptools

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func decodeSavedDraft(t *testing.T, res *mcp.CallToolResult) saveDraftOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out saveDraftOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSaveDraftToolAnnotations(t *testing.T) {
	session := newTestSession(t, &fakeBridge{})
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "save_draft" {
			continue
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Fatalf("annotations = %+v", tool.Annotations)
		}
		if tool.Annotations.IdempotentHint {
			t.Error("save_draft must not claim idempotency")
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Error("save_draft should declare a closed world")
		}
		return
	}
	t.Fatal("save_draft not registered")
}

func TestSaveDraftToolMapsCreateInput(t *testing.T) {
	fake := &fakeBridge{savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 9, UIDValidity: 42}}
	session := newTestSession(t, fake)
	res := callTool(t, session, "save_draft", map[string]any{
		"from": "alice@example.org", "to": []string{"bob@example.org"},
		"subject": "hello", "text_body": "plain", "html_body": "<p>plain</p>",
		"attachments": []map[string]any{{
			"filename": "note.txt", "content_type": "text/plain",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("data")),
		}},
	})
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	if fake.gotDraft.From != "alice@example.org" || fake.gotDraft.HTMLBody != "<p>plain</p>" || len(fake.gotDraft.Attachments) != 1 || string(fake.gotDraft.Attachments[0].Data) != "data" {
		t.Errorf("mapped draft = %+v", fake.gotDraft)
	}
	out := decodeSavedDraft(t, res)
	if out.Folder != "Drafts" || out.UID != 9 || out.UIDValidity != 42 {
		t.Errorf("output = %+v", out)
	}
}

func TestSaveDraftToolMapsUpdateAndCleanupWarning(t *testing.T) {
	fake := &fakeBridge{savedDraft: &bridgeclient.SavedDraft{
		Folder: "Drafts", UID: 10, UIDValidity: 42, ReplacedUID: 9,
		PreviousDraftRemoved: false, Warning: "replacement saved but old draft remains",
	}}
	session := newTestSession(t, fake)
	res := callTool(t, session, "save_draft", map[string]any{
		"from": "alice@example.org", "uid": 9, "uidvalidity": 42,
	})
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	if fake.gotDraft.Replace == nil || fake.gotDraft.Replace.UID != 9 || fake.gotDraft.Replace.UIDValidity != 42 {
		t.Errorf("replacement = %+v", fake.gotDraft.Replace)
	}
	out := decodeSavedDraft(t, res)
	if out.ReplacedUID != 9 || out.PreviousDraftRemoved || !strings.Contains(out.Warning, "old draft") {
		t.Errorf("output = %+v", out)
	}
}

func TestSaveDraftToolRejectsPartialReferenceAndInvalidBase64(t *testing.T) {
	for _, args := range []map[string]any{
		{"from": "alice@example.org", "uid": 9},
		{"from": "alice@example.org", "attachments": []map[string]any{{"filename": "x", "content_type": "text/plain", "content_base64": "%%%"}}},
	} {
		fake := &fakeBridge{}
		res := callTool(t, newTestSession(t, fake), "save_draft", args)
		if !res.IsError {
			t.Fatalf("expected error for args %#v", args)
		}
		if fake.saveDraftCalls != 0 {
			t.Error("bridge called for invalid input")
		}
	}
}

func TestToDraftStopsAtTotalAttachmentLimit(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, bridgeclient.MaxDraftAttachmentTotalBytes/2+1))
	attachments := make([]draftAttachmentInput, 0, 3)
	for i := 0; i < 2; i++ {
		attachments = append(attachments, draftAttachmentInput{
			Filename:      "data.bin",
			ContentType:   "application/octet-stream",
			ContentBase64: encoded,
		})
	}
	attachments = append(attachments, draftAttachmentInput{
		Filename:      "invalid.bin",
		ContentType:   "application/octet-stream",
		ContentBase64: "%%%",
	})

	_, err := toDraft(saveDraftInput{From: "alice@example.org", Attachments: attachments})
	if err == nil || !strings.Contains(err.Error(), "attachments total") {
		t.Fatalf("error = %v, want aggregate attachment limit", err)
	}
}

func TestToDraftChecksExactDecodedAttachmentSize(t *testing.T) {
	data := make([]byte, bridgeclient.MaxDraftAttachmentBytes+1)
	encoded := base64.StdEncoding.EncodeToString(data)
	if len(encoded) != base64.StdEncoding.EncodedLen(bridgeclient.MaxDraftAttachmentBytes) {
		t.Fatal("test payload must pass the encoded-length precheck")
	}

	_, err := toDraft(saveDraftInput{
		From: "alice@example.org",
		Attachments: []draftAttachmentInput{{
			Filename:      "data.bin",
			ContentType:   "application/octet-stream",
			ContentBase64: encoded,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "decoded content") {
		t.Fatalf("error = %v, want exact decoded attachment limit", err)
	}
}
