package config

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestLoadUnconfiguredIntrospection(t *testing.T) {
	t.Run("opt-in without credentials", func(t *testing.T) {
		cfg, err := Load(env(map[string]string{"BARYON_ALLOW_UNCONFIGURED_INTROSPECTION": "true"}))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Unconfigured {
			t.Error("want Unconfigured=true")
		}
	})

	t.Run("credentials still required without the opt-in", func(t *testing.T) {
		for _, v := range []string{"", "false"} {
			m := map[string]string{"BARYON_ALLOW_UNCONFIGURED_INTROSPECTION": v}
			if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "PROTON_BRIDGE_USERNAME") {
				t.Errorf("opt-in %q: got err %v, want the username requirement", v, err)
			}
		}
	})

	t.Run("one credential fails", func(t *testing.T) {
		for _, missing := range []string{"PROTON_BRIDGE_USERNAME", "PROTON_BRIDGE_PASSWORD"} {
			m := validEnv()
			delete(m, missing)
			m["BARYON_ALLOW_UNCONFIGURED_INTROSPECTION"] = "true"
			if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), missing) {
				t.Errorf("missing %s: got err %v, want it still required", missing, err)
			}
		}
	})

	t.Run("complete credentials stay configured", func(t *testing.T) {
		m := validEnv()
		m["BARYON_ALLOW_UNCONFIGURED_INTROSPECTION"] = "true"
		cfg, err := Load(env(m))
		if err != nil || cfg.Unconfigured {
			t.Errorf("got (%+v, %v), want a configured server", cfg, err)
		}
	})

	t.Run("malformed opt-in fails", func(t *testing.T) {
		m := validEnv()
		m["BARYON_ALLOW_UNCONFIGURED_INTROSPECTION"] = "sure"
		if _, err := Load(env(m)); err == nil {
			t.Error("non-boolean opt-in: expected error")
		}
	})

	// The fallback drops the credential requirement, nothing else.
	t.Run("other settings are still validated", func(t *testing.T) {
		for key, bad := range map[string]string{
			"PROTON_BRIDGE_HOST":           "192.168.1.5",
			"PROTON_BRIDGE_IMAP_PORT":      "70000",
			"PROTON_BRIDGE_IMAP_SECURITY":  "ssl",
			"PROTON_BRIDGE_ALLOW_INSECURE": "maybe",
			"PROTON_BRIDGE_TLS_CERT":       filepath.Join(t.TempDir(), "missing.pem"),
			"BARYON_ATTACHMENT_ROOTS":      "relative/dir",
		} {
			m := map[string]string{"BARYON_ALLOW_UNCONFIGURED_INTROSPECTION": "true", key: bad}
			if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s=%q: got err %v, want rejection", key, bad, err)
			}
		}
	})
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

func TestLoadAttachmentRoots(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	m := validEnv()
	m["BARYON_ATTACHMENT_ROOTS"] = dirA + string(os.PathListSeparator) + dirB
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	resolvedA, _ := filepath.EvalSymlinks(dirA)
	resolvedB, _ := filepath.EvalSymlinks(dirB)
	if len(cfg.AttachmentRoots) != 2 || cfg.AttachmentRoots[0] != resolvedA || cfg.AttachmentRoots[1] != resolvedB {
		t.Errorf("AttachmentRoots = %v, want resolved %q and %q", cfg.AttachmentRoots, resolvedA, resolvedB)
	}

	file := filepath.Join(dirA, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative/dir", filepath.Join(dirA, "missing"), file, string(os.PathListSeparator)} {
		m["BARYON_ATTACHMENT_ROOTS"] = bad
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "BARYON_ATTACHMENT_ROOTS") {
			t.Errorf("root %q: got err %v, want rejection", bad, err)
		}
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

func TestLoadSenderIdentities(t *testing.T) {
	m := validEnv()
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SenderIdentities) != 1 || cfg.SenderIdentities[0].Address != "alice@proton.me" {
		t.Errorf("unset: identities = %+v, want the Bridge username", cfg.SenderIdentities)
	}

	m["PROTON_BRIDGE_USERNAME"] = "bridge-account-1"
	cfg, err = Load(env(m))
	if err != nil || len(cfg.SenderIdentities) != 0 {
		t.Errorf("username that is not an address: got (%+v, %v), want no identities", cfg.SenderIdentities, err)
	}

	m = validEnv()
	m["BARYON_SENDER_IDENTITIES"] = `"Doe, Alice" <alice@proton.me>, work@proton.me, ALICE@proton.me`
	cfg, err = Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SenderIdentities) != 2 {
		t.Fatalf("identities = %+v, want the case-insensitive duplicate dropped", cfg.SenderIdentities)
	}
	if got := cfg.SenderIdentities[0]; got.Address != "alice@proton.me" || got.Name != "Doe, Alice" {
		t.Errorf("first identity = %+v, want the configured default with its name", got)
	}

	m["BARYON_SENDER_IDENTITIES"] = "not an address"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "BARYON_SENDER_IDENTITIES") {
		t.Errorf("malformed list: got err %v, want rejection", err)
	}
}

func TestLoadAllowedFolders(t *testing.T) {
	m := validEnv()
	cfg, err := Load(env(m))
	if err != nil || cfg.AllowedFolders != nil {
		t.Errorf("unset: got (%v, %v), want no restriction", cfg.AllowedFolders, err)
	}

	m["BARYON_ALLOWED_FOLDERS"] = `INBOX, "Folders/Invoices, paid", All Mail, INBOX`
	cfg, err = Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"INBOX", "Folders/Invoices, paid", "All Mail"}
	if len(cfg.AllowedFolders) != len(want) {
		t.Fatalf("AllowedFolders = %q, want %q", cfg.AllowedFolders, want)
	}
	for i, name := range want {
		if cfg.AllowedFolders[i] != name {
			t.Errorf("folder %d = %q, want %q", i, cfg.AllowedFolders[i], name)
		}
	}

	for _, bad := range []string{",  ,", "INBOX\nSent", `"unterminated`} {
		m["BARYON_ALLOWED_FOLDERS"] = bad
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "BARYON_ALLOWED_FOLDERS") {
			t.Errorf("value %q: got err %v, want rejection", bad, err)
		}
	}
}

func TestManagedAttachmentRootDefaultsAndYields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the attachment tools refuse local paths on Windows, so there is no managed root")
	}
	home := t.TempDir()
	m := validEnv()
	m["XDG_CONFIG_HOME"] = home
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(home, "baryon-mcp", "attachments")
	if cfg.ManagedAttachmentRoot != want {
		t.Errorf("ManagedAttachmentRoot = %q, want %q", cfg.ManagedAttachmentRoot, want)
	}
	// Load only resolves the path: `baryon-mcp setup` runs it too, and must not
	// leave directories behind as a side effect.
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("Load created %q; it should only resolve the path", want)
	}
	if len(cfg.AttachmentRoots) != 0 {
		t.Errorf("AttachmentRoots = %v, want empty until the root is activated", cfg.AttachmentRoots)
	}

	if err := cfg.ActivateManagedAttachmentRoot(); err != nil {
		t.Fatalf("ActivateManagedAttachmentRoot: %v", err)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("managed root: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("managed root mode = %v, want 0700", info.Mode().Perm())
	}
	resolved, _ := filepath.EvalSymlinks(want)
	if len(cfg.AttachmentRoots) != 1 || cfg.AttachmentRoots[0] != resolved {
		t.Errorf("AttachmentRoots = %v, want [%s]", cfg.AttachmentRoots, resolved)
	}

	// An existing directory left world-readable is tightened, not accepted.
	loose := filepath.Join(t.TempDir(), "baryon-mcp", "attachments")
	if err := os.MkdirAll(loose, 0o777); err != nil {
		t.Fatal(err)
	}
	other := &Config{ManagedAttachmentRoot: loose}
	if err := other.ActivateManagedAttachmentRoot(); err != nil {
		t.Fatalf("ActivateManagedAttachmentRoot: %v", err)
	}
	if info, err := os.Stat(loose); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("existing directory mode = %v (%v), want 0700", info.Mode().Perm(), err)
	}
}

func TestExplicitAttachmentRootsReplaceTheManagedDefault(t *testing.T) {
	dir := t.TempDir()
	m := validEnv()
	m["XDG_CONFIG_HOME"] = t.TempDir()
	m["BARYON_ATTACHMENT_ROOTS"] = dir
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ManagedAttachmentRoot != "" {
		t.Errorf("ManagedAttachmentRoot = %q, want empty when roots are configured", cfg.ManagedAttachmentRoot)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if len(cfg.AttachmentRoots) != 1 || cfg.AttachmentRoots[0] != resolved {
		t.Errorf("AttachmentRoots = %v, want only the explicit root", cfg.AttachmentRoots)
	}
	if err := cfg.ActivateManagedAttachmentRoot(); err != nil || len(cfg.AttachmentRoots) != 1 {
		t.Errorf("activation with explicit roots: got (%v, %v), want no change", cfg.AttachmentRoots, err)
	}
}

func TestUnresolvedTemplatesCountAsUnset(t *testing.T) {
	m := validEnv()
	m["XDG_CONFIG_HOME"] = t.TempDir()
	m["BARYON_ALLOWED_FOLDERS"] = "${user_config.allowed_folders}"
	m["BARYON_SENDER_IDENTITIES"] = "${user_config.sender_identities}"
	m["BARYON_ATTACHMENT_ROOTS"] = "${user_config.attachment_root}"
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AllowedFolders != nil {
		t.Errorf("AllowedFolders = %v, want unset", cfg.AllowedFolders)
	}
	if len(cfg.SenderIdentities) != 1 || cfg.SenderIdentities[0].Address != "alice@proton.me" {
		t.Errorf("SenderIdentities = %+v, want the username fallback", cfg.SenderIdentities)
	}
	if runtime.GOOS != "windows" && cfg.ManagedAttachmentRoot == "" {
		t.Error("ManagedAttachmentRoot is empty; an unresolved template must fall back to the managed default")
	}
}

// An unwritable managed directory fails startup, naming the setting that
// moves it: a container under a UID with no home directory hits this.
func TestActivateManagedAttachmentRootFailsActionably(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the test process cannot write to")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{ManagedAttachmentRoot: filepath.Join(locked, "baryon-mcp", "attachments")}
	err := cfg.ActivateManagedAttachmentRoot()
	if err == nil {
		t.Fatal("expected the unwritable directory to fail startup")
	}
	if !strings.Contains(err.Error(), "BARYON_ATTACHMENT_ROOTS") {
		t.Errorf("error %v should name the setting that moves the boundary", err)
	}
	if len(cfg.AttachmentRoots) != 0 {
		t.Errorf("AttachmentRoots = %v, want nothing activated", cfg.AttachmentRoots)
	}
}

// Malformed CSV after a valid first row must fail, not truncate the policy.
func TestAllowedFoldersRejectsTrailingGarbage(t *testing.T) {
	m := validEnv()
	m["XDG_CONFIG_HOME"] = t.TempDir()
	for _, bad := range []string{"INBOX\n\"unterminated", "INBOX\nSent", "INBOX,Sent\nArchive"} {
		m["BARYON_ALLOWED_FOLDERS"] = bad
		cfg, err := Load(env(m))
		if err == nil {
			t.Errorf("value %q: accepted as %q, want rejection", bad, cfg.AllowedFolders)
			continue
		}
		if !strings.Contains(err.Error(), "BARYON_ALLOWED_FOLDERS") {
			t.Errorf("value %q: error %v should name the setting", bad, err)
		}
	}

	// A single row with or without a trailing newline is still fine.
	for _, good := range []string{"INBOX,Sent", "INBOX,Sent\n"} {
		m["BARYON_ALLOWED_FOLDERS"] = good
		cfg, err := Load(env(m))
		if err != nil || len(cfg.AllowedFolders) != 2 {
			t.Errorf("value %q: got (%q, %v), want both folders", good, cfg.AllowedFolders, err)
		}
	}
}

// An introspection-only server must start without creating anything, since
// published images are inspected read-only, and still permit no path.
func TestConfineUnusedAttachmentRootTakesTheBoundaryWithoutTheDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the attachment tools refuse local paths on Windows")
	}
	home := t.TempDir()
	cfg, err := Load(env(map[string]string{
		"BARYON_ALLOW_UNCONFIGURED_INTROSPECTION": "true",
		"XDG_CONFIG_HOME":                         home,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Unconfigured {
		t.Fatal("want an introspection-only config")
	}

	cfg.ConfineUnusedAttachmentRoot()
	managed := filepath.Join(home, "baryon-mcp", "attachments")
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Errorf("introspection-only startup created %q", managed)
	}
	// A configured-but-absent root is what makes the attachment tools refuse
	// every path instead of falling back to the whole filesystem.
	if len(cfg.AttachmentRoots) != 1 || cfg.AttachmentRoots[0] != managed {
		t.Errorf("AttachmentRoots = %v, want the uncreated managed directory", cfg.AttachmentRoots)
	}
}
