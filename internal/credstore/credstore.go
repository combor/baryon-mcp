// Package credstore reads and writes the Bridge credentials that `baryon-mcp
// setup` stores, so a packaged binary can serve without a wrapper script
// injecting environment variables. The password lives in the login keyring
// when a Secret Service answers and in a mode-600 file otherwise; the
// username — which the MCPB manifest treats as non-secret — is always a
// plain file and doubles as the keyring lookup key.
package credstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/combor/baryon-mcp/internal/config"
)

const (
	// keyringService names the Secret Service item. go-keyring labels items
	// "Password for '<user>' on '<service>'", so a keyring browser shows
	// "Password for 'you@proton.me' on 'baryon-mcp-proton-bridge'" — both
	// Baryon's ownership and the credential's origin stay legible, and
	// `secret-tool lookup service baryon-mcp-proton-bridge username <address>`
	// finds it without quoting.
	keyringService = "baryon-mcp-proton-bridge"

	usernameFile = "bridge-username"
	passwordFile = "bridge-password"

	// CertFileName is the pinned Bridge certificate's name inside Dir.
	CertFileName = "cert.pem"
)

// keyringReadTimeout bounds keyring reads on the serving path. A locked
// collection makes the Secret Service block on the session's unlock prompt,
// and an MCP server that hangs before its first response is a worse failure
// than falling through to "no stored credentials".
var keyringReadTimeout = 10 * time.Second

// Credentials is a stored Bridge username/password pair.
type Credentials struct {
	Username string
	Password string
}

// Backend identifies which store holds the saved password.
type Backend int

const (
	BackendKeyring Backend = iota
	BackendFile
)

// ErrNotFound reports that a keyring item does not exist. Deleting an absent
// item is clean; any other Delete failure means a stored secret may linger.
var ErrNotFound = errors.New("keyring item not found")

// Keyring abstracts the platform secret store so tests can inject a fake.
// Load treats every Get failure as "no stored credentials", so fakes may
// return any error; Delete distinguishes ErrNotFound from real failures.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, pass string) error
	Delete(service, user string) error
}

// Store reads and writes credentials below one configuration directory.
type Store struct {
	dir string
	kr  Keyring
}

// Dir returns the configuration directory,
// ${XDG_CONFIG_HOME:-$HOME/.config}/baryon-mcp — the same location
// install.sh uses on both Linux and macOS, so credentials written by the
// installer are read without migration. The result is absolute: a relative
// XDG_CONFIG_HOME would otherwise resolve against the working directory,
// which differs between setup and a server launched by an MCP client.
func Dir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		dir, err := filepath.Abs(filepath.Join(v, "baryon-mcp"))
		if err != nil {
			return "", fmt.Errorf("resolving the configuration directory: %w", err)
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving the configuration directory: %w", err)
	}
	return filepath.Join(home, ".config", "baryon-mcp"), nil
}

// Open returns a Store over Dir() and the system keyring.
func Open() (*Store, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return OpenWith(dir, systemKeyring{}), nil
}

// OpenWith returns a Store over an explicit directory and keyring, so tests
// never touch the caller's configuration or the real secret store.
func OpenWith(dir string, kr Keyring) *Store {
	return &Store{dir: dir, kr: kr}
}

// Dir returns the directory this store reads and writes.
func (s *Store) Dir() string {
	return s.dir
}

// CertPath returns the stored certificate's path when the file exists.
func (s *Store) CertPath() (string, bool) {
	p := filepath.Join(s.dir, CertFileName)
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return p, true
}

// RepairPermissions tightens the directory to 0700 and every stored file to
// 0600. A restore from backup, a careless umask, or a copied configuration
// directory can leave the Bridge password world-readable, and reusing it
// unchanged would keep it exposed.
func (s *Store) RepairPermissions() error {
	if err := os.Chmod(s.dir, 0o700); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, name := range []string{usernameFile, passwordFile, CertFileName} {
		if err := os.Chmod(filepath.Join(s.dir, name), 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SaveCert writes PEM data as the stored certificate at mode 0600 and
// returns its path.
func (s *Store) SaveCert(pemData []byte) (string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, CertFileName)
	if err := writeSecretFile(path, string(pemData)); err != nil {
		return "", err
	}
	return path, nil
}

// Load returns the stored credentials, or ok=false when the store holds
// none. The password file wins over the keyring: Save leaves exactly one of
// the two behind, so a present file means either the keyring was unavailable
// at save time or an installer wrote it — and consulting a possibly locked
// keyring first would block on an unlock prompt the file makes unnecessary.
// Every keyring failure counts as "no stored credentials", never an error: a
// container without D-Bus must start in introspection-only mode, not fail.
func (s *Store) Load() (Credentials, bool) {
	username, err := readTrimmed(filepath.Join(s.dir, usernameFile))
	if err != nil || username == "" {
		return Credentials{}, false
	}

	if password, err := readTrimmed(filepath.Join(s.dir, passwordFile)); err == nil && password != "" {
		return Credentials{Username: username, Password: password}, true
	}

	password, err := getWithTimeout(s.kr, keyringService, username)
	if err != nil || password == "" {
		return Credentials{}, false
	}
	return Credentials{Username: username, Password: password}, true
}

// Save stores creds, preferring the keyring and falling back to a mode-600
// file, and removes whichever store it did not use so exactly one source of
// truth survives a re-run. The item pinned under a previous username is
// deleted too, so changing the Bridge address does not strand a secret. The
// returned warning is non-empty when such a cleanup delete failed: the save
// itself succeeded, but a superseded password may remain in the keyring.
func (s *Store) Save(creds Credentials) (Backend, string, error) {
	if creds.Username == "" || creds.Password == "" {
		return 0, "", fmt.Errorf("both a Bridge username and password are required")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return 0, "", err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return 0, "", err
	}

	previousUsername, _ := readTrimmed(filepath.Join(s.dir, usernameFile))
	// Exactly one store holds the password, so an existing password file
	// means the previous save did not use the keyring — and a delete that
	// then fails is reporting the missing Secret Service, not a secret left
	// behind.
	previousWasKeyring := previousUsername != "" && !fileExists(filepath.Join(s.dir, passwordFile))

	if err := s.kr.Set(keyringService, creds.Username, creds.Password); err == nil {
		if err := writeSecretFile(filepath.Join(s.dir, usernameFile), creds.Username); err != nil {
			return 0, "", err
		}
		var warning string
		if previousUsername != "" && previousUsername != creds.Username {
			warning = s.clearKeyringItems(previousUsername)
		}
		if err := os.Remove(filepath.Join(s.dir, passwordFile)); err != nil && !os.IsNotExist(err) {
			return 0, "", fmt.Errorf("removing the superseded password file: %w", err)
		}
		return BackendKeyring, warning, nil
	}

	// Both files are staged before either is committed, so a write that
	// fails partway leaves the previous username and password paired as
	// they were rather than a new username beside an old password.
	stagedUsername, err := stageSecretFile(filepath.Join(s.dir, usernameFile), creds.Username)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = os.Remove(stagedUsername) }()
	stagedPassword, err := stageSecretFile(filepath.Join(s.dir, passwordFile), creds.Password)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = os.Remove(stagedPassword) }()

	if err := os.Rename(stagedPassword, filepath.Join(s.dir, passwordFile)); err != nil {
		return 0, "", err
	}
	if err := os.Rename(stagedUsername, filepath.Join(s.dir, usernameFile)); err != nil {
		return 0, "", err
	}
	warning := s.clearKeyringItems(creds.Username, previousUsername)
	if !previousWasKeyring {
		warning = ""
	}
	return BackendFile, warning, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// clearKeyringItems deletes the items for the given usernames. An absent
// item is clean; any other failure is reported as a warning rather than an
// error, because the credentials themselves were stored — only a stale copy
// of an old password may remain behind.
func (s *Store) clearKeyringItems(usernames ...string) string {
	seen := map[string]bool{}
	var failed []string
	for _, username := range usernames {
		if username == "" || seen[username] {
			continue
		}
		seen[username] = true
		if err := s.kr.Delete(keyringService, username); err != nil && !errors.Is(err, ErrNotFound) {
			failed = append(failed, username)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("could not remove the keyring item for %s; a previously stored Bridge password may remain in the keyring",
		strings.Join(failed, ", "))
}

// getenvOpen is Open; a variable so tests can substitute a store with a fake
// keyring.
var getenvOpen = Open

// Getenv wraps base so that the three Bridge variables setup stores fall back
// to the stored credentials when the real environment leaves them unset.
// Environment always wins: values from the MCPB manifest, the Docker
// configuration, and explicit -e flags take precedence over the store, and
// the stored username/password pair is never mixed with credentials from the
// environment. The store is consulted lazily and at most once — config.Load
// reads keys repeatedly.
func Getenv(base func(string) string) func(string) string {
	var (
		store       *Store
		storeOpened bool
		creds       Credentials
		found       bool
		credsLoaded bool
		certPath    string
		certLoaded  bool
	)
	openStore := func() *Store {
		if !storeOpened {
			storeOpened = true
			store, _ = getenvOpen()
		}
		return store
	}
	load := func() {
		if credsLoaded {
			return
		}
		credsLoaded = true
		if s := openStore(); s != nil {
			creds, found = s.Load()
		}
	}
	// The certificate is resolved without Load: with both credentials set in
	// the environment, only this key reaches the store, and a plain file
	// stat must not wait out the keyring timeout for credentials that can
	// never be used.
	loadCert := func() {
		if certLoaded {
			return
		}
		certLoaded = true
		if s := openStore(); s != nil {
			if p, ok := s.CertPath(); ok {
				certPath = p
			}
		}
	}
	return func(key string) string {
		v := base(key)
		if !config.Unset(v) {
			return v
		}
		switch key {
		// The stored pair is atomic: a stored username is supplied only when
		// the environment sets no password, and the stored password only for
		// the username it was saved with. Half-configured environments must
		// fail config.Load's explicit missing-credential checks, not be
		// completed with a value belonging to a different account.
		case "PROTON_BRIDGE_USERNAME":
			if !config.Unset(base("PROTON_BRIDGE_PASSWORD")) {
				return v
			}
			load()
			if found {
				return creds.Username
			}
		case "PROTON_BRIDGE_PASSWORD":
			load()
			if found {
				if u := base("PROTON_BRIDGE_USERNAME"); config.Unset(u) || u == creds.Username {
					return creds.Password
				}
			}
		case "PROTON_BRIDGE_TLS_CERT":
			loadCert()
			// Resolved only when the file exists, so config.Load's stat
			// check never sees a path that is not there.
			return certPath
		}
		return v
	}
}

// getWithTimeout runs a keyring read with keyringReadTimeout. The goroutine
// is abandoned on timeout; the read holds no resources worth waiting for.
func getWithTimeout(kr Keyring, service, user string) (string, error) {
	type result struct {
		value string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := kr.Get(service, user)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.value, r.err
	case <-time.After(keyringReadTimeout):
		return "", fmt.Errorf("keyring did not answer within %s (locked collection?)", keyringReadTimeout)
	}
}

// readTrimmed returns the file's contents without a trailing newline, so
// hand-edited files behave like installer-written ones.
func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// writeSecretFile writes value at mode 0600 via a same-directory temp file and
// rename, so a crash never leaves a partially written credential and the mode
// is in force before any content is.
func writeSecretFile(path, value string) error {
	staged, err := stageSecretFile(path, value)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, path); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

// stageSecretFile writes value to a mode-0600 temp file beside path and
// returns its name, ready to be renamed into place. The caller commits it
// with os.Rename or discards it with os.Remove.
func stageSecretFile(path, value string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if _, err := tmp.WriteString(value); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
