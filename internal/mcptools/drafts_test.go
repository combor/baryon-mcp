package mcptools

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestSaveDraftToolMapsThreadHeaders(t *testing.T) {
	fake := &fakeBridge{savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 9, UIDValidity: 42}}
	res := callTool(t, newTestSession(t, fake), "save_draft", map[string]any{
		"from": "alice@example.org", "to": []string{"bob@example.org"},
		"subject": "Re: plans", "text_body": "reply",
		"in_reply_to": []string{"parent@example.org"},
		"references":  []string{"root@example.org", "parent@example.org"},
	})
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	if !slices.Equal(fake.gotDraft.InReplyTo, []string{"parent@example.org"}) {
		t.Errorf("in-reply-to = %v", fake.gotDraft.InReplyTo)
	}
	if !slices.Equal(fake.gotDraft.References, []string{"root@example.org", "parent@example.org"}) {
		t.Errorf("references = %v", fake.gotDraft.References)
	}
}

// An empty array asks to detach a draft from its thread; an absent field asks to
// leave it alone. Collapsing the two would silently ignore one of the requests.
func TestSaveDraftToolDistinguishesEmptyThreadHeadersFromAbsent(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		wantNil bool
	}{
		{name: "explicit empty clears", args: map[string]any{
			"from": "alice@example.org", "in_reply_to": []string{}, "references": []string{},
		}, wantNil: false},
		{name: "absent keeps", args: map[string]any{"from": "alice@example.org"}, wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBridge{savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 9, UIDValidity: 42}}
			res := callTool(t, newTestSession(t, fake), "save_draft", tt.args)
			if res.IsError {
				t.Fatalf("tool errored: %v", res.Content)
			}
			for field, got := range map[string][]string{
				"in_reply_to": fake.gotDraft.InReplyTo,
				"references":  fake.gotDraft.References,
			} {
				if (got == nil) != tt.wantNil {
					t.Errorf("%s nil = %v, want %v", field, got == nil, tt.wantNil)
				}
				if len(got) != 0 {
					t.Errorf("%s = %v, want no ids", field, got)
				}
			}
		})
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
			ContentBase64: strp(encoded),
		})
	}
	attachments = append(attachments, draftAttachmentInput{
		Filename:      "invalid.bin",
		ContentType:   "application/octet-stream",
		ContentBase64: strp("%%%"),
	})

	_, err := toDraft(saveDraftInput{From: "alice@example.org", Attachments: attachments}, nil)
	if err == nil || !strings.Contains(err.Error(), "attachments total") {
		t.Fatalf("error = %v, want aggregate attachment limit", err)
	}
}

func draftWithAttachments(from string, attachments ...draftAttachmentInput) saveDraftInput {
	return saveDraftInput{From: from, Attachments: attachments}
}

func strp(s string) *string { return &s }

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSaveDraftToolAttachesFromPathAndBase64(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "report.pdf", []byte("%PDF-fake"))
	fake := &fakeBridge{savedDraft: &bridgeclient.SavedDraft{Folder: "Drafts", UID: 3, UIDValidity: 7}}
	session := newTestSession(t, fake)
	res := callTool(t, session, "save_draft", map[string]any{
		"from": "alice@example.org",
		"attachments": []map[string]any{
			{"content_path": path},
			{"content_path": path, "filename": "renamed.bin", "content_type": "application/x-test"},
			{"filename": "note.txt", "content_type": "text/plain",
				"content_base64": base64.StdEncoding.EncodeToString([]byte("data"))},
			{"filename": "empty.bin", "content_type": "application/octet-stream", "content_base64": ""},
		},
	})
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}
	atts := fake.gotDraft.Attachments
	if len(atts) != 4 {
		t.Fatalf("attachments = %+v", atts)
	}
	if atts[0].Filename != "report.pdf" || atts[0].ContentType != "application/pdf" || string(atts[0].Data) != "%PDF-fake" {
		t.Errorf("path attachment with defaults = %+v", atts[0])
	}
	if atts[1].Filename != "renamed.bin" || atts[1].ContentType != "application/x-test" {
		t.Errorf("path attachment with overrides = %+v", atts[1])
	}
	if atts[2].Filename != "note.txt" || string(atts[2].Data) != "data" {
		t.Errorf("base64 attachment = %+v", atts[2])
	}
	if atts[3].Filename != "empty.bin" || len(atts[3].Data) != 0 {
		t.Errorf("zero-byte base64 attachment = %+v", atts[3])
	}
}

func TestToDraftRejectsAmbiguousAttachmentSource(t *testing.T) {
	for _, attachment := range []draftAttachmentInput{
		{Filename: "x", ContentType: "text/plain"},
		{Filename: "x", ContentType: "text/plain", ContentBase64: strp("aGk="), ContentPath: strp("/tmp/x")},
	} {
		_, err := toDraft(draftWithAttachments("alice@example.org", attachment), nil)
		if err == nil || !strings.Contains(err.Error(), "exactly one of content_base64 or content_path") {
			t.Errorf("attachment %+v: error = %v, want source conflict", attachment, err)
		}
	}
}

func TestToDraftRejectsBadAttachmentPaths(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ path, want string }{
		{"relative.pdf", "absolute path"},
		{"//server/share/file.pdf", "UNC or device path"},
		{filepath.Join(dir, "missing.pdf"), "missing.pdf"},
		{dir, "regular file"},
	} {
		_, err := toDraft(draftWithAttachments("alice@example.org", draftAttachmentInput{ContentPath: strp(tc.path)}), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("path %q: error = %v, want mention of %q", tc.path, err, tc.want)
		}
	}
}

func TestToDraftEnforcesAttachmentRoots(t *testing.T) {
	resolve := func(dir string) string {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	root := resolve(t.TempDir())
	outside := resolve(t.TempDir())
	inside := writeTestFile(t, root, "in.txt", []byte("in"))
	outsideFile := writeTestFile(t, outside, "out.txt", []byte("out"))
	escape := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outsideFile, escape); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	draft, err := toDraft(draftWithAttachments("alice@example.org", draftAttachmentInput{ContentPath: strp(inside)}), []string{root})
	if err != nil || string(draft.Attachments[0].Data) != "in" {
		t.Errorf("inside root: (%+v, %v), want success", draft.Attachments, err)
	}
	for _, path := range []string{outsideFile, escape} {
		_, err := toDraft(draftWithAttachments("alice@example.org", draftAttachmentInput{ContentPath: strp(path)}), []string{root})
		if err == nil || !strings.Contains(err.Error(), "outside the directories") {
			t.Errorf("path %q: error = %v, want root restriction", path, err)
		}
	}
}

func TestToDraftAcceptsCaseInsensitiveSpellingInsideRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "Docs")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, sub, "a.txt", []byte("in"))
	respelled := filepath.Join(root, "dOCS", "a.txt")
	if _, err := os.Stat(respelled); err != nil {
		t.Skipf("volume is case-sensitive: %v", err)
	}

	draft, err := toDraft(draftWithAttachments("alice@example.org", draftAttachmentInput{ContentPath: strp(respelled)}), []string{root})
	if err != nil || string(draft.Attachments[0].Data) != "in" {
		t.Errorf("respelled path inside root: (%+v, %v), want success", draft.Attachments, err)
	}
}

func TestToDraftChecksPathAttachmentSizeBeforeReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: over the per-attachment limit without writing the bytes.
	if err := f.Truncate(bridgeclient.MaxDraftAttachmentBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = toDraft(draftWithAttachments("alice@example.org", draftAttachmentInput{ContentPath: strp(path)}), nil)
	if err == nil || !strings.Contains(err.Error(), "byte limit") || !strings.Contains(err.Error(), "big.bin") {
		t.Fatalf("error = %v, want per-attachment size limit naming the path", err)
	}
}

func TestToDraftCountsPathAndBase64TowardTotalLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "half.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(bridgeclient.MaxDraftAttachmentTotalBytes / 2); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(make([]byte, bridgeclient.MaxDraftAttachmentTotalBytes/2+1))

	_, err = toDraft(draftWithAttachments("alice@example.org",
		draftAttachmentInput{ContentPath: strp(path)},
		draftAttachmentInput{Filename: "rest.bin", ContentType: "application/octet-stream", ContentBase64: strp(encoded)},
	), nil)
	if err == nil || !strings.Contains(err.Error(), "attachments total") {
		t.Fatalf("error = %v, want aggregate attachment limit across sources", err)
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
			ContentBase64: strp(encoded),
		}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "decoded content") {
		t.Fatalf("error = %v, want exact decoded attachment limit", err)
	}
}
