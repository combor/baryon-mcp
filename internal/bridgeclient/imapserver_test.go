package bridgeclient

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/combor/baryon-mcp/internal/config"
)

// serverTLSCert generates a self-signed server certificate and returns it
// with its PEM encoding for pinning.
func serverTLSCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pemBytes
}

func testMessage(n int, from string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: me@example.org\r\nSubject: Message %d\r\nDate: Wed, 0%d Jul 2026 10:00:00 +0000\r\nMessage-ID: <%d@test>\r\n\r\nbody %d\r\n",
		from, n, n, n, n))
}

// startMemServer runs a TLS-wrapped in-process IMAP server seeded via seed
// and returns a *Client pinned to the server's certificate.
func startMemServer(t *testing.T, seed func(u *imapmemserver.User)) *Client {
	t.Helper()
	cert, certPEM := serverTLSCert(t)

	memSrv := imapmemserver.New()
	user := imapmemserver.NewUser("user", "pass")
	seed(user)
	memSrv.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memSrv.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	go func() { _ = srv.Serve(tlsLn) }()
	t.Cleanup(func() { _ = srv.Close() })

	certPath := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	tlsCfg, _, err := buildTLSConfig(certPath, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return &Client{
		cfg: &config.Config{
			Username: "user", Password: "pass",
			Host: host, Port: port,
			Security: config.SecurityTLS,
		},
		tlsConfig: &tlsConfigHolder{config: tlsCfg},
		sem:       make(chan struct{}, maxConcurrentConnections),
	}
}

func seedInbox(t *testing.T) *Client {
	t.Helper()
	return startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 5; n++ {
			from := "alice@example.org"
			if n == 3 {
				from = "bob@example.org"
			}
			msg := testMessage(n, from)
			opts := &imap.AppendOptions{Time: time.Date(2026, 7, n, 10, 0, 0, 0, time.UTC)}
			if n == 2 {
				opts.Flags = []imap.Flag{imap.FlagSeen}
			}
			if _, err := u.Append("INBOX", bytes.NewReader(msg), opts); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestProtocolListFolders(t *testing.T) {
	c := seedInbox(t)
	folders, err := c.ListFolders(context.Background())
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	var found bool
	for _, f := range folders {
		if f.Name == "INBOX" {
			found = true
		}
	}
	if !found {
		t.Errorf("INBOX missing from %+v", folders)
	}
}

func TestProtocolListMessagesPagination(t *testing.T) {
	c := seedInbox(t)

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, 2, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if page.Total != 5 || len(page.Emails) != 2 {
		t.Fatalf("got total=%d returned=%d, want 5/2", page.Total, len(page.Emails))
	}
	if page.UIDValidity == 0 {
		t.Error("UIDValidity should be nonzero")
	}
	if page.Emails[0].Subject != "Message 5" || page.Emails[1].Subject != "Message 4" {
		t.Errorf("not newest-first: %q, %q", page.Emails[0].Subject, page.Emails[1].Subject)
	}
	if page.Emails[0].UID <= page.Emails[1].UID {
		t.Errorf("UIDs not descending: %d, %d", page.Emails[0].UID, page.Emails[1].UID)
	}

	page, err = c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Emails) != 1 || page.Emails[0].Subject != "Message 1" {
		t.Errorf("offset page: total=%d emails=%+v", page.Total, page.Emails)
	}

	page, err = c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || len(page.Emails) != 0 {
		t.Errorf("past-end page: total=%d returned=%d", page.Total, len(page.Emails))
	}
}

func TestProtocolUnreadOnly(t *testing.T) {
	c := seedInbox(t)
	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{UnreadOnly: true}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 {
		t.Errorf("total = %d, want 4 unread", page.Total)
	}
	for _, e := range page.Emails {
		if e.Seen {
			t.Errorf("seen message %d in unread-only results", e.UID)
		}
		if e.Subject == "Message 2" {
			t.Error("Message 2 is seen and should be filtered")
		}
	}
}

func TestProtocolSearchCriteria(t *testing.T) {
	c := seedInbox(t)

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{Subject: "Message 3"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Emails[0].Subject != "Message 3" {
		t.Errorf("subject search: %+v", page.Emails)
	}
	if len(page.Emails[0].From) != 1 || page.Emails[0].From[0] != "bob@example.org" {
		t.Errorf("from = %+v", page.Emails[0].From)
	}

	page, err = c.ListMessages(context.Background(), "INBOX", SearchCriteria{From: "bob@example.org"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Errorf("from search total = %d, want 1", page.Total)
	}
}

func TestProtocolSelectMissingFolder(t *testing.T) {
	c := seedInbox(t)
	_, err := c.ListMessages(context.Background(), "NoSuchFolder", SearchCriteria{}, 10, 0)
	if err == nil {
		t.Fatal("expected error selecting missing folder")
	}
}
