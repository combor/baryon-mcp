// baryon-mcp is a read-only MCP server for Proton Mail, speaking IMAP to a
// locally-running Proton Mail Bridge over loopback.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

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

	if err := rootCommand().Run(context.Background(), os.Args); err != nil {
		// An ExitCoder carries its own message and status; anything else is
		// a plain failure reported the way the server reports its own.
		var exit cli.ExitCoder
		if errors.As(err, &exit) {
			cli.HandleExitCoder(err)
			return
		}
		log.Fatalf("baryon-mcp: %v", err)
	}
}

// rootCommand serves MCP over stdio when invoked bare, which is how every MCP
// client launches it, and exposes the setup flow as a subcommand.
func rootCommand() *cli.Command {
	return &cli.Command{
		Name:    "baryon-mcp",
		Usage:   "Read Proton Mail and save drafts through your local Proton Mail Bridge",
		Version: version,
		// Usage and errors must never reach stdout, which carries the
		// JSON-RPC stream.
		Writer:    os.Stderr,
		ErrWriter: os.Stderr,
		Commands: []*cli.Command{
			setup.Command(os.Stdin, os.Stdout, os.Stderr),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() > 0 {
				return cli.Exit(fmt.Sprintf("baryon-mcp: unknown command %q; run `baryon-mcp setup` to configure, or launch it with no arguments to serve MCP over stdio", cmd.Args().First()), 2)
			}
			return serve(ctx)
		},
	}
}

// serve builds the MCP server and runs it over stdio until the client
// disconnects.
func serve(ctx context.Context) error {
	// Environment variables win; credentials stored by `baryon-mcp setup`
	// fill in whatever the environment leaves unset.
	cfg, err := config.Load(credstore.Getenv(os.Getenv))
	if err != nil {
		return err
	}

	bridge, err := newBridge(cfg)
	if err != nil {
		return err
	}

	return newServer(bridge, cfg).Run(ctx, &mcp.StdioTransport{})
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
