package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// fakeBridge implements bridgeclient.Bridge for handler tests.
type fakeBridge struct {
	folders    []bridgeclient.Folder
	foldersErr error

	page     *bridgeclient.MessagePage
	pageErr  error
	gotQuery struct {
		folder        string
		criteria      bridgeclient.SearchCriteria
		limit, offset int
	}

	email    *bridgeclient.EmailContent
	emailErr error

	attachments    []bridgeclient.AttachmentInfo
	attachmentsErr error

	attachment    *bridgeclient.AttachmentContent
	attachmentErr error

	gotDraft       bridgeclient.Draft
	savedDraft     *bridgeclient.SavedDraft
	saveDraftErr   error
	saveDraftCalls int
}

func (f *fakeBridge) ListFolders(ctx context.Context) ([]bridgeclient.Folder, error) {
	return f.folders, f.foldersErr
}

func (f *fakeBridge) ListMessages(ctx context.Context, folder string, criteria bridgeclient.SearchCriteria, limit, offset int) (*bridgeclient.MessagePage, error) {
	f.gotQuery.folder = folder
	f.gotQuery.criteria = criteria
	f.gotQuery.limit = limit
	f.gotQuery.offset = offset
	if f.page == nil {
		return &bridgeclient.MessagePage{}, f.pageErr
	}
	return f.page, f.pageErr
}

func (f *fakeBridge) GetEmail(ctx context.Context, folder string, uid, uidvalidity uint32) (*bridgeclient.EmailContent, error) {
	if f.email == nil {
		return nil, f.emailErr
	}
	return f.email, f.emailErr
}

func (f *fakeBridge) ListAttachments(ctx context.Context, folder string, uid, uidvalidity uint32) ([]bridgeclient.AttachmentInfo, error) {
	return f.attachments, f.attachmentsErr
}

func (f *fakeBridge) GetAttachment(ctx context.Context, folder string, uid, uidvalidity uint32, index int) (*bridgeclient.AttachmentContent, error) {
	if f.attachment == nil {
		return nil, f.attachmentErr
	}
	return f.attachment, f.attachmentErr
}

func (f *fakeBridge) SaveDraft(ctx context.Context, draft bridgeclient.Draft) (*bridgeclient.SavedDraft, error) {
	f.saveDraftCalls++
	f.gotDraft = draft
	if f.savedDraft == nil {
		return nil, f.saveDraftErr
	}
	return f.savedDraft, f.saveDraftErr
}

// newTestSession wires a server with the given bridge to an in-memory client
// session.
func newTestSession(t *testing.T, bridge bridgeclient.Bridge) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "baryon-mcp", Version: "test"}, nil)
	RegisterAll(server, bridge, Options{})

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

func TestListFoldersToolIsRegisteredReadOnly(t *testing.T) {
	session := newTestSession(t, &fakeBridge{})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var found *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "list_folders" {
			found = tool
		}
	}
	if found == nil {
		t.Fatal("list_folders not registered")
	}
	if found.Annotations == nil || !found.Annotations.ReadOnlyHint {
		t.Error("list_folders must carry ReadOnlyHint: true")
	}
	if found.Annotations.OpenWorldHint == nil || *found.Annotations.OpenWorldHint {
		t.Error("list_folders should declare a closed world")
	}
}

func TestListFoldersReturnsFolders(t *testing.T) {
	session := newTestSession(t, &fakeBridge{folders: []bridgeclient.Folder{
		{Name: "INBOX", Delimiter: "/", Attributes: []string{"\\HasNoChildren"}},
		{Name: "Folders/Receipts", Delimiter: "/"},
	}})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_folders"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %v", res.Content)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out listFoldersOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	if len(out.Folders) != 2 || out.Folders[0].Name != "INBOX" || out.Folders[1].Name != "Folders/Receipts" {
		t.Errorf("unexpected folders: %+v", out.Folders)
	}
}

func TestListFoldersSurfacesBridgeError(t *testing.T) {
	session := newTestSession(t, &fakeBridge{foldersErr: errors.New("bridge login failed")})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_folders"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError result")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "bridge login failed") {
		t.Errorf("expected error text in content, got %#v", res.Content)
	}
}
