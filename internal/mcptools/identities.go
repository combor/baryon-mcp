package mcptools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/config"
)

type senderIdentityOutput struct {
	Address string `json:"address" jsonschema:"the mailbox itself, without a display name"`
	Name    string `json:"name,omitempty" jsonschema:"display name to send under, when one is configured"`
	Default bool   `json:"default" jsonschema:"true for the address used when a draft names none"`
}

type listSenderIdentitiesOutput struct {
	Identities []senderIdentityOutput `json:"identities" jsonschema:"most preferred first; the first entry is the default"`
}

func registerListSenderIdentities(server *mcp.Server, identities []config.Identity) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sender_identities",
		Description: "List the addresses this server may put in a draft's From header, as configured by the operator. save_draft requires one of them as from; save_reply_draft accepts one and picks a suitable address itself when from is omitted. These are the account's own addresses, not addresses taken from any message.",
		Annotations: readOnly("List sender identities"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, listSenderIdentitiesOutput, error) {
		out := listSenderIdentitiesOutput{Identities: make([]senderIdentityOutput, 0, len(identities))}
		for i, identity := range identities {
			out.Identities = append(out.Identities, senderIdentityOutput{
				Address: identity.Address,
				Name:    identity.Name,
				Default: i == 0,
			})
		}
		return nil, out, nil
	})
}
