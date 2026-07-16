package mcptools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

type listFoldersInput struct{}

type folderInfo struct {
	Name       string   `json:"name" jsonschema:"full folder name, used as the folder argument of other tools"`
	Delimiter  string   `json:"delimiter,omitempty" jsonschema:"hierarchy delimiter separating nested folder names"`
	Attributes []string `json:"attributes,omitempty" jsonschema:"IMAP mailbox attributes such as \\HasChildren or \\Trash"`
}

type listFoldersOutput struct {
	Folders []folderInfo `json:"folders" jsonschema:"all folders in the mailbox"`
}

func registerListFolders(server *mcp.Server, bridge bridgeclient.Bridge) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_folders",
		Description: "List all folders (mailboxes) in the Proton Mail account, including system folders like INBOX, Sent, and Archive.",
		Annotations: readOnly("List folders"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listFoldersInput) (*mcp.CallToolResult, listFoldersOutput, error) {
		folders, err := bridge.ListFolders(ctx)
		if err != nil {
			return nil, listFoldersOutput{}, err
		}
		out := listFoldersOutput{Folders: make([]folderInfo, 0, len(folders))}
		for _, f := range folders {
			out.Folders = append(out.Folders, folderInfo{
				Name:       f.Name,
				Delimiter:  f.Delimiter,
				Attributes: f.Attributes,
			})
		}
		return nil, out, nil
	})
}
