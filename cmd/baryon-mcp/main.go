// baryon-mcp is a read-only MCP server for Proton Mail, speaking IMAP to a
// locally-running Proton Mail Bridge over loopback.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
	"github.com/combor/baryon-mcp/internal/credstore"
	"github.com/combor/baryon-mcp/internal/mcptools"
	"github.com/combor/baryon-mcp/internal/setup"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	// stdout carries the MCP JSON-RPC stream; everything else goes to stderr.
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			if err := setup.Run(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
				log.Fatalf("baryon-mcp setup: %v", err)
			}
			return
		default:
			log.Printf("baryon-mcp: unknown command %q", os.Args[1])
			log.Print("usage: baryon-mcp          serve MCP over stdio")
			log.Print("       baryon-mcp setup    store Bridge credentials and configure MCP clients")
			os.Exit(2)
		}
	}

	// Environment variables win; credentials stored by `baryon-mcp setup`
	// fill in whatever the environment leaves unset.
	cfg, err := config.Load(credstore.Getenv(os.Getenv))
	if err != nil {
		log.Fatalf("baryon-mcp: %v", err)
	}

	bridge, err := newBridge(cfg)
	if err != nil {
		log.Fatalf("baryon-mcp: %v", err)
	}

	if err := newServer(bridge, cfg).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("baryon-mcp: %v", err)
	}
}

// newBridge returns the real Bridge client, or one that fails every call when
// the server was started for introspection without credentials. Building the
// real client resolves TLS material, which cannot succeed without a
// certificate, so introspection-only mode skips it entirely.
func newBridge(cfg *config.Config) (bridgeclient.Bridge, error) {
	if cfg.Unconfigured {
		log.Print("baryon-mcp: no Proton Bridge credentials: serving tool schemas for MCP introspection only, every tool call will fail")
		return bridgeclient.Unavailable{}, nil
	}
	client, err := bridgeclient.New(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// newServer builds the MCP server with every tool registered against bridge.
// Without credentials the tools are listed but never executed: save_draft
// reads its content_path attachments while assembling the request, which a
// server that cannot reach a mailbox has no business doing.
func newServer(bridge bridgeclient.Bridge, cfg *config.Config) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "baryon-mcp",
		Title:   "Baryon — Proton Mail via Bridge",
		Version: version,
	}, nil)
	mcptools.RegisterAll(server, bridge, mcptools.Options{AttachmentRoots: cfg.AttachmentRoots})
	if cfg.Unconfigured {
		server.AddReceivingMiddleware(refuseToolCalls)
	}
	return server
}

// refuseToolCalls answers every tools/call with the unconfigured error in
// place of the tool's own handler.
func refuseToolCalls(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: bridgeclient.ErrUnconfigured.Error()}},
		}, nil
	}
}
