package bridgeclient

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/combor/baryon-mcp/internal/config"
)

// stallingServer accepts one connection, greets, acknowledges STARTTLS, then
// never completes the TLS handshake.
func stallingServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "* OK ready\r\n")
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		tag := strings.Fields(line)[0]
		fmt.Fprintf(conn, "%s OK begin TLS\r\n", tag)
		time.Sleep(time.Minute) // stall the handshake
	}()
	return ln.Addr().String()
}

func testClient(t *testing.T, addr string) *Client {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var portNum int
	fmt.Sscanf(port, "%d", &portNum)
	return &Client{
		cfg: &config.Config{
			Username: "u", Password: "p",
			Host: host, Port: portNum,
			Security: config.SecurityStartTLS,
		},
		tlsConfig: &tlsConfigHolder{config: &tls.Config{InsecureSkipVerify: true}},
		sem:       make(chan struct{}, maxConcurrentConnections),
	}
}

func TestStalledHandshakeReleasesSlot(t *testing.T) {
	oldTimeout := setupTimeout
	setupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { setupTimeout = oldTimeout })

	c := testClient(t, stallingServer(t))
	done := make(chan error, 1)
	go func() {
		done <- c.withSession(context.Background(), func(*imapclient.Client) error { return nil })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from stalled handshake")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("withSession did not return; semaphore slot leaked")
	}
	if len(c.sem) != 0 {
		t.Fatalf("semaphore not released: %d slots held", len(c.sem))
	}
}

func TestCancellationReleasesSlot(t *testing.T) {
	c := testClient(t, stallingServer(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.withSession(ctx, func(*imapclient.Client) error { return nil })
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("withSession did not return after cancel; semaphore slot leaked")
	}
	if len(c.sem) != 0 {
		t.Fatalf("semaphore not released: %d slots held", len(c.sem))
	}
}
