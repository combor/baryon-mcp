package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

// TestContainerServesIntrospection drives the container image through a real
// MCP session with no Proton Bridge credentials, the way a directory that only
// reads schemas would. Set BARYON_SMOKE_IMAGE to the image under test, or run
// `make docker-smoke`, which builds it first.
//
// The client owns the container's stdin for the whole session: the server
// ends the session as soon as stdin closes, dropping any replies still in
// flight.
func TestContainerServesIntrospection(t *testing.T) {
	image := os.Getenv("BARYON_SMOKE_IMAGE")
	if image == "" {
		t.Skip("BARYON_SMOKE_IMAGE is unset; no image to test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// No credentials, and --network=none so a Bridge connection is impossible.
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "--network=none", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "baryon-smoke-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connecting to %s: %v\nstderr: %s", image, err, stderr.String())
	}

	if name := session.InitializeResult().ServerInfo.Name; name != "baryon-mcp" {
		t.Errorf("server identified itself as %q, want baryon-mcp", name)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	got := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)
	if want := slices.Sorted(slices.Values(wantTools)); !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}

	if res, err := session.ListResources(ctx, nil); err != nil || len(res.Resources) != 0 {
		t.Errorf("resources/list = (%v, %v), want an empty list", res, err)
	}
	if res, err := session.ListPrompts(ctx, nil); err != nil || len(res.Prompts) != 0 {
		t.Errorf("prompts/list = (%v, %v), want an empty list", res, err)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_folders"})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if !res.IsError {
		t.Error("list_folders should have failed without credentials")
	} else if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, bridgeclient.ErrUnconfigured.Error()) {
		t.Errorf("error %q should report the missing configuration", text)
	}

	// Close returns once the container has exited, so stderr is complete and
	// nothing is writing to it any more.
	if err := session.Close(); err != nil {
		t.Errorf("container did not shut down cleanly: %v", err)
	}
	if !strings.Contains(stderr.String(), "introspection only") {
		t.Errorf("startup should warn that no credentials are configured, got: %s", stderr.String())
	}
}
