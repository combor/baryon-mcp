package bridgeclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/combor/baryon-mcp/internal/config"
)

func listenerConfig(t *testing.T, ln net.Listener, security config.Security) *config.Config {
	t.Helper()
	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return &config.Config{Host: host, Port: port, Security: security}
}

func TestFetchServerCertificateImplicitTLS(t *testing.T) {
	cert, _ := serverTLSCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()

	got, err := FetchServerCertificate(context.Background(), listenerConfig(t, ln, config.SecurityTLS))
	if err != nil {
		t.Fatalf("FetchServerCertificate: %v", err)
	}
	if !bytes.Equal(got.Raw, cert.Certificate[0]) {
		t.Error("captured certificate does not match what the server presented")
	}
}

func TestFetchServerCertificateStartTLS(t *testing.T) {
	cert, _ := serverTLSCert(t)
	memSrv := imapmemserver.New()
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memSrv.NewSession(), nil, nil
		},
		TLSConfig:    &tls.Config{Certificates: []tls.Certificate{cert}},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	got, err := FetchServerCertificate(context.Background(), listenerConfig(t, ln, config.SecurityStartTLS))
	if err != nil {
		t.Fatalf("FetchServerCertificate: %v", err)
	}
	if !bytes.Equal(got.Raw, cert.Certificate[0]) {
		t.Error("captured certificate does not match what the server presented")
	}
}

func TestFetchServerCertificateNoListener(t *testing.T) {
	// Reserve a port and close it so nothing is listening there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := listenerConfig(t, ln, config.SecurityTLS)
	_ = ln.Close()

	if _, err := FetchServerCertificate(context.Background(), cfg); err == nil {
		t.Error("FetchServerCertificate succeeded against a closed port")
	}
}
