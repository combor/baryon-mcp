package bridgeclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/combor/baryon-mcp/internal/config"
)

// FetchServerCertificate connects to Bridge's IMAP endpoint and returns the
// leaf certificate the server presents, without verifying it and without
// logging in. It exists for setup's certificate capture on installs where
// Bridge's GUI export is unavailable: the caller decides — after checking who
// owns the listening socket — whether the returned certificate deserves to be
// pinned. Nothing here trusts the peer, and no credentials touch the
// connection.
func FetchServerCertificate(ctx context.Context, cfg *config.Config) (*x509.Certificate, error) {
	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()
	conn, err := new(net.Dialer).DialContext(dialCtx, "tcp", cfg.Addr())
	if err != nil {
		return nil, fmt.Errorf("connecting to bridge at %s: %w", cfg.Addr(), err)
	}
	defer func() { _ = conn.Close() }()

	stopWatch := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopWatch()
	setupTimer := time.AfterFunc(setupTimeout, func() { _ = conn.Close() })
	defer setupTimer.Stop()

	var captured []byte
	tlsCfg := &tls.Config{
		ServerName: cfg.Host,
		NextProtos: []string{"imap"},
		// Verification is deliberately absent: the certificate is the thing
		// being fetched, and the caller judges it before any pinning.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("bridge presented no certificate")
			}
			captured = rawCerts[0]
			return nil
		},
	}

	switch cfg.Security {
	case config.SecurityTLS:
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("TLS handshake with bridge at %s: %w", cfg.Addr(), err)
		}
		_ = tlsConn.Close()
	default:
		// NewStartTLS completes the handshake before returning, so the
		// capture callback has fired by the time it succeeds.
		cli, err := imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: tlsCfg})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("STARTTLS with bridge at %s: %w", cfg.Addr(), err)
		}
		_ = cli.Close()
	}

	if captured == nil {
		return nil, fmt.Errorf("bridge at %s presented no certificate", cfg.Addr())
	}
	cert, err := x509.ParseCertificate(captured)
	if err != nil {
		return nil, fmt.Errorf("parsing the certificate presented by bridge at %s: %w", cfg.Addr(), err)
	}
	return cert, nil
}
