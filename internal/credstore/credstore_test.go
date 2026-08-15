package credstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/combor/baryon-mcp/internal/config"
)

// fakeKeyring is an in-memory Keyring; setErr, getErr and deleteErr force
// failures and getDelay simulates a locked collection blocking on its unlock
// prompt.
type fakeKeyring struct {
	items     map[string]string
	setErr    error
	getErr    error
	deleteErr error
	getDelay  time.Duration
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{items: map[string]string{}}
}

func (k *fakeKeyring) key(service, user string) string { return service + "\x00" + user }

func (k *fakeKeyring) Get(service, user string) (string, error) {
	if k.getDelay > 0 {
		time.Sleep(k.getDelay)
	}
	if k.getErr != nil {
		return "", k.getErr
	}
	v, ok := k.items[k.key(service, user)]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (k *fakeKeyring) Set(service, user, pass string) error {
	if k.setErr != nil {
		return k.setErr
	}
	k.items[k.key(service, user)] = pass
	return nil
}

func (k *fakeKeyring) Delete(service, user string) error {
	if k.deleteErr != nil {
		return k.deleteErr
	}
	if _, ok := k.items[k.key(service, user)]; !ok {
		return ErrNotFound
	}
	delete(k.items, k.key(service, user))
	return nil
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestSaveKeyringRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	s := OpenWith(dir, kr)

	backend, _, err := s.Save(Credentials{Username: "alice@proton.me", Password: "s3cret"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backend != BackendKeyring {
		t.Fatalf("backend = %v, want BackendKeyring", backend)
	}
	if got := kr.items[kr.key(keyringService, "alice@proton.me")]; got != "s3cret" {
		t.Errorf("keyring item = %q, want the password", got)
	}
	if _, err := os.Stat(filepath.Join(dir, passwordFile)); !os.IsNotExist(err) {
		t.Error("password file exists despite keyring storage")
	}
	if mode := fileMode(t, dir); mode != 0o700 {
		t.Errorf("dir mode = %o, want 700", mode)
	}
	if mode := fileMode(t, filepath.Join(dir, usernameFile)); mode != 0o600 {
		t.Errorf("username file mode = %o, want 600", mode)
	}

	creds, ok := s.Load()
	if !ok {
		t.Fatal("Load found nothing after Save")
	}
	if creds.Username != "alice@proton.me" || creds.Password != "s3cret" {
		t.Errorf("Load = %+v", creds)
	}
}

func TestSaveFallsBackToFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	kr.setErr = errors.New("no secret service")
	s := OpenWith(dir, kr)

	backend, _, err := s.Save(Credentials{Username: "alice@proton.me", Password: "s3cret"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backend != BackendFile {
		t.Fatalf("backend = %v, want BackendFile", backend)
	}
	if mode := fileMode(t, filepath.Join(dir, passwordFile)); mode != 0o600 {
		t.Errorf("password file mode = %o, want 600", mode)
	}

	creds, ok := s.Load()
	if !ok || creds.Password != "s3cret" {
		t.Fatalf("Load = %+v, %v", creds, ok)
	}
}

// A Save that lands in the keyring must remove a pre-existing password file,
// and one that lands in files must remove the keyring item, so a re-run
// leaves exactly one source of truth.
func TestSaveClearsTheOtherStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	s := OpenWith(dir, kr)

	kr.setErr = errors.New("no secret service")
	if _, _, err := s.Save(Credentials{Username: "alice@proton.me", Password: "old"}); err != nil {
		t.Fatalf("file Save: %v", err)
	}
	kr.setErr = nil
	if _, _, err := s.Save(Credentials{Username: "alice@proton.me", Password: "new"}); err != nil {
		t.Fatalf("keyring Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, passwordFile)); !os.IsNotExist(err) {
		t.Error("password file survived a keyring Save")
	}

	kr.setErr = errors.New("no secret service")
	if _, _, err := s.Save(Credentials{Username: "alice@proton.me", Password: "newer"}); err != nil {
		t.Fatalf("second file Save: %v", err)
	}
	if _, ok := kr.items[kr.key(keyringService, "alice@proton.me")]; ok {
		t.Error("keyring item survived a file Save")
	}
	creds, ok := s.Load()
	if !ok || creds.Password != "newer" {
		t.Fatalf("Load = %+v, %v", creds, ok)
	}
}

// Absent items delete cleanly; any other Delete failure must surface as a
// warning, because a superseded password then remains in the keyring.
func TestSaveWarnsWhenKeyringCleanupFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	s := OpenWith(dir, kr)

	if _, _, err := s.Save(Credentials{Username: "alice@proton.me", Password: "old"}); err != nil {
		t.Fatal(err)
	}

	kr.setErr = errors.New("secret service unavailable")
	kr.deleteErr = errors.New("secret service unavailable")
	// The first save used the keyring, so its item may now be stranded.
	backend, warning, err := s.Save(Credentials{Username: "alice@proton.me", Password: "new"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backend != BackendFile {
		t.Fatalf("backend = %v, want BackendFile", backend)
	}
	if warning == "" {
		t.Error("no warning despite a stale keyring item that could not be removed")
	}

	// A file Save with nothing in the keyring is clean: absent items must
	// not produce a warning.
	kr.deleteErr = nil
	kr.items = map[string]string{}
	if _, warning, err := s.Save(Credentials{Username: "alice@proton.me", Password: "newer"}); err != nil || warning != "" {
		t.Errorf("Save = warning %q, err %v; want a clean file save", warning, err)
	}
}

// On a machine that never had a Secret Service, every Delete fails — but
// nothing was ever stored in a keyring, so warning about a stranded secret
// would be false.
func TestSaveDoesNotWarnWithoutAPriorKeyringSave(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	kr.setErr = errors.New("no secret service")
	kr.deleteErr = errors.New("no secret service")
	s := OpenWith(dir, kr)

	if _, warning, err := s.Save(Credentials{Username: "alice@proton.me", Password: "one"}); err != nil || warning != "" {
		t.Errorf("first save: warning %q, err %v; want silence", warning, err)
	}
	if _, warning, err := s.Save(Credentials{Username: "alice@proton.me", Password: "two"}); err != nil || warning != "" {
		t.Errorf("second save: warning %q, err %v; want silence", warning, err)
	}
}

func TestSaveDeletesStaleItemForRenamedUsername(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	s := OpenWith(dir, kr)

	if _, _, err := s.Save(Credentials{Username: "old@proton.me", Password: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Save(Credentials{Username: "new@proton.me", Password: "two"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := kr.items[kr.key(keyringService, "old@proton.me")]; ok {
		t.Error("item for the previous username was not deleted")
	}
	if got := kr.items[kr.key(keyringService, "new@proton.me")]; got != "two" {
		t.Errorf("item for the new username = %q", got)
	}
}

// A keyring that errors — no D-Bus, no provider — must read as "no stored
// credentials", not fail: a container without a keyring still has to start.
func TestLoadTreatsKeyringErrorAsAbsent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, usernameFile), []byte("alice@proton.me"), 0o600); err != nil {
		t.Fatal(err)
	}
	kr := newFakeKeyring()
	kr.getErr = errors.New("dbus: no such service")
	if _, ok := OpenWith(dir, kr).Load(); ok {
		t.Error("Load reported credentials despite a failing keyring and no password file")
	}
}

func TestLoadTimesOutOnBlockedKeyring(t *testing.T) {
	oldTimeout := keyringReadTimeout
	keyringReadTimeout = 20 * time.Millisecond
	defer func() { keyringReadTimeout = oldTimeout }()

	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, usernameFile), []byte("alice@proton.me"), 0o600); err != nil {
		t.Fatal(err)
	}
	kr := newFakeKeyring()
	kr.getDelay = time.Second

	start := time.Now()
	if _, ok := OpenWith(dir, kr).Load(); ok {
		t.Error("Load reported credentials from a blocked keyring")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Load blocked for %s despite the read timeout", elapsed)
	}
}

// install.sh writes both credential files; they must load as-is, and the
// password file must win without the keyring ever being consulted — a locked
// keyring must not block a file-configured machine.
func TestLoadInstallerWrittenFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, usernameFile), []byte("alice@proton.me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, passwordFile), []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kr := newFakeKeyring()
	kr.getDelay = time.Second
	kr.items[kr.key(keyringService, "alice@proton.me")] = "from-keyring"

	start := time.Now()
	creds, ok := OpenWith(dir, kr).Load()
	if !ok {
		t.Fatal("Load found nothing")
	}
	if creds.Password != "from-file" {
		t.Errorf("password = %q, want the file's value", creds.Password)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Load consulted the keyring despite a password file (took %s)", elapsed)
	}
}

// A restored or copied configuration directory can arrive world-readable;
// reuse must tighten it rather than leave the password exposed.
func TestRepairPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{usernameFile, passwordFile, CertFileName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := OpenWith(dir, newFakeKeyring())
	if err := s.RepairPermissions(); err != nil {
		t.Fatalf("RepairPermissions: %v", err)
	}
	if mode := fileMode(t, dir); mode != 0o700 {
		t.Errorf("dir mode = %o, want 700", mode)
	}
	for _, name := range []string{usernameFile, passwordFile, CertFileName} {
		if mode := fileMode(t, filepath.Join(dir, name)); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", name, mode)
		}
	}

	// A store with nothing in it is not an error.
	empty := OpenWith(filepath.Join(t.TempDir(), "absent"), newFakeKeyring())
	if err := empty.RepairPermissions(); err != nil {
		t.Errorf("RepairPermissions on an absent store: %v", err)
	}
}

// A file-backed Save that cannot commit must leave the previous pair intact:
// a new username beside an old password authenticates as neither account.
func TestSaveKeepsPairIntactWhenCommitFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	kr := newFakeKeyring()
	kr.setErr = errors.New("no secret service")
	s := OpenWith(dir, kr)

	if _, _, err := s.Save(Credentials{Username: "old@proton.me", Password: "old-pass"}); err != nil {
		t.Fatal(err)
	}
	// A directory in place of the password file fails the commit rename
	// while leaving the staged username untouched.
	if err := os.Remove(filepath.Join(dir, passwordFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, passwordFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Save(Credentials{Username: "new@proton.me", Password: "new-pass"}); err == nil {
		t.Fatal("Save reported success despite a failed commit")
	}
	username, err := readTrimmed(filepath.Join(dir, usernameFile))
	if err != nil {
		t.Fatal(err)
	}
	if username != "old@proton.me" {
		t.Errorf("username = %q, want the previous one after a failed save", username)
	}
}

func TestDirIsAbsolute(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-config")
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("Dir() = %q, want an absolute path", dir)
	}
}

func TestSaveCert(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	s := OpenWith(dir, newFakeKeyring())

	path, err := s.SaveCert([]byte("PEM DATA"))
	if err != nil {
		t.Fatalf("SaveCert: %v", err)
	}
	if mode := fileMode(t, path); mode != 0o600 {
		t.Errorf("cert mode = %o, want 600", mode)
	}
	got, ok := s.CertPath()
	if !ok || got != path {
		t.Errorf("CertPath = %q, %v", got, ok)
	}
}

func TestGetenvPrecedence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(dir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		usernameFile: "stored@proton.me",
		passwordFile: "stored-pass",
		CertFileName: "PEM",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	getenv := Getenv(func(key string) string {
		switch key {
		case "PROTON_BRIDGE_USERNAME":
			return "env@proton.me"
		case "PROTON_BRIDGE_PASSWORD":
			return "env-pass"
		default:
			return ""
		}
	})

	if got := getenv("PROTON_BRIDGE_USERNAME"); got != "env@proton.me" {
		t.Errorf("username = %q, environment must win", got)
	}
	if got := getenv("PROTON_BRIDGE_PASSWORD"); got != "env-pass" {
		t.Errorf("password = %q, environment must win", got)
	}
	if got := getenv("PROTON_BRIDGE_TLS_CERT"); got != filepath.Join(dir, CertFileName) {
		t.Errorf("cert = %q, want the stored certificate", got)
	}
	if got := getenv("PROTON_BRIDGE_HOST"); got != "" {
		t.Errorf("host = %q, keys the store does not hold must pass through", got)
	}

	// An unresolved MCPB "${...}" template counts as unset and falls back.
	templated := Getenv(func(key string) string {
		if key == "PROTON_BRIDGE_PASSWORD" {
			return "${user_config.bridge_password}"
		}
		return ""
	})
	if got := templated("PROTON_BRIDGE_PASSWORD"); got != "stored-pass" {
		t.Errorf("password = %q, an unresolved template must fall back to the store", got)
	}
}

// The stored pair must never mix with environment credentials: a stored
// password belongs to its stored username only, and a stored username must
// not pair with an environment password.
func TestGetenvRefusesMixedCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(dir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		usernameFile: "stored@proton.me",
		passwordFile: "stored-pass",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	env := func(vars map[string]string) func(string) string {
		return func(key string) string { return vars[key] }
	}

	if got := Getenv(env(map[string]string{"PROTON_BRIDGE_USERNAME": "other@proton.me"}))("PROTON_BRIDGE_PASSWORD"); got != "" {
		t.Errorf("password = %q, the stored password must not serve a different username", got)
	}
	if got := Getenv(env(map[string]string{"PROTON_BRIDGE_USERNAME": "stored@proton.me"}))("PROTON_BRIDGE_PASSWORD"); got != "stored-pass" {
		t.Errorf("password = %q, a matching username must get the stored password", got)
	}
	if got := Getenv(env(map[string]string{"PROTON_BRIDGE_PASSWORD": "env-pass"}))("PROTON_BRIDGE_USERNAME"); got != "" {
		t.Errorf("username = %q, the stored username must not pair with an environment password", got)
	}
}

// Resolving the stored certificate must not touch the keyring: with both
// credentials in the environment, the certificate is the only key that
// reaches the store, and a blocked keyring must not delay startup for
// credentials that can never be used.
func TestGetenvCertLookupSkipsKeyring(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baryon-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A username file without a password file forces Load onto the keyring.
	for name, content := range map[string]string{
		usernameFile: "stored@proton.me",
		CertFileName: "PEM",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kr := newFakeKeyring()
	kr.getDelay = time.Minute
	oldOpen := getenvOpen
	getenvOpen = func() (*Store, error) { return OpenWith(dir, kr), nil }
	defer func() { getenvOpen = oldOpen }()

	getenv := Getenv(func(key string) string {
		switch key {
		case "PROTON_BRIDGE_USERNAME":
			return "env@proton.me"
		case "PROTON_BRIDGE_PASSWORD":
			return "env-pass"
		default:
			return ""
		}
	})

	start := time.Now()
	if got := getenv("PROTON_BRIDGE_TLS_CERT"); got != filepath.Join(dir, CertFileName) {
		t.Errorf("cert = %q, want the stored certificate", got)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("certificate lookup consulted the keyring (took %s)", elapsed)
	}
}

func TestGetenvOmitsMissingCert(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	getenv := Getenv(func(string) string { return "" })
	if got := getenv("PROTON_BRIDGE_TLS_CERT"); got != "" {
		t.Errorf("cert = %q, want empty for a store without one", got)
	}
}

// A container with no stored credentials must still reach introspection-only
// mode through the wrapped getenv.
func TestGetenvKeepsIntrospectionMode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := config.Load(Getenv(func(key string) string {
		if key == "BARYON_ALLOW_UNCONFIGURED_INTROSPECTION" {
			return "true"
		}
		return ""
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Unconfigured {
		t.Error("cfg.Unconfigured = false, want introspection-only mode")
	}
}

// With credentials stored, the same introspection flag must yield a
// configured server: the flag only applies when both credentials are absent.
func TestGetenvStoredCredentialsBeatIntrospection(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", parent)
	dir := filepath.Join(parent, "baryon-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		usernameFile: "stored@proton.me",
		passwordFile: "stored-pass",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(Getenv(func(key string) string {
		if key == "BARYON_ALLOW_UNCONFIGURED_INTROSPECTION" {
			return "true"
		}
		return ""
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Unconfigured {
		t.Error("cfg.Unconfigured = true despite stored credentials")
	}
	if cfg.Username != "stored@proton.me" || cfg.Password != "stored-pass" {
		t.Errorf("credentials = %q/%q", cfg.Username, cfg.Password)
	}
}
