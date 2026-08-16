package setup

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/combor/baryon-mcp/internal/credstore"
)

// fakeKeyring keeps setup tests away from the real secret store; setErr
// forces the file fallback.
type fakeKeyring struct {
	items  map[string]string
	setErr error
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{items: map[string]string{}} }

func (k *fakeKeyring) Get(service, user string) (string, error) {
	v, ok := k.items[service+"\x00"+user]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (k *fakeKeyring) Set(service, user, pass string) error {
	if k.setErr != nil {
		return k.setErr
	}
	k.items[service+"\x00"+user] = pass
	return nil
}

func (k *fakeKeyring) Delete(service, user string) error {
	delete(k.items, service+"\x00"+user)
	return nil
}

// testCertPEM generates a self-signed certificate for tests that need a
// valid exported cert.pem.
func testCertPEM(t *testing.T) []byte {
	t.Helper()
	pemBytes, _ := selfSignedPair(t)
	return pemBytes
}

// selfSignedPair generates a self-signed certificate, returning its PEM and
// the serving pair for tests that stand up a TLS listener.
func selfSignedPair(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return pemBytes, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// fakeClients installs claude and codex stand-ins that log their argv, and
// points PATH at them exclusively. getExit is the exit code for `mcp get`,
// mirroring whether an entry already exists.
func fakeClients(t *testing.T, getExit int) (logPath string) {
	t.Helper()
	binDir := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "clients.log")
	// PATH holds only the fakes, so no external commands in the script.
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s:%%s\n' "${0##*/}" "$*" >>%q
case "$2" in get) exit %d ;; esac
exit 0
`, logPath, getExit)
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	return logPath
}

func clientLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// testRunner wires a runner over a fake store, piped stdin, and captured
// output. The empty procRoot fixture means no listener exists, so any capture
// attempt fails rather than touching the machine.
func testRunner(t *testing.T, store *credstore.Store, input string, env map[string]string) (*runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	stdin := strings.NewReader(input)
	return &runner{
		store:    store,
		stdin:    bufio.NewReader(stdin),
		rawStdin: stdin,
		stdout:   stdout,
		stderr:   stderr,
		procRoot: t.TempDir(),
		getenv:   func(key string) string { return env[key] },
	}, stdout, stderr
}

func TestRunFreshSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	logPath := fakeClients(t, 1)

	certSource := filepath.Join(t.TempDir(), "exported.pem")
	if err := os.WriteFile(certSource, testCertPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}

	r, stdout, _ := testRunner(t, store, "alice@proton.me\nbridge-pass\n", nil)
	if err := r.run([]string{"--tls-cert", certSource}); err != nil {
		t.Fatalf("run: %v", err)
	}

	creds, ok := store.Load()
	if !ok || creds.Username != "alice@proton.me" || creds.Password != "bridge-pass" {
		t.Errorf("stored credentials = %+v, %v", creds, ok)
	}
	certPath, ok := store.CertPath()
	if !ok {
		t.Fatal("no certificate was pinned")
	}
	info, err := os.Stat(certPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("cert stat = %v, mode %o", err, info.Mode().Perm())
	}

	log := clientLog(t, logPath)
	if !strings.Contains(log, "claude:mcp add --transport stdio --scope user baryon --") {
		t.Errorf("claude was not configured:\n%s", log)
	}
	if !strings.Contains(log, "codex:mcp add baryon --") {
		t.Errorf("codex was not configured:\n%s", log)
	}
	if !strings.Contains(stdout.String(), "Configured baryon-mcp") {
		t.Errorf("no summary printed:\n%s", stdout.String())
	}
}

// A second run must reuse the stored certificate and credentials without
// prompting: stdin is empty, so any prompt would fail the run.
func TestRunReusesExistingSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "alice@proton.me", Password: "pass"}); err != nil {
		t.Fatal(err)
	}

	r, stdout, _ := testRunner(t, store, "", nil)
	if err := r.run([]string{"--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Reusing") {
		t.Errorf("nothing was reused:\n%s", out)
	}
}

func TestRunLeavesExistingClientEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	logPath := fakeClients(t, 0)

	r, _, stderr := testRunner(t, store, "", nil)
	if err := r.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if log := clientLog(t, logPath); strings.Contains(log, "mcp add") {
		t.Errorf("existing entries were replaced:\n%s", log)
	}
	if !strings.Contains(stderr.String(), "left it unchanged") {
		t.Errorf("no warning about the preserved entry:\n%s", stderr.String())
	}
}

func TestRunForceReplacesClientEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	logPath := fakeClients(t, 0)

	r, _, _ := testRunner(t, store, "", nil)
	if err := r.run([]string{"--force-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	log := clientLog(t, logPath)
	if !strings.Contains(log, "claude:mcp remove --scope user baryon") {
		t.Errorf("the old claude entry was not removed:\n%s", log)
	}
	if !strings.Contains(log, "claude:mcp add ") || !strings.Contains(log, "codex:mcp add ") {
		t.Errorf("entries were not re-added:\n%s", log)
	}
}

// Endpoint overrides given to setup must travel into the client entries, or
// the client dials a different endpoint than the one whose certificate setup
// pinned. Credentials must not: they live in the store.
func TestRunPassesEndpointOverridesToClients(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	logPath := fakeClients(t, 1)

	r, _, _ := testRunner(t, store, "", map[string]string{
		"PROTON_BRIDGE_IMAP_PORT":     "1144",
		"PROTON_BRIDGE_IMAP_SECURITY": "tls",
		"PROTON_BRIDGE_PASSWORD":      "must-not-travel",
	})
	if err := r.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	log := clientLog(t, logPath)
	if !strings.Contains(log, "-e PROTON_BRIDGE_IMAP_PORT=1144") ||
		!strings.Contains(log, "-e PROTON_BRIDGE_IMAP_SECURITY=tls") {
		t.Errorf("claude entry lost the endpoint overrides:\n%s", log)
	}
	if !strings.Contains(log, "--env PROTON_BRIDGE_IMAP_PORT=1144") ||
		!strings.Contains(log, "--env PROTON_BRIDGE_IMAP_SECURITY=tls") {
		t.Errorf("codex entry lost the endpoint overrides:\n%s", log)
	}
	if strings.Contains(log, "must-not-travel") {
		t.Errorf("a credential leaked into a client entry:\n%s", log)
	}
}

// A client process need not inherit setup's XDG_CONFIG_HOME, so the resolved
// store location travels with the entry.
func TestRunPassesStoreLocationToClients(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	logPath := fakeClients(t, 1)

	r, _, _ := testRunner(t, store, "", map[string]string{"XDG_CONFIG_HOME": parent})
	if err := r.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	log := clientLog(t, logPath)
	if !strings.Contains(log, "-e XDG_CONFIG_HOME="+parent) {
		t.Errorf("claude entry lost the store location:\n%s", log)
	}
	if !strings.Contains(log, "--env XDG_CONFIG_HOME="+parent) {
		t.Errorf("codex entry lost the store location:\n%s", log)
	}
}

// Reusing stored credentials must tighten permissive modes rather than leave
// the Bridge password readable by others.
func TestRunRepairsPermissionsOnReuse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	kr := newFakeKeyring()
	kr.setErr = errors.New("no secret service")
	fileStore := credstore.OpenWith(dir, kr)
	if _, _, err := fileStore.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bridge-username", "bridge-password", "cert.pem"} {
		if err := os.Chmod(filepath.Join(dir, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r, _, _ := testRunner(t, fileStore, "", nil)
	if err := r.run([]string{"--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if mode := statMode(t, dir); mode != 0o700 {
		t.Errorf("dir mode = %o, want 700", mode)
	}
	for _, name := range []string{"bridge-username", "bridge-password", "cert.pem"} {
		if mode := statMode(t, filepath.Join(dir, name)); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", name, mode)
		}
	}
}

func statMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestRunRejectsNonPEMCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	garbage := filepath.Join(t.TempDir(), "not-a-cert")
	if err := os.WriteFile(garbage, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, _, _ := testRunner(t, store, "", nil)
	err := r.run([]string{"--tls-cert", garbage, "--skip-client-config"})
	if err == nil || !strings.Contains(err.Error(), "TLS certificate") {
		t.Fatalf("err = %v, want a certificate rejection", err)
	}
	if _, ok := store.CertPath(); ok {
		t.Error("a non-PEM file was pinned")
	}
}

// -h prints usage and succeeds; a non-zero exit would make the documented
// help command look like a failure.
func TestRunHelpSucceeds(t *testing.T) {
	store := credstore.OpenWith(filepath.Join(t.TempDir(), "baryon-mcp"), newFakeKeyring())
	r, _, stderr := testRunner(t, store, "", nil)
	if err := r.run([]string{"-h"}); err != nil {
		t.Fatalf("run -h: %v", err)
	}
	for _, want := range []string{"USAGE:", "--capture-cert", "--skip-client-config"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("usage missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRunRejectsUnknownClient(t *testing.T) {
	store := credstore.OpenWith(filepath.Join(t.TempDir(), "baryon-mcp"), newFakeKeyring())
	r, _, _ := testRunner(t, store, "", nil)
	err := r.run([]string{"--client", "zed"})
	if err == nil || !strings.Contains(err.Error(), "unsupported client") {
		t.Fatalf("err = %v, want an unsupported-client rejection", err)
	}
}

// A client named twice must be configured once.
func TestRunDeduplicatesClients(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	logPath := fakeClients(t, 1)

	r, _, _ := testRunner(t, store, "", nil)
	// Interleaved, not just adjacent: both forms must collapse to one pass.
	if err := r.run([]string{"--client", "claude", "--client", "codex", "--client", "claude"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	log := clientLog(t, logPath)
	if got := strings.Count(log, "claude:mcp add "); got != 1 {
		t.Errorf("claude configured %d times:\n%s", got, log)
	}
	if got := strings.Count(log, "codex:mcp add "); got != 1 {
		t.Errorf("codex configured %d times:\n%s", got, log)
	}
}

// Without --client both supported clients are configured.
func TestRunDefaultsToBothClients(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	store := credstore.OpenWith(dir, newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "a@b.c", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	logPath := fakeClients(t, 1)

	r, _, _ := testRunner(t, store, "", nil)
	if err := r.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	log := clientLog(t, logPath)
	for _, client := range strings.Fields(defaultClients) {
		if !strings.Contains(log, client+":mcp add ") {
			t.Errorf("%s was not configured:\n%s", client, log)
		}
	}
}

// An unexpected positional argument is a typo, not a client to configure.
func TestRunRejectsPositionalArgument(t *testing.T) {
	store := credstore.OpenWith(filepath.Join(t.TempDir(), "baryon-mcp"), newFakeKeyring())
	r, _, _ := testRunner(t, store, "", nil)
	err := r.run([]string{"stray"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("err = %v, want a rejection of the stray argument", err)
	}
}

// The summary names where the attachment tools may read and write.
func TestSetupReportsTheAttachmentBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the attachment tools refuse local paths on Windows")
	}
	store := credstore.OpenWith(filepath.Join(t.TempDir(), "baryon-mcp"), newFakeKeyring())
	if _, err := store.SaveCert(testCertPEM(t)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(credstore.Credentials{Username: "alice@proton.me", Password: "pass"}); err != nil {
		t.Fatal(err)
	}

	config := t.TempDir()
	r, stdout, _ := testRunner(t, store, "", map[string]string{"XDG_CONFIG_HOME": config})
	if err := r.run([]string{"--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	managed := filepath.Join(config, "baryon-mcp", "attachments")
	if !strings.Contains(stdout.String(), managed) {
		t.Errorf("summary does not name the managed attachment directory %q:\n%s", managed, stdout.String())
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Errorf("setup created %q; the server creates it when it starts", managed)
	}

	explicit := t.TempDir()
	r, stdout, _ = testRunner(t, store, "", map[string]string{
		"XDG_CONFIG_HOME":         config,
		"BARYON_ATTACHMENT_ROOTS": explicit,
	})
	if err := r.run([]string{"--skip-client-config"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(explicit)
	if !strings.Contains(stdout.String(), resolved) {
		t.Errorf("summary does not name the configured root %q:\n%s", resolved, stdout.String())
	}
}

// Setup writes the policy variables into the client entry, so it must refuse
// a value the server would reject rather than report success.
func TestRunRefusesPolicyTheServerWouldReject(t *testing.T) {
	for name, bad := range map[string]map[string]string{
		"malformed folder list":      {"BARYON_ALLOWED_FOLDERS": "INBOX\n\"unterminated"},
		"malformed sender identity":  {"BARYON_SENDER_IDENTITIES": "not an address"},
		"attachment root that is no": {"BARYON_ATTACHMENT_ROOTS": "relative/dir"},
	} {
		t.Run(name, func(t *testing.T) {
			store := credstore.OpenWith(filepath.Join(t.TempDir(), "baryon-mcp"), newFakeKeyring())
			if _, err := store.SaveCert(testCertPEM(t)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Save(credstore.Credentials{Username: "alice@proton.me", Password: "pass"}); err != nil {
				t.Fatal(err)
			}
			logPath := fakeClients(t, 1)

			r, stdout, _ := testRunner(t, store, "", bad)
			err := r.run(nil)
			if err == nil {
				t.Fatalf("setup reported success:\n%s", stdout.String())
			}
			for key := range bad {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error %v should name %s", err, key)
				}
			}
			if log := clientLog(t, logPath); strings.Contains(log, "mcp add") {
				t.Errorf("a client was configured with the rejected value:\n%s", log)
			}
		})
	}
}
