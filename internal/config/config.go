// Package config loads and validates baryon-mcp's configuration from
// environment variables (populated by the MCPB manifest's user_config
// mapping, or set directly when running the binary by hand).
package config

import (
	"fmt"
	"net"
	"os"
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
}

// Addr returns the host:port dial address.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Load reads configuration using rawGetenv (usually os.Getenv; injectable for
// tests). Empty values and unresolved MCPB config templates (a literal
// "${...}" left by the host for an unset optional field) are treated as unset.
func Load(rawGetenv func(string) string) (*Config, error) {
	getenv := func(key string) string {
		v := rawGetenv(key)
		if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
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

	if cfg.Username == "" {
		return nil, fmt.Errorf("PROTON_BRIDGE_USERNAME is required (Bridge's local IMAP username, shown in Bridge's mailbox settings)")
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("PROTON_BRIDGE_PASSWORD is required (Bridge's generated password, not your Proton account password)")
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

	return cfg, nil
}

// isLoopback reports whether host is "localhost" or a loopback IP literal.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
