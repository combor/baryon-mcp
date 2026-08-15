// Package config loads and validates baryon-mcp's configuration from
// environment variables (populated by the MCPB manifest's user_config
// mapping, or set directly when running the binary by hand).
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	}

	return cfg, nil
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
