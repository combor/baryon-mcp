package bridgeclient

import (
	"context"
	"errors"
)

// ErrUnconfigured is returned by every Unavailable method, so a client that
// calls a tool in introspection-only mode gets one stable, actionable message
// instead of a connection failure it cannot fix.
var ErrUnconfigured = errors.New("Proton Bridge is not configured; this server is running for MCP introspection only")

// Unavailable is the Bridge used when the server starts without credentials.
// Tools keep their schemas so a directory or client can inspect them, and no
// method touches the network.
type Unavailable struct{}

var _ Bridge = Unavailable{}

func (Unavailable) ListFolders(context.Context) ([]Folder, error) {
	return nil, ErrUnconfigured
}

func (Unavailable) ListMessages(context.Context, string, SearchCriteria, PageRequest) (*MessagePage, error) {
	return nil, ErrUnconfigured
}

func (Unavailable) GetEmail(context.Context, string, uint32, uint32) (*EmailContent, error) {
	return nil, ErrUnconfigured
}

func (Unavailable) GetThread(context.Context, ThreadRef) (*Thread, error) {
	return nil, ErrUnconfigured
}

func (Unavailable) ListAttachments(context.Context, string, uint32, uint32) ([]AttachmentInfo, error) {
	return nil, ErrUnconfigured
}

func (Unavailable) GetAttachment(context.Context, string, uint32, uint32, int) (*AttachmentContent, error) {
	return nil, ErrUnconfigured
}

func (Unavailable) SaveDraft(context.Context, Draft) (*SavedDraft, error) {
	return nil, ErrUnconfigured
}
