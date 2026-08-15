//go:build linux

package setup

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// bridgeExecutables are the base names a process serving Bridge's IMAP port
// may have. The same-uid check is what excludes a hostile process; this list
// only catches pointing setup at some unrelated local IMAP server by
// accident.
var bridgeExecutables = map[string]bool{
	"bridge":                 true,
	"proton-bridge":          true,
	"protonmail-bridge":      true,
	"protonmail-bridge-core": true,
	"protonmail-bridge-gui":  true,
	"proton-mail-bridge":     true,
	"bridge-gui":             true,
}

// tcpListener is one LISTEN socket from a /proc/net/tcp-format table.
type tcpListener struct {
	uid   uint32
	inode string
}

// verifyBridgeListener requires every process listening on the loopback port
// to be a Bridge executable running as the current user before any
// certificate capture. 127.0.0.1, ::1 and the wildcards are distinct sockets
// sharing the port, and which one a dial reaches depends on the configured
// host, so approving a single socket would leave a capture free to pin
// whatever squats the others. procRoot is /proc outside tests.
func verifyBridgeListener(procRoot string, port int) (*bridgeListener, error) {
	entries, err := findLoopbackListeners(procRoot, port)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no process is listening on loopback port %d — is Proton Mail Bridge running?", port)
	}

	var first *bridgeListener
	for _, entry := range entries {
		listener, err := verifyListenerEntry(procRoot, port, entry)
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = listener
		}
	}
	return first, nil
}

// verifyListenerEntry resolves one socket to its owning process and checks
// the uid and executable.
func verifyListenerEntry(procRoot string, port int, entry tcpListener) (*bridgeListener, error) {
	if myUID := uint32(os.Getuid()); entry.uid != myUID {
		return nil, fmt.Errorf(
			"a process listening on loopback port %d runs as uid %d, not as you (uid %d); refusing to pin its certificate",
			port, entry.uid, myUID)
	}
	pid, err := findSocketOwner(procRoot, entry.inode)
	if err != nil {
		return nil, fmt.Errorf("could not identify the process listening on loopback port %d: %w", port, err)
	}
	exe, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "exe"))
	if err != nil {
		return nil, fmt.Errorf("could not identify the executable listening on loopback port %d: %w", port, err)
	}
	if !bridgeExecutables[filepath.Base(exe)] {
		return nil, fmt.Errorf(
			"a process listening on loopback port %d is %s, which does not look like Proton Mail Bridge; refusing to pin its certificate",
			port, exe)
	}
	return &bridgeListener{pid: pid, exe: exe, uid: entry.uid, inode: entry.inode}, nil
}

// findLoopbackListeners collects every LISTEN socket on port from
// /proc/net/tcp and tcp6 whose local address is loopback or the wildcard (a
// wildcard listener also answers loopback connections).
func findLoopbackListeners(procRoot string, port int) ([]tcpListener, error) {
	var entries []tcpListener
	for _, name := range []string{"tcp", "tcp6"} {
		found, err := scanTCPTable(filepath.Join(procRoot, "net", name), port)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		entries = append(entries, found...)
	}
	return entries, nil
}

// scanTCPTable parses one /proc/net/tcp-format file. Relevant columns:
// local_address (hex ip:port), st (0A is LISTEN), uid, and the socket inode.
func scanTCPTable(path string, port int) ([]tcpListener, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []tcpListener
	scanner := bufio.NewScanner(f)
	scanner.Scan() // header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		localIP, localPort, ok := parseHexAddr(fields[1])
		if !ok || localPort != port {
			continue
		}
		if !localIP.IsLoopback() && !localIP.IsUnspecified() {
			continue
		}
		uid, err := strconv.ParseUint(fields[7], 10, 32)
		if err != nil {
			continue
		}
		entries = append(entries, tcpListener{uid: uint32(uid), inode: fields[9]})
	}
	return entries, scanner.Err()
}

// parseHexAddr decodes procfs's "IP:port" notation, where the IP is hex with
// each 4-byte group in little-endian order and the port is plain hex.
func parseHexAddr(s string) (net.IP, int, bool) {
	ipHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	raw, err := hex.DecodeString(ipHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, 0, false
	}
	for i := 0; i+4 <= len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return nil, 0, false
	}
	return net.IP(raw), int(port), true
}

// findSocketOwner locates the pid holding the socket inode. Scanning another
// user's fd directory fails with EACCES, which is fine: the uid check has
// already required the socket to belong to the current user.
func findSocketOwner(procRoot, inode string) (int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, err
	}
	target := "socket:[" + inode + "]"
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(procRoot, entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err == nil && link == target {
				return pid, nil
			}
		}
	}
	return 0, fmt.Errorf("no process owns socket inode %s", inode)
}
