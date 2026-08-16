// Package config loads and validates baryon-mcp's configuration from
// environment variables (populated by the MCPB manifest's user_config
// mapping, or set directly when running the binary by hand).
package config

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Security selects how the TLS session with Proton Bridge is established.
type Security string

const (
	// SecurityStartTLS upgrades a plain-text connection via STARTTLS.
	// This is Bridge's default IMAP mode (port 1143).
	SecurityStartTLS Security = "starttls"
	// SecurityTLS uses implicit TLS, for Bridge installs switched to
	// SSL connection mode.
	SecurityTLS Security = "tls"
)

// Identity is one address the server may put in a draft's From header. The
// first entry of Config.SenderIdentities is the default.
type Identity struct {
	Address string
	Name    string
}

// Config holds everything needed to reach Proton Bridge's IMAP endpoint.
type Config struct {
	Username string
	Password string
	Host     string
	Port     int
	Security Security
	// TLSCertPath points at Bridge's exported TLS certificate (PEM) used
	// for pinning. Empty means "not provided" — the bridge client then
	// probes well-known locations and otherwise refuses to start unless
	// AllowInsecure is set.
	TLSCertPath string
	// AllowInsecure permits connecting without verifying Bridge's
	// certificate. Explicit opt-in only; accepts the risk that another
	// local process squats Bridge's port and captures the credentials.
	AllowInsecure bool
	// AttachmentRoots limits save_draft content_path reads to these
	// symlink-resolved directories; empty means any readable file.
	AttachmentRoots []string
	// ManagedAttachmentRoot is the fallback directory when
	// BARYON_ATTACHMENT_ROOTS is unset; ActivateManagedAttachmentRoot turns it
	// into a boundary. Empty with explicit roots, or where the attachment tools
	// refuse local paths outright.
	ManagedAttachmentRoot string
	// SenderIdentities are the addresses a draft may be sent from, most
	// preferred first; empty when none could be determined.
	SenderIdentities []Identity
	// AllowedFolders restricts every read tool to these mailboxes; empty means
	// all of them.
	AllowedFolders []string
	// Unconfigured reports that Username and Password are absent and the
	// caller opted into serving MCP introspection without them. Bridge
	// operations are unusable; everything else was still validated.
	Unconfigured bool
}

// Addr returns the host:port dial address.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Unset reports whether a raw environment value counts as not provided: empty,
// or an unresolved MCPB config template (a literal "${...}" left by the host
// for an unset optional field).
func Unset(raw string) bool {
	return raw == "" || (strings.HasPrefix(raw, "${") && strings.HasSuffix(raw, "}"))
}

// Load reads configuration using rawGetenv (usually os.Getenv; injectable for
// tests). Values that Unset reports as not provided are treated as absent.
//
// Bridge credentials are required unless introspectionOnly applies, and every
// other setting is validated either way: introspection-only mode drops the
// credential requirement, not the rest of the validation.
func Load(rawGetenv func(string) string) (*Config, error) {
	getenv := func(key string) string {
		v := rawGetenv(key)
		if Unset(v) {
			return ""
		}
		return v
	}
	cfg := &Config{
		Username:    getenv("PROTON_BRIDGE_USERNAME"),
		Password:    getenv("PROTON_BRIDGE_PASSWORD"),
		Host:        getenv("PROTON_BRIDGE_HOST"),
		Port:        1143,
		Security:    SecurityStartTLS,
		TLSCertPath: getenv("PROTON_BRIDGE_TLS_CERT"),
	}

	unconfigured, err := introspectionOnly(getenv)
	if err != nil {
		return nil, err
	}
	cfg.Unconfigured = unconfigured

	if !unconfigured {
		if cfg.Username == "" {
			return nil, fmt.Errorf("PROTON_BRIDGE_USERNAME is required (Bridge's local IMAP username, shown in Bridge's mailbox settings)")
		}
		if cfg.Password == "" {
			return nil, fmt.Errorf("PROTON_BRIDGE_PASSWORD is required (Bridge's generated password, not your Proton account password)")
		}
	}

	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if !isLoopback(cfg.Host) {
		return nil, fmt.Errorf("PROTON_BRIDGE_HOST %q is not a loopback address: Bridge only listens on the local machine, and this server refuses to send credentials off-host", cfg.Host)
	}

	if v := getenv("PROTON_BRIDGE_IMAP_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("PROTON_BRIDGE_IMAP_PORT %q is not a valid port", v)
		}
		cfg.Port = port
	}

	if v := getenv("PROTON_BRIDGE_IMAP_SECURITY"); v != "" {
		switch Security(strings.ToLower(v)) {
		case SecurityStartTLS:
			cfg.Security = SecurityStartTLS
		case SecurityTLS:
			cfg.Security = SecurityTLS
		default:
			return nil, fmt.Errorf("PROTON_BRIDGE_IMAP_SECURITY %q must be %q (Bridge default) or %q (Bridge in SSL mode)", v, SecurityStartTLS, SecurityTLS)
		}
	}

	if v := getenv("PROTON_BRIDGE_ALLOW_INSECURE"); v != "" {
		allow, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("PROTON_BRIDGE_ALLOW_INSECURE %q is not a boolean", v)
		}
		cfg.AllowInsecure = allow
	}

	if cfg.TLSCertPath != "" {
		if _, err := os.Stat(cfg.TLSCertPath); err != nil {
			return nil, fmt.Errorf("PROTON_BRIDGE_TLS_CERT: %w", err)
		}
	}

	identities, err := senderIdentities(getenv("BARYON_SENDER_IDENTITIES"), cfg.Username)
	if err != nil {
		return nil, err
	}
	cfg.SenderIdentities = identities

	folders, err := allowedFolders(getenv("BARYON_ALLOWED_FOLDERS"))
	if err != nil {
		return nil, err
	}
	cfg.AllowedFolders = folders

	if v := getenv("BARYON_ATTACHMENT_ROOTS"); v != "" {
		for _, root := range filepath.SplitList(v) {
			if root == "" {
				continue
			}
			if !filepath.IsAbs(root) {
				return nil, fmt.Errorf("BARYON_ATTACHMENT_ROOTS entry %q is not an absolute path", root)
			}
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				return nil, fmt.Errorf("BARYON_ATTACHMENT_ROOTS: %w", err)
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return nil, fmt.Errorf("BARYON_ATTACHMENT_ROOTS: %w", err)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("BARYON_ATTACHMENT_ROOTS entry %q is not a directory", root)
			}
			cfg.AttachmentRoots = append(cfg.AttachmentRoots, resolved)
		}
		// Fail closed: a set but entry-less restriction must not mean unrestricted.
		if len(cfg.AttachmentRoots) == 0 {
			return nil, fmt.Errorf("BARYON_ATTACHMENT_ROOTS %q contains no directory entries", v)
		}
	} else {
		managed, err := managedAttachmentRoot(getenv)
		if err != nil {
			return nil, err
		}
		cfg.ManagedAttachmentRoot = managed
	}

	return cfg, nil
}

// managedAttachmentRoot is where local attachment access is confined when the
// operator configured no roots. It only computes the path: `baryon-mcp setup`
// runs Load too, so Load must have no filesystem side effects. Windows has
// none, because both file-touching tools refuse to run there.
func managedAttachmentRoot(getenv func(string) string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	if v := getenv("XDG_CONFIG_HOME"); v != "" {
		// A relative XDG_CONFIG_HOME would resolve against the working directory,
		// which differs between setup and a client-launched server.
		dir, err := filepath.Abs(filepath.Join(v, "baryon-mcp", "attachments"))
		if err != nil {
			return "", fmt.Errorf("resolving the managed attachment directory: %w", err)
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving the attachment directory: %w (set HOME, XDG_CONFIG_HOME, or BARYON_ATTACHMENT_ROOTS)", err)
	}
	return filepath.Join(home, ".config", "baryon-mcp", "attachments"), nil
}

// ConfineUnusedAttachmentRoot names the managed directory as the boundary
// without creating it, for a server that refuses every tool call anyway. The
// directory is absent, so every path is refused — closed rather than open, and
// with no write for a read-only container to fail on.
func (c *Config) ConfineUnusedAttachmentRoot() {
	if c.ManagedAttachmentRoot == "" {
		return
	}
	c.AttachmentRoots = append(c.AttachmentRoots, c.ManagedAttachmentRoot)
}

// ActivateManagedAttachmentRoot creates the managed directory at mode 0700 and
// makes it the active boundary, before the tools pin their roots. Failing here
// beats serving with file access confined to nothing.
func (c *Config) ActivateManagedAttachmentRoot() error {
	if c.ManagedAttachmentRoot == "" {
		return nil
	}
	if err := os.MkdirAll(c.ManagedAttachmentRoot, 0o700); err != nil {
		return fmt.Errorf("creating the attachment directory %s: %w (set BARYON_ATTACHMENT_ROOTS to a directory this process may write to instead)", c.ManagedAttachmentRoot, err)
	}
	// MkdirAll leaves an existing directory's mode alone, and umask can loosen
	// a fresh one.
	if err := os.Chmod(c.ManagedAttachmentRoot, 0o700); err != nil {
		return fmt.Errorf("securing the attachment directory %s: %w", c.ManagedAttachmentRoot, err)
	}
	resolved, err := filepath.EvalSymlinks(c.ManagedAttachmentRoot)
	if err != nil {
		return fmt.Errorf("resolving the attachment directory %s: %w", c.ManagedAttachmentRoot, err)
	}
	c.AttachmentRoots = append(c.AttachmentRoots, resolved)
	return nil
}

// senderIdentities parses BARYON_SENDER_IDENTITIES as an RFC 5322 address
// list, in order, so the first entry is the default. Unset, the Bridge
// username stands in when it is itself an address.
func senderIdentities(raw, username string) ([]Identity, error) {
	if raw == "" {
		address, err := mail.ParseAddress(username)
		if err != nil {
			return nil, nil
		}
		return []Identity{{Address: address.Address, Name: address.Name}}, nil
	}
	parsed, err := mail.ParseAddressList(raw)
	if err != nil {
		return nil, fmt.Errorf("BARYON_SENDER_IDENTITIES %q is not a comma-separated list of RFC 5322 addresses: %w", raw, err)
	}
	identities := make([]Identity, 0, len(parsed))
	seen := make(map[string]bool, len(parsed))
	for _, address := range parsed {
		key := strings.ToLower(address.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		identities = append(identities, Identity{Address: address.Address, Name: address.Name})
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("BARYON_SENDER_IDENTITIES %q lists no addresses", raw)
	}
	return identities, nil
}

// allowedFolders parses BARYON_ALLOWED_FOLDERS as one RFC 4180 CSV row, so a
// mailbox whose name contains a comma can be quoted rather than split.
func allowedFolders(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	record, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("BARYON_ALLOWED_FOLDERS %q is not a single CSV row of folder names: %w", raw, err)
	}
	// Anything after the first row — an unquoted newline, or CSV that does not
	// parse — fails rather than truncating the policy to whatever parsed.
	if _, err := reader.Read(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("BARYON_ALLOWED_FOLDERS %q must be a single CSV row of folder names", raw)
	}
	folders := make([]string, 0, len(record))
	seen := make(map[string]bool, len(record))
	for _, name := range record {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		folders = append(folders, name)
	}
	// Fail closed: a set but entry-less restriction must not mean unrestricted.
	if len(folders) == 0 {
		return nil, fmt.Errorf("BARYON_ALLOWED_FOLDERS %q contains no folder names", raw)
	}
	return folders, nil
}

// introspectionOnly reports whether the server may start with no Bridge
// credentials at all, serving tool schemas to a client that only inspects
// them. It is meant for container images published for MCP directory
// validation, where no mailbox is reachable.
//
// Both credentials must be absent: with one of them set, the missing half is
// a typo or a lost secret, and startup must fail rather than quietly degrade
// into a server whose every call errors.
func introspectionOnly(getenv func(string) string) (bool, error) {
	v := getenv("BARYON_ALLOW_UNCONFIGURED_INTROSPECTION")
	if v == "" {
		return false, nil
	}
	allow, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("BARYON_ALLOW_UNCONFIGURED_INTROSPECTION %q is not a boolean", v)
	}
	return allow && getenv("PROTON_BRIDGE_USERNAME") == "" && getenv("PROTON_BRIDGE_PASSWORD") == "", nil
}

// isLoopback reports whether host is "localhost" or a loopback IP literal.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
