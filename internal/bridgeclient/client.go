// Package bridgeclient talks IMAP to a locally-running Proton Mail Bridge.
// Every operation opens a fresh connection (connect → select → operate →
// logout), bounded by a small semaphore: the MCP SDK dispatches tool calls
// concurrently, and mailbox selection is per-connection state in IMAP, so
// sharing one connection across calls would race on SELECT.
package bridgeclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"os"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"

	"github.com/combor/baryon-mcp/internal/config"
)

// maxConcurrentConnections bounds simultaneous IMAP sessions so a burst of
// parallel tool calls cannot exhaust Bridge's connection allowance.
const maxConcurrentConnections = 4

const dialTimeout = 10 * time.Second

// setupTimeout bounds greeting + STARTTLS + TLS handshake; var so tests can shorten it.
var setupTimeout = 30 * time.Second

// Folder describes one IMAP mailbox.
type Folder struct {
	Name       string
	Delimiter  string
	Attributes []string
}

// DraftAttachment is one regular file attachment to save with a draft.
type DraftAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// DraftRef identifies an existing draft within the Drafts mailbox.
type DraftRef struct {
	UID         uint32
	UIDValidity uint32
}

// Draft is the complete desired state of a saved draft. Replace is nil when
// creating a draft and identifies the previous version when updating one.
// InReplyTo and References thread the draft into a conversation; both accept
// identifiers with or without angle brackets. On a replacement nil keeps the
// previous draft's header and a non-nil empty slice removes it, so the two must
// not be conflated.
type Draft struct {
	From        string
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	TextBody    string
	HTMLBody    string
	InReplyTo   []string
	References  []string
	Attachments []DraftAttachment
	Replace     *DraftRef
}

// SavedDraft identifies the newly appended draft and reports whether an old
// version was removed after a replacement.
type SavedDraft struct {
	Folder               string
	UID                  uint32
	UIDValidity          uint32
	ReplacedUID          uint32
	PreviousDraftRemoved bool
	Warning              string
}

// Bridge is the surface the MCP tools consume; *Client implements it and
// tests substitute a fake.
type Bridge interface {
	ListFolders(ctx context.Context) ([]Folder, error)
	ListMessages(ctx context.Context, folder string, criteria SearchCriteria, limit, offset int) (*MessagePage, error)
	GetEmail(ctx context.Context, folder string, uid, uidvalidity uint32) (*EmailContent, error)
	ListAttachments(ctx context.Context, folder string, uid, uidvalidity uint32) ([]AttachmentInfo, error)
	GetAttachment(ctx context.Context, folder string, uid, uidvalidity uint32, index int) (*AttachmentContent, error)
	SaveDraft(ctx context.Context, draft Draft) (*SavedDraft, error)
}

// Client implements Bridge against a real Proton Mail Bridge instance.
type Client struct {
	cfg       *config.Config
	tlsConfig *tlsConfigHolder
	sem       chan struct{}
	draftGate chan struct{}
}

// tlsConfigHolder keeps the resolved TLS material together with how it was
// resolved, for the startup log line.
type tlsConfigHolder struct {
	config *tls.Config
	source string
}

// New builds a Client, resolving TLS peer authentication up front so that
// misconfiguration fails at startup with an actionable message, not on the
// first tool call.
func New(cfg *config.Config) (*Client, error) {
	tlsCfg, source, err := buildTLSConfig(cfg.TLSCertPath, cfg.AllowInsecure, certProbePaths())
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "baryon-mcp: bridge TLS peer authentication: %s\n", source)
	return &Client{
		cfg:       cfg,
		tlsConfig: &tlsConfigHolder{config: tlsCfg, source: source},
		sem:       make(chan struct{}, maxConcurrentConnections),
		draftGate: make(chan struct{}, 1),
	}, nil
}

// withSession acquires a semaphore slot, dials, authenticates, runs fn with
// the ready session, and always tears everything down afterwards. Scoping the
// session to fn means a connection — and, more importantly, its semaphore
// slot — can never leak: a leaked slot would eventually deadlock every tool
// call.
func (c *Client) withSession(ctx context.Context, fn func(cli *imapclient.Client) error) error {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sem }()

	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()
	conn, err := new(net.Dialer).DialContext(dialCtx, "tcp", c.cfg.Addr())
	if err != nil {
		return fmt.Errorf("connecting to bridge at %s: %w", c.cfg.Addr(), err)
	}

	// go-imap commands take no context; closing the connection is the only way to interrupt them.
	stopWatch := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopWatch()

	// go-imap runs the STARTTLS handshake without a read deadline.
	setupTimer := time.AfterFunc(setupTimeout, func() { _ = conn.Close() })

	tlsCfg := c.tlsConfig.config.Clone()
	tlsCfg.ServerName = c.cfg.Host
	tlsCfg.NextProtos = []string{"imap"}
	opts := &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	}

	var cli *imapclient.Client
	switch c.cfg.Security {
	case config.SecurityTLS:
		cli = imapclient.New(tls.Client(conn, tlsCfg), opts)
	default:
		startTLSOpts := *opts
		startTLSOpts.TLSConfig = tlsCfg
		cli, err = imapclient.NewStartTLS(conn, &startTLSOpts)
		if err != nil {
			setupTimer.Stop()
			_ = conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("STARTTLS with bridge at %s: %w", c.cfg.Addr(), err)
		}
	}

	if err := cli.WaitGreeting(); err != nil {
		setupTimer.Stop()
		_ = cli.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("establishing session with bridge at %s (%s): %w", c.cfg.Addr(), c.cfg.Security, err)
	}
	setupTimer.Stop()

	defer func() {
		_ = cli.Logout().Wait()
		_ = cli.Close()
	}()

	if err := cli.Login(c.cfg.Username, c.cfg.Password).Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("bridge login failed (check the Bridge-generated username/password, not the Proton account password): %w", err)
	}

	if err := fn(cli); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

// ListFolders returns all mailboxes via LIST "" "*".
func (c *Client) ListFolders(ctx context.Context) ([]Folder, error) {
	var folders []Folder
	err := c.withSession(ctx, func(cli *imapclient.Client) error {
		mailboxes, err := cli.List("", "*", nil).Collect()
		if err != nil {
			return fmt.Errorf("listing folders: %w", err)
		}
		folders = make([]Folder, 0, len(mailboxes))
		for _, m := range mailboxes {
			f := Folder{Name: m.Mailbox}
			if m.Delim != 0 {
				f.Delimiter = string(m.Delim)
			}
			for _, attr := range m.Attrs {
				f.Attributes = append(f.Attributes, string(attr))
			}
			folders = append(folders, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return folders, nil
}
