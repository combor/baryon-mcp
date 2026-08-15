//go:build linux

package setup

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/combor/baryon-mcp/internal/credstore"
)

// captureEnv points the runner at the TLS server over implicit TLS.
func captureEnv(port int) map[string]string {
	return map[string]string{
		"PROTON_BRIDGE_IMAP_PORT":     fmt.Sprintf("%d", port),
		"PROTON_BRIDGE_IMAP_SECURITY": "tls",
	}
}

func TestRunCapturesCertificateFromListener(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	fakeClients(t, 1)

	port, certPEM := startLoopbackTLS(t)
	r, stdout, _ := testRunner(t, store, "alice@proton.me\nbridge-pass\n", captureEnv(port))
	r.procRoot = procFixture(t, uint32(os.Getuid()), port, fixtureBridgeExe)

	if err := r.run([]string{"--capture-cert", "--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	certPath, ok := store.CertPath()
	if !ok {
		t.Fatal("no certificate was pinned")
	}
	pinned, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(pinned) != string(certPEM) {
		t.Error("pinned certificate does not match what the server presented")
	}
	out := stdout.String()
	for _, want := range []string{"pid 4242", "sha256", fixtureBridgeExe} {
		if !strings.Contains(out, want) {
			t.Errorf("capture output missing %q:\n%s", want, out)
		}
	}
}

// --capture-cert must replace a stored pin: a headless Bridge that
// regenerated its certificate leaves no exported file to re-pin from.
func TestRunCaptureCertReplacesExistingPin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}

	port, certPEM := startLoopbackTLS(t)
	r, _, _ := testRunner(t, store, "", captureEnv(port))
	r.procRoot = procFixture(t, uint32(os.Getuid()), port, fixtureBridgeExe)

	if err := r.run([]string{"--capture-cert", "--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	certPath, _ := store.CertPath()
	pinned, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(pinned) != string(certPEM) {
		t.Error("the stored pin was not replaced with the captured certificate")
	}
}

// A stored pin that cannot be parsed fails every server launch, so setup
// must resolve a replacement instead of reporting a successful reuse.
func TestRunReplacesUnusableStoredCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}

	port, certPEM := startLoopbackTLS(t)
	r, _, stderr := testRunner(t, store, "y\n", captureEnv(port))
	r.procRoot = procFixture(t, uint32(os.Getuid()), port, fixtureBridgeExe)

	if err := r.run([]string{"--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stderr.String(), "unusable") {
		t.Errorf("no warning about the unusable pin:\n%s", stderr.String())
	}
	certPath, _ := store.CertPath()
	pinned, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(pinned) != string(certPEM) {
		t.Error("the unusable pin was not replaced")
	}
}

func TestRunDeclinedCapturePinsNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())

	port, _ := startLoopbackTLS(t)
	r, _, _ := testRunner(t, store, "n\n", captureEnv(port))
	r.procRoot = procFixture(t, uint32(os.Getuid()), port, fixtureBridgeExe)

	err := r.run([]string{"--skip-client-config"})
	if err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("err = %v, want a declined-capture explanation", err)
	}
	if _, ok := store.CertPath(); ok {
		t.Error("a declined certificate was pinned")
	}
}

// Bridge can exit between the ownership check and the dial, letting another
// process serve the certificate. A rebind changes the socket inode, and the
// re-check must refuse rather than pin what answered.
func TestRunRefusesListenerReplacedDuringCapture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())

	port, _ := startLoopbackTLS(t)
	r, _, _ := testRunner(t, store, "", captureEnv(port))
	root := procFixture(t, uint32(os.Getuid()), port, fixtureBridgeExe)
	r.procRoot = root

	// Swap in a different socket for the same port, as a rebind would.
	rewriteInode := func() {
		writeProcTCP(t, root, "tcp", fmt.Sprintf("0100007F:%04X", port), uint32(os.Getuid()), "777")
		if err := os.Symlink("socket:[777]", filepath.Join(root, "4242", "fd", "12")); err != nil {
			t.Fatal(err)
		}
	}
	r.beforeRecheck = rewriteInode

	err := r.run([]string{"--capture-cert", "--skip-client-config"})
	if err == nil || !strings.Contains(err.Error(), "changed while its certificate was being fetched") {
		t.Fatalf("err = %v, want a refusal for the replaced listener", err)
	}
	if _, ok := store.CertPath(); ok {
		t.Error("a replaced listener's certificate was pinned")
	}
}

func TestRunRefusesForeignListener(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())

	port, _ := startLoopbackTLS(t)
	r, _, _ := testRunner(t, store, "", captureEnv(port))
	r.procRoot = procFixture(t, uint32(os.Getuid())+1, port, fixtureBridgeExe)

	err := r.run([]string{"--capture-cert", "--skip-client-config"})
	if err == nil || !strings.Contains(err.Error(), "refusing to pin") {
		t.Fatalf("err = %v, want a foreign-listener refusal", err)
	}
	if _, ok := store.CertPath(); ok {
		t.Error("a foreign listener's certificate was pinned")
	}
}

// startLoopbackTLS serves TLS handshakes on a loopback port with a fresh
// self-signed certificate, returning the port and the certificate's PEM.
func startLoopbackTLS(t *testing.T) (int, []byte) {
	t.Helper()
	certPEM, cert := selfSignedPair(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port, certPEM
}
