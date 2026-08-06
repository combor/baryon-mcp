package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
)

// wantTools is every tool the server registers, shared with the container
// smoke test so both check the same list.
var wantTools = []string{
	"list_folders", "list_emails", "search_emails", "get_email", "get_thread",
	"list_attachments", "get_attachment", "save_attachment", "save_draft",
}

// introspectionSession connects an in-memory client to the server as it is
// built when no Bridge credentials were supplied.
func introspectionSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	bridge, err := newBridge(&config.Config{Unconfigured: true})
	if err != nil {
		t.Fatalf("newBridge: %v", err)
	}
	if _, ok := bridge.(bridgeclient.Unavailable); !ok {
		t.Fatalf("newBridge returned %T, want Unavailable", bridge)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := newServer(bridge, &config.Config{Unconfigured: true}).Connect(context.Background(), serverTransport, nil); err != nil {
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

// The nine tools are what an MCP directory inspects an unconfigured image
// for, so introspection-only mode must expose all of them.
func TestIntrospectionListsEveryTool(t *testing.T) {
	res, err := introspectionSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, name := range wantTools {
		if !got[name] {
			t.Errorf("tool %q missing", name)
		}
	}
	if len(res.Tools) != len(wantTools) {
		t.Errorf("got %d tools, want %d", len(res.Tools), len(wantTools))
	}
}

func TestIntrospectionToolCallReportsMissingConfiguration(t *testing.T) {
	res, err := introspectionSession(t).CallTool(context.Background(), &mcp.CallToolParams{Name: "list_folders"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result without credentials")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, bridgeclient.ErrUnconfigured.Error()) {
		t.Errorf("error %q should report the missing configuration", text)
	}
}

// A refused call must not have opened the file first: the path below does not
// exist, so a handler that ran would report that instead.
func TestIntrospectionRefusesToolCallsBeforeTouchingFiles(t *testing.T) {
	res, err := introspectionSession(t).CallTool(context.Background(), &mcp.CallToolParams{
		Name: "save_draft",
		Arguments: map[string]any{
			"from":        "alice@proton.me",
			"to":          []string{"bob@example.com"},
			"attachments": []any{map[string]any{"content_path": "/nonexistent/probe.txt"}},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result without credentials")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, bridgeclient.ErrUnconfigured.Error()) {
		t.Errorf("error %q should report the missing configuration, not the filesystem", text)
	}
}

func TestConfiguredServerUsesRealClient(t *testing.T) {
	bridge, err := newBridge(&config.Config{
		Username:      "alice@proton.me",
		Password:      "bridge-pass",
		Host:          "127.0.0.1",
		Port:          1143,
		Security:      config.SecurityStartTLS,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("newBridge: %v", err)
	}
	if _, ok := bridge.(*bridgeclient.Client); !ok {
		t.Errorf("newBridge returned %T, want *bridgeclient.Client", bridge)
	}
}
