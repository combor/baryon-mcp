package bridgeclient

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// certProbePaths returns well-known locations where an exported Bridge TLS
// certificate might live. Bridge v3 keeps its certificate inside an encrypted
// vault, so these usually only exist after the user runs Bridge's
// "Export TLS certificate" — probing is a convenience, not the expected path.
func certProbePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "protonmail", "bridge-v3", "cert.pem"),
			filepath.Join(home, "Library", "Application Support", "protonmail", "bridge", "cert.pem"),
		}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return nil
		}
		return []string{
			filepath.Join(appData, "protonmail", "bridge-v3", "cert.pem"),
			filepath.Join(appData, "protonmail", "bridge", "cert.pem"),
		}
	default: // linux and friends
		return []string{
			filepath.Join(home, ".config", "protonmail", "bridge-v3", "cert.pem"),
			filepath.Join(home, ".config", "protonmail", "bridge", "cert.pem"),
		}
	}
}

// buildTLSConfig produces the tls.Config used for connections to Bridge and a
// human-readable description of how the peer will be authenticated.
//
// Resolution order: explicit certPath (pinned) → probe well-known exported
// locations (pinned) → error, unless allowInsecure explicitly opts out of
// verification. There is deliberately no silent insecure fallback: Bridge's
// port is unprivileged, so an unverified connection would hand the Bridge
// credentials to any local process that binds the port while Bridge is down.
func buildTLSConfig(certPath string, allowInsecure bool, probePaths []string) (*tls.Config, string, error) {
	if certPath != "" {
		cfg, err := pinnedTLSConfig(certPath)
		if err != nil {
			return nil, "", fmt.Errorf("loading PROTON_BRIDGE_TLS_CERT: %w", err)
		}
		return cfg, fmt.Sprintf("pinned to certificate %s", certPath), nil
	}

	for _, p := range probePaths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cfg, err := pinnedTLSConfig(p)
		if err != nil {
			return nil, "", fmt.Errorf("loading discovered Bridge certificate %s: %w", p, err)
		}
		return cfg, fmt.Sprintf("pinned to discovered certificate %s", p), nil
	}

	if allowInsecure {
		return &tls.Config{InsecureSkipVerify: true},
			"UNVERIFIED (PROTON_BRIDGE_ALLOW_INSECURE=true): a local process squatting Bridge's port could capture the credentials",
			nil
	}

	return nil, "", fmt.Errorf(
		"no Bridge TLS certificate available: export it via Bridge's Settings → Export TLS certificate and set PROTON_BRIDGE_TLS_CERT to the cert.pem path, or set PROTON_BRIDGE_ALLOW_INSECURE=true to skip verification (accepts the risk that another local process impersonates Bridge)")
}

// pinnedTLSConfig verifies the peer by comparing its certificate against the
// PEM certificate(s) in path. Bridge's self-signed certificate carries no
// hostname SAN for arbitrary loopback literals, so standard hostname
// verification is disabled and identity is checked byte-for-byte instead.
func pinnedTLSConfig(path string) (*tls.Config, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pinned, err := parsePEMCertificates(pemData)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		InsecureSkipVerify: true, // identity checked in VerifyPeerCertificate instead
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("bridge presented no certificate")
			}
			for _, want := range pinned {
				if bytes.Equal(rawCerts[0], want.Raw) {
					return nil
				}
			}
			return fmt.Errorf("bridge's certificate does not match the pinned certificate %s — if Bridge regenerated its certificate, re-export it", path)
		},
	}, nil
}

func parsePEMCertificates(pemData []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for len(pemData) > 0 {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no CERTIFICATE blocks found in PEM data")
	}
	return certs, nil
}
