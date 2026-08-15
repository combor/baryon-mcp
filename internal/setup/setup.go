// Package setup implements `baryon-mcp setup`: it pins Bridge's TLS
// certificate, stores the Bridge credentials the server later loads through
// credstore, and registers the server with local MCP clients. It does for a
// package-manager install what scripts/install.sh does for a downloaded one,
// with the difference that clients are pointed at the binary itself — the
// server reads the stored credentials natively, so no launcher script exists.
package setup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
	"github.com/combor/baryon-mcp/internal/credstore"
)

// bridgeListener describes the verified owner of Bridge's IMAP socket. The
// socket inode identifies the exact listening socket, so a re-check can tell
// "still the same socket" from "someone rebound the port".
type bridgeListener struct {
	pid   int
	exe   string
	uid   uint32
	inode string
}

// errCaptureUnsupported marks platforms where the listener cannot be
// identified; the certificate step then falls back to prompting for a path.
var errCaptureUnsupported = errors.New("certificate capture from a running Bridge is only supported on Linux")

type options struct {
	clients           []string
	tlsCert           string
	captureCert       bool
	resetCredentials  bool
	forceClientConfig bool
	skipClientConfig  bool
}

const defaultClients = "claude codex"

// runner carries the streams and seams; tests construct it directly with a
// fixture procfs and buffer streams.
type runner struct {
	store    *credstore.Store
	stdin    *bufio.Reader
	rawStdin io.Reader
	stdout   io.Writer
	stderr   io.Writer
	procRoot string
	getenv   func(string) string
	// beforeRecheck runs between the capture and its listener re-check, so
	// tests can simulate a rebind inside that window.
	beforeRecheck func()
}

// Command returns the `setup` subcommand, bound to the process streams. The
// credential store is opened when the command runs, so a misresolved home
// directory is reported as a command failure rather than at wiring time.
func Command(stdin io.Reader, stdout, stderr io.Writer) *cli.Command {
	r := &runner{
		stdin:    bufio.NewReader(stdin),
		rawStdin: stdin,
		stdout:   stdout,
		stderr:   stderr,
		procRoot: "/proc",
		getenv:   os.Getenv,
	}
	return r.command()
}

// command describes the subcommand and runs the flow with the parsed flags.
func (r *runner) command() *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Store Proton Bridge credentials and register MCP clients",
		Description: "The Bridge password goes into the system keyring when one answers, and\n" +
			"otherwise into a mode-600 file below the user's configuration directory.",
		Reader: r.rawStdin,
		// Help and errors go to stderr, as they do for the root command:
		// stdout carries the server's JSON-RPC stream and, here, the flow's
		// own summary.
		Writer:    r.stderr,
		ErrWriter: r.stderr,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:      "client",
				Usage:     "Configure `NAME` (claude or codex; repeatable, default: both)",
				Validator: validateClients,
			},
			&cli.StringFlag{
				Name:  "tls-cert",
				Usage: "Proton Bridge exported cert.pem at `PATH`",
			},
			&cli.BoolFlag{
				Name:  "capture-cert",
				Usage: "Pin the running Bridge's certificate without prompting, replacing any stored pin",
			},
			&cli.BoolFlag{
				Name:  "reset-credentials",
				Usage: "Replace stored Bridge credentials",
			},
			&cli.BoolFlag{
				Name:  "force-client-config",
				Usage: "Replace existing baryon client entries",
			},
			&cli.BoolFlag{
				Name:  "skip-client-config",
				Usage: "Store credentials without configuring clients",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() > 0 {
				return fmt.Errorf("unexpected argument: %s", cmd.Args().First())
			}
			if r.store == nil {
				store, err := credstore.Open()
				if err != nil {
					return err
				}
				r.store = store
			}
			return r.execute(optionsFrom(cmd))
		},
	}
}

// run parses args as the subcommand would and executes the flow.
func (r *runner) run(args []string) error {
	return r.command().Run(context.Background(), append([]string{"setup"}, args...))
}

// validateClients rejects unsupported client names up front, so a typo fails
// before any credential is touched rather than silently configuring nothing.
func validateClients(names []string) error {
	for _, name := range names {
		if !slices.Contains(strings.Fields(defaultClients), name) {
			return fmt.Errorf("unsupported client %q (supported: %s)", name, defaultClients)
		}
	}
	return nil
}

func optionsFrom(cmd *cli.Command) *options {
	// Repeats are dropped wherever they appear, not just adjacently: naming
	// a client twice must configure it once, and with --force-client-config
	// a second pass would remove and re-add the entry just written.
	var clients []string
	for _, name := range cmd.StringSlice("client") {
		if !slices.Contains(clients, name) {
			clients = append(clients, name)
		}
	}
	if len(clients) == 0 {
		clients = strings.Fields(defaultClients)
	}
	return &options{
		clients:           clients,
		tlsCert:           cmd.String("tls-cert"),
		captureCert:       cmd.Bool("capture-cert"),
		resetCredentials:  cmd.Bool("reset-credentials"),
		forceClientConfig: cmd.Bool("force-client-config"),
		skipClientConfig:  cmd.Bool("skip-client-config"),
	}
}

func (r *runner) execute(opts *options) error {
	certPath, err := r.ensureCertificate(opts)
	if err != nil {
		return err
	}
	credentialStore, err := r.ensureCredentials(opts)
	if err != nil {
		return err
	}
	serverPath, err := r.configureClients(opts)
	if err != nil {
		return err
	}

	r.sayf("Configured baryon-mcp")
	r.sayf("  server:      %s", serverPath)
	r.sayf("  credentials: %s", credentialStore)
	r.sayf("  certificate: %s", certPath)
	return nil
}

// ensureCertificate resolves a certificate, trying in order: an explicit
// path, the environment, the stored copy, Bridge's well-known export
// locations, capture from the running Bridge, and finally a prompt.
// --capture-cert jumps straight to capture: a headless Bridge that
// regenerated its certificate leaves nothing to export, so the stored copy
// must be replaceable without a file to point --tls-cert at.
func (r *runner) ensureCertificate(opts *options) (string, error) {
	source := opts.tlsCert
	if source == "" && opts.captureCert {
		return r.captureCertificate(opts)
	}
	if source == "" {
		if v := r.getenv("PROTON_BRIDGE_TLS_CERT"); !config.Unset(v) {
			source = v
		}
	}

	if source == "" {
		if existing, ok := r.store.CertPath(); ok {
			// An unparseable pin fails every server launch, so it is worth
			// replacing rather than reusing.
			if err := bridgeclient.ValidateCertificateFile(existing); err != nil {
				r.warnf("stored certificate %s is unusable (%v); resolving a replacement", existing, err)
			} else {
				if err := os.Chmod(existing, 0o600); err != nil {
					return "", err
				}
				r.sayf("Reusing %s", existing)
				return existing, nil
			}
		}
		for _, probe := range bridgeclient.CertProbePaths() {
			if _, err := os.Stat(probe); err == nil {
				source = probe
				break
			}
		}
	}

	if source == "" {
		path, err := r.captureCertificate(opts)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, errCaptureUnsupported) {
			return "", err
		}
		line, err := r.promptLine("Path to Proton Bridge's exported cert.pem: ")
		if err != nil {
			return "", fmt.Errorf("a TLS certificate is required: %w", err)
		}
		source = line
	}

	if err := bridgeclient.ValidateCertificateFile(source); err != nil {
		return "", fmt.Errorf("TLS certificate %s: %w", source, err)
	}
	pemData, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	path, err := r.store.SaveCert(pemData)
	if err != nil {
		return "", err
	}
	r.sayf("Pinned certificate %s", path)
	return path, nil
}

// captureCertificate pins the certificate the running Bridge is serving.
// Bridge v3 keeps its certificate inside an encrypted vault and only the GUI
// exports it, so a headless install has nothing on disk to point at; the
// capture is gated on verifying that the process owning the port is Bridge
// running as the current user, and on the user's explicit confirmation.
func (r *runner) captureCertificate(opts *options) (string, error) {
	cfg, err := r.bridgeEndpoint()
	if err != nil {
		return "", err
	}
	listener, err := verifyBridgeListener(r.procRoot, cfg.Port)
	if err != nil {
		return "", err
	}
	cert, err := bridgeclient.FetchServerCertificate(context.Background(), cfg)
	if err != nil {
		return "", err
	}
	// Bridge could have exited between the check above and the dial, letting
	// another process bind the port and serve the certificate just fetched.
	// Re-checking pins the result to the socket that was verified: a rebind
	// yields a different socket inode, whoever owns it.
	if r.beforeRecheck != nil {
		r.beforeRecheck()
	}
	if again, err := verifyBridgeListener(r.procRoot, cfg.Port); err != nil {
		return "", err
	} else if again.inode != listener.inode || again.pid != listener.pid {
		return "", fmt.Errorf(
			"the process listening on %s changed while its certificate was being fetched; refusing to pin it", cfg.Addr())
	}

	r.sayf("Bridge listening on %s", cfg.Addr())
	r.sayf("  process  %s (pid %d, uid %d)", listener.exe, listener.pid, listener.uid)
	r.sayf("  subject  %s", cert.Subject)
	r.sayf("  sha256   %s", fingerprint(cert))
	r.sayf("  expires  %s", cert.NotAfter.Format("2006-01-02"))

	if !opts.captureCert {
		answer, err := r.promptLine("Pin this certificate? [y/N] ")
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			return "", fmt.Errorf("certificate not pinned; re-run with --capture-cert to accept it, or --tls-cert with an exported cert.pem")
		}
	}

	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	path, err := r.store.SaveCert(pemData)
	if err != nil {
		return "", err
	}
	r.sayf("Pinned certificate %s", path)
	return path, nil
}

// bridgeEndpoint resolves host, port and security exactly as the server
// will, by running config.Load with placeholder credentials. Capture must
// target the endpoint the server is later going to dial, including any
// PROTON_BRIDGE_HOST/PORT/SECURITY overrides in the environment.
func (r *runner) bridgeEndpoint() (*config.Config, error) {
	return config.Load(func(key string) string {
		switch key {
		case "PROTON_BRIDGE_USERNAME", "PROTON_BRIDGE_PASSWORD":
			return "baryon-setup"
		case "PROTON_BRIDGE_TLS_CERT", "BARYON_ATTACHMENT_ROOTS":
			// Validated against the filesystem by Load; a stale value must
			// not block resolving the endpoint.
			return ""
		default:
			return r.getenv(key)
		}
	})
}

func fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// ensureCredentials stores the Bridge credentials, reusing what is already
// stored unless --reset-credentials asked otherwise. The returned string
// describes where the password lives, for the summary.
func (r *runner) ensureCredentials(opts *options) (string, error) {
	if !opts.resetCredentials {
		if _, ok := r.store.Load(); ok {
			// Reuse repairs the modes: a credential directory restored from
			// a backup or copied between machines can arrive world-readable.
			if err := r.store.RepairPermissions(); err != nil {
				return "", err
			}
			r.sayf("Reusing stored Proton Bridge credentials")
			return "existing (use --reset-credentials to replace)", nil
		}
	}

	username, err := r.promptLine("Proton Bridge IMAP username: ")
	if err != nil || username == "" {
		return "", fmt.Errorf("a Bridge username is required")
	}
	password, err := r.promptSecret("Proton Bridge-generated password: ")
	if err != nil || password == "" {
		return "", fmt.Errorf("the Bridge-generated password is required")
	}

	backend, warning, err := r.store.Save(credstore.Credentials{Username: username, Password: password})
	if err != nil {
		return "", err
	}
	if warning != "" {
		r.warnf("%s", warning)
	}
	if backend == credstore.BackendKeyring {
		return "system keyring", nil
	}
	return fmt.Sprintf("%s (mode-600 files)", r.store.Dir()), nil
}

// configureClients registers the server binary with each requested MCP
// client CLI and returns the binary path that was registered.
func (r *runner) configureClients(opts *options) (string, error) {
	exe, err := serverExecutable()
	if err != nil {
		return "", err
	}
	if opts.skipClientConfig {
		return exe, nil
	}

	// Client CLIs run in a temp directory so a project-local baryon entry in
	// the caller's directory is not mistaken for the user-level one.
	workDir, err := os.MkdirTemp("", "baryon-mcp-setup.")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	overrides := r.endpointOverrides()
	for _, client := range opts.clients {
		switch client {
		case "claude":
			err = r.configureClaude(exe, workDir, overrides, opts.forceClientConfig)
		case "codex":
			err = r.configureCodex(exe, workDir, overrides, opts.forceClientConfig)
		}
		if err != nil {
			return "", err
		}
	}
	return exe, nil
}

// endpointOverrides returns the Bridge endpoint variables set in setup's own
// environment as KEY=VALUE pairs. They travel into the client entry so that a
// client process, which never sees setup's environment, dials the endpoint
// whose certificate was pinned. Credentials are excluded: those live in the
// store precisely so no client configuration file holds them.
func (r *runner) endpointOverrides() []string {
	var env []string
	for _, key := range []string{
		"PROTON_BRIDGE_HOST",
		"PROTON_BRIDGE_IMAP_PORT",
		"PROTON_BRIDGE_IMAP_SECURITY",
	} {
		if v := r.getenv(key); !config.Unset(v) {
			env = append(env, key+"="+v)
		}
	}
	// The store lives below XDG_CONFIG_HOME. A client process need not
	// inherit setup's value, and would then look for credentials in a
	// directory this run never wrote, so the resolved location travels with
	// the entry whenever it is not the default.
	if !config.Unset(r.getenv("XDG_CONFIG_HOME")) {
		env = append(env, "XDG_CONFIG_HOME="+filepath.Dir(r.store.Dir()))
	}
	return env
}

// serverExecutable returns the path the binary was invoked as — what the
// client entries point at. The invocation path is preferred over
// os.Executable because a package manager's stable symlink (a Homebrew bin
// link, a Nix profile entry) outlives upgrades while its resolved target
// does not, and on Linux os.Executable reads /proc/self/exe, which is
// always the resolved target.
func serverExecutable() (string, error) {
	if invoked := os.Args[0]; invoked != "" {
		if strings.ContainsRune(invoked, os.PathSeparator) {
			return filepath.Abs(invoked)
		}
		if path, err := exec.LookPath(invoked); err == nil {
			return filepath.Abs(path)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

func (r *runner) configureClaude(exe, workDir string, overrides []string, force bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		r.warnf("Claude Code is not installed; skipped Claude configuration")
		return nil
	}
	if r.clientCommand(workDir, "claude", "mcp", "get", "baryon").Run() == nil {
		if !force {
			r.warnf("Claude already has a baryon entry; left it unchanged")
			return nil
		}
		_ = r.clientCommand(workDir, "claude", "mcp", "remove", "--scope", "user", "baryon").Run()
	}
	args := []string{"mcp", "add", "--transport", "stdio", "--scope", "user", "baryon"}
	for _, kv := range overrides {
		args = append(args, "-e", kv)
	}
	args = append(args, "--", exe)
	cmd := r.clientCommand(workDir, "claude", args...)
	cmd.Stdout, cmd.Stderr = r.stderr, r.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not configure Claude Code: %w", err)
	}
	r.sayf("Configured Claude Code (user scope)")
	return nil
}

func (r *runner) configureCodex(exe, workDir string, overrides []string, force bool) error {
	if _, err := exec.LookPath("codex"); err != nil {
		r.warnf("Codex is not installed; skipped Codex configuration")
		return nil
	}
	if r.clientCommand(workDir, "codex", "mcp", "get", "baryon", "--json").Run() == nil {
		if !force {
			r.warnf("Codex already has a baryon entry; left it unchanged")
			return nil
		}
		_ = r.clientCommand(workDir, "codex", "mcp", "remove", "baryon").Run()
	}
	// codex takes its options before the server name.
	args := []string{"mcp", "add"}
	for _, kv := range overrides {
		args = append(args, "--env", kv)
	}
	args = append(args, "baryon", "--", exe)
	cmd := r.clientCommand(workDir, "codex", args...)
	cmd.Stdout, cmd.Stderr = r.stderr, r.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not configure Codex: %w", err)
	}
	r.sayf("Configured Codex")
	return nil
}

func (r *runner) clientCommand(workDir string, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	return cmd
}

func (r *runner) sayf(format string, args ...any) {
	fmt.Fprintf(r.stdout, format+"\n", args...)
}

func (r *runner) warnf(format string, args ...any) {
	fmt.Fprintf(r.stderr, "warning: "+format+"\n", args...)
}

func (r *runner) promptLine(prompt string) (string, error) {
	fmt.Fprint(r.stderr, prompt)
	line, err := r.stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// promptSecret reads without echo on a terminal and falls back to a plain
// line read when stdin is a pipe, so tests and scripts can feed it.
func (r *runner) promptSecret(prompt string) (string, error) {
	if f, ok := r.rawStdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(r.stderr, prompt)
		secret, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(r.stderr)
		return string(secret), err
	}
	return r.promptLine(prompt)
}
