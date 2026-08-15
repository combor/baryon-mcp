//go:build linux

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureBridgeExe = "/fixture/bin/bridge"
	fixtureOtherExe  = "/fixture/bin/some-imap-server"
)

// procFixture builds a minimal procfs tree: one LISTEN socket on the loopback
// port in net/tcp, owned by uid, held by pid 4242 whose exe points at
// exeTarget. The symlink targets do not need to exist.
func procFixture(t *testing.T, uid uint32, port int, exeTarget string) string {
	t.Helper()
	root := t.TempDir()
	writeProcTCP(t, root, "tcp", fmt.Sprintf("0100007F:%04X", port), uid, "999")

	pidDir := filepath.Join(root, "4242")
	if err := os.MkdirAll(filepath.Join(pidDir, "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[999]", filepath.Join(pidDir, "fd", "10")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exeTarget, filepath.Join(pidDir, "exe")); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeProcTCP(t *testing.T, root, table, localAddr string, uid uint32, inode string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		fmt.Sprintf("   0: %s 00000000:0000 0A 00000000:00000000 00:00000000 00000000 %5d        0 %s 1 0000000000000000 100 0 0 10 0\n",
			localAddr, uid, inode)
	if err := os.WriteFile(filepath.Join(root, "net", table), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyBridgeListener(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	listener, err := verifyBridgeListener(root, 1143)
	if err != nil {
		t.Fatalf("verifyBridgeListener: %v", err)
	}
	if listener.pid != 4242 || listener.exe != fixtureBridgeExe {
		t.Errorf("listener = %+v", listener)
	}
}

func TestVerifyBridgeListenerIPv6Loopback(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	// Replace the v4 entry with a ::1 one in tcp6.
	if err := os.Remove(filepath.Join(root, "net", "tcp")); err != nil {
		t.Fatal(err)
	}
	writeProcTCP(t, root, "tcp6", fmt.Sprintf("00000000000000000000000001000000:%04X", 1143), uint32(os.Getuid()), "999")
	if _, err := verifyBridgeListener(root, 1143); err != nil {
		t.Fatalf("verifyBridgeListener over tcp6: %v", err)
	}
}

func TestVerifyBridgeListenerForeignUID(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid())+1, 1143, fixtureBridgeExe)
	_, err := verifyBridgeListener(root, 1143)
	if err == nil || !strings.Contains(err.Error(), "uid") {
		t.Fatalf("err = %v, want a refusal naming the foreign uid", err)
	}
}

func TestVerifyBridgeListenerWrongExecutable(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureOtherExe)
	_, err := verifyBridgeListener(root, 1143)
	if err == nil || !strings.Contains(err.Error(), "does not look like Proton Mail Bridge") {
		t.Fatalf("err = %v, want a refusal naming the executable", err)
	}
}

func TestVerifyBridgeListenerNoListener(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	_, err := verifyBridgeListener(root, 2222)
	if err == nil || !strings.Contains(err.Error(), "no process is listening") {
		t.Fatalf("err = %v, want a missing-listener explanation", err)
	}
}

func TestVerifyBridgeListenerUnfindableSocket(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	if err := os.Remove(filepath.Join(root, "4242", "fd", "10")); err != nil {
		t.Fatal(err)
	}
	_, err := verifyBridgeListener(root, 1143)
	if err == nil || !strings.Contains(err.Error(), "could not identify the process") {
		t.Fatalf("err = %v, want an unidentified-process explanation", err)
	}
}

// Every loopback listener on the port must verify: which one a dial reaches
// depends on the configured host, so a foreign socket on any other loopback
// address poisons the capture even when Bridge's own socket checks out.
func TestVerifyBridgeListenerRefusesMixedListeners(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	writeProcTCP(t, root, "tcp6", fmt.Sprintf("00000000000000000000000001000000:%04X", 1143), uint32(os.Getuid())+1, "888")
	_, err := verifyBridgeListener(root, 1143)
	if err == nil || !strings.Contains(err.Error(), "uid") {
		t.Fatalf("err = %v, want a refusal for the foreign ::1 listener", err)
	}
}

// Bridge listening on both loopback families is the healthy dual-socket
// case and must pass.
func TestVerifyBridgeListenerAcceptsTwoBridgeSockets(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	writeProcTCP(t, root, "tcp6", fmt.Sprintf("00000000000000000000000001000000:%04X", 1143), uint32(os.Getuid()), "888")
	if err := os.Symlink("socket:[888]", filepath.Join(root, "4242", "fd", "11")); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBridgeListener(root, 1143); err != nil {
		t.Fatalf("verifyBridgeListener: %v", err)
	}
}

// A listener on a non-loopback address must not count, even on the right
// port: Bridge only binds loopback, and capture must not pin anything else.
func TestVerifyBridgeListenerIgnoresNonLoopback(t *testing.T) {
	root := procFixture(t, uint32(os.Getuid()), 1143, fixtureBridgeExe)
	// 192.168.1.10 in procfs little-endian notation.
	writeProcTCP(t, root, "tcp", fmt.Sprintf("0A01A8C0:%04X", 1143), uint32(os.Getuid()), "999")
	_, err := verifyBridgeListener(root, 1143)
	if err == nil || !strings.Contains(err.Error(), "no process is listening") {
		t.Fatalf("err = %v, want the non-loopback listener ignored", err)
	}
}
