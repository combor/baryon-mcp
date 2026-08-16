package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	"list_attachments", "get_attachment", "save_attachment",
	"list_sender_identities", "save_draft", "save_reply_draft",
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

// The whole inventory is what an MCP directory inspects an unconfigured image
// for, so introspection-only mode must expose all of it.
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

// TestDispatch re-executes the test binary as main() with controlled
// arguments: an unknown command must exit 2 with usage, and "setup" must
// reach the setup flow (whose flag parser rejects the bogus flag).
func TestDispatch(t *testing.T) {
	if args := os.Getenv("BARYON_TEST_DISPATCH_ARGS"); args != "" {
		os.Args = append([]string{"baryon-mcp"}, strings.Fields(args)...)
		main()
		return
	}

	run := func(args string) (string, int) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run", "TestDispatch")
		cmd.Env = append(os.Environ(), "BARYON_TEST_DISPATCH_ARGS="+args)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode()
		}
		if err != nil {
			t.Fatalf("re-exec: %v", err)
		}
		return string(out), 0
	}

	out, code := run("bogus")
	if code != 2 {
		t.Errorf("unknown command exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "unknown command") || !strings.Contains(out, "baryon-mcp setup") {
		t.Errorf("unknown command did not print usage:\n%s", out)
	}

	out, code = run("setup --definitely-not-a-flag")
	if code == 0 {
		t.Errorf("setup with a bogus flag exited 0:\n%s", out)
	}
	if !strings.Contains(out, "baryon-mcp setup") {
		t.Errorf("the setup flow was not dispatched:\n%s", out)
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
