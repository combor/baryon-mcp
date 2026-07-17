package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"PROTON_BRIDGE_USERNAME": "alice@proton.me",
		"PROTON_BRIDGE_PASSWORD": "bridge-pass",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(validEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 1143 {
		t.Errorf("got %s:%d, want 127.0.0.1:1143", cfg.Host, cfg.Port)
	}
	if cfg.Security != SecurityStartTLS {
		t.Errorf("got security %q, want starttls", cfg.Security)
	}
	if cfg.AllowInsecure {
		t.Error("AllowInsecure should default to false")
	}
	if got, want := cfg.Addr(), "127.0.0.1:1143"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}

func TestLoadMissingCredentials(t *testing.T) {
	for _, missing := range []string{"PROTON_BRIDGE_USERNAME", "PROTON_BRIDGE_PASSWORD"} {
		m := validEnv()
		delete(m, missing)
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("missing %s: got err %v, want mention of it", missing, err)
		}
	}
}

func TestLoadHostValidation(t *testing.T) {
	accepted := []string{"localhost", "LocalHost", "127.0.0.1", "127.1.2.3", "::1"}
	for _, h := range accepted {
		m := validEnv()
		m["PROTON_BRIDGE_HOST"] = h
		if _, err := Load(env(m)); err != nil {
			t.Errorf("host %q: unexpected error %v", h, err)
		}
	}
	rejected := []string{"192.168.1.5", "10.0.0.1", "bridge.example.com", "0.0.0.0", "::"}
	for _, h := range rejected {
		m := validEnv()
		m["PROTON_BRIDGE_HOST"] = h
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Errorf("host %q: got err %v, want loopback rejection", h, err)
		}
	}
}

func TestLoadPort(t *testing.T) {
	m := validEnv()
	m["PROTON_BRIDGE_IMAP_PORT"] = "1144"
	cfg, err := Load(env(m))
	if err != nil || cfg.Port != 1144 {
		t.Errorf("got (%v, %v), want port 1144", cfg, err)
	}
	for _, bad := range []string{"0", "65536", "-1", "imap"} {
		m["PROTON_BRIDGE_IMAP_PORT"] = bad
		if _, err := Load(env(m)); err == nil {
			t.Errorf("port %q: expected error", bad)
		}
	}
}

func TestLoadSecurity(t *testing.T) {
	m := validEnv()
	m["PROTON_BRIDGE_IMAP_SECURITY"] = "TLS"
	cfg, err := Load(env(m))
	if err != nil || cfg.Security != SecurityTLS {
		t.Errorf("got (%v, %v), want tls", cfg, err)
	}
	m["PROTON_BRIDGE_IMAP_SECURITY"] = "ssl"
	if _, err := Load(env(m)); err == nil {
		t.Error(`security "ssl": expected error naming valid values`)
	}
}

func TestLoadAllowInsecure(t *testing.T) {
	m := validEnv()
	m["PROTON_BRIDGE_ALLOW_INSECURE"] = "true"
	cfg, err := Load(env(m))
	if err != nil || !cfg.AllowInsecure {
		t.Errorf("got (%+v, %v), want AllowInsecure=true", cfg, err)
	}
	m["PROTON_BRIDGE_ALLOW_INSECURE"] = "yes please"
	if _, err := Load(env(m)); err == nil {
		t.Error("non-boolean ALLOW_INSECURE: expected error")
	}
}

func TestLoadUnresolvedTemplateTreatedAsUnset(t *testing.T) {
	m := validEnv()
	m["PROTON_BRIDGE_TLS_CERT"] = "${user_config.bridge_tls_cert}"
	m["PROTON_BRIDGE_ALLOW_INSECURE"] = "${user_config.bridge_allow_insecure}"
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("unresolved templates must not fail Load: %v", err)
	}
	if cfg.TLSCertPath != "" || cfg.AllowInsecure {
		t.Errorf("templates should read as unset, got %+v", cfg)
	}
}

func TestLoadTLSCertPath(t *testing.T) {
	m := validEnv()
	m["PROTON_BRIDGE_TLS_CERT"] = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := Load(env(m)); err == nil {
		t.Error("nonexistent cert path: expected error")
	}

	certPath := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(certPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	m["PROTON_BRIDGE_TLS_CERT"] = certPath
	cfg, err := Load(env(m))
	if err != nil || cfg.TLSCertPath != certPath {
		t.Errorf("got (%+v, %v), want cert path accepted", cfg, err)
	}
}
