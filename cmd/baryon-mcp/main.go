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
	"github.com/combor/baryon-mcp/internal/mcptools"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	// stdout carries the MCP JSON-RPC stream; everything else goes to stderr.
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatalf("baryon-mcp: %v", err)
	}

	bridge, err := bridgeclient.New(cfg)
	if err != nil {
		log.Fatalf("baryon-mcp: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "baryon-mcp",
		Title:   "Baryon — Proton Mail via Bridge",
		Version: version,
	}, nil)
	mcptools.RegisterAll(server, bridge, mcptools.Options{AttachmentRoots: cfg.AttachmentRoots})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("baryon-mcp: %v", err)
	}
}
