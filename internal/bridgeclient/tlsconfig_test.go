package bridgeclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfSignedCert generates a throwaway certificate and returns its PEM
// encoding plus raw DER bytes.
func selfSignedCert(t *testing.T, cn string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return pemBytes, der
}

func writeCert(t *testing.T, pemBytes []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPinnedCertAcceptsAndRejects(t *testing.T) {
	bridgePEM, bridgeDER := selfSignedCert(t, "bridge")
	_, impostorDER := selfSignedCert(t, "impostor")
	path := writeCert(t, bridgePEM)

	cfg, source, err := buildTLSConfig(path, false, nil)
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if !strings.Contains(source, "pinned") {
		t.Errorf("source = %q, want pinned", source)
	}
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("expected VerifyPeerCertificate to be set")
	}

	if err := cfg.VerifyPeerCertificate([][]byte{bridgeDER}, nil); err != nil {
		t.Errorf("pinned cert rejected: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{impostorDER}, nil); err == nil {
		t.Error("impostor cert accepted")
	}
	if err := cfg.VerifyPeerCertificate(nil, nil); err == nil {
		t.Error("empty cert chain accepted")
	}
}

func TestProbeDiscoversCert(t *testing.T) {
	bridgePEM, bridgeDER := selfSignedCert(t, "bridge")
	probed := writeCert(t, bridgePEM)

	cfg, source, err := buildTLSConfig("", false, []string{
		filepath.Join(t.TempDir(), "does-not-exist.pem"),
		probed,
	})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if !strings.Contains(source, "discovered") {
		t.Errorf("source = %q, want discovered", source)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{bridgeDER}, nil); err != nil {
		t.Errorf("probed cert rejected: %v", err)
	}
}

func TestNoCertFailsClosed(t *testing.T) {
	_, _, err := buildTLSConfig("", false, nil)
	if err == nil {
		t.Fatal("expected error with no cert and no insecure opt-in")
	}
	for _, want := range []string{"Export TLS certificate", "PROTON_BRIDGE_TLS_CERT", "PROTON_BRIDGE_ALLOW_INSECURE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing guidance %q", err, want)
		}
	}
}

func TestExplicitInsecureOptIn(t *testing.T) {
	cfg, source, err := buildTLSConfig("", true, nil)
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if !cfg.InsecureSkipVerify || cfg.VerifyPeerCertificate != nil {
		t.Error("expected plain InsecureSkipVerify config")
	}
	if !strings.Contains(source, "UNVERIFIED") {
		t.Errorf("source = %q, want loud UNVERIFIED marker", source)
	}
}

func TestExplicitCertBeatsProbe(t *testing.T) {
	explicitPEM, explicitDER := selfSignedCert(t, "explicit")
	probePEM, probeDER := selfSignedCert(t, "probe")
	explicitPath := writeCert(t, explicitPEM)
	probePath := writeCert(t, probePEM)

	cfg, _, err := buildTLSConfig(explicitPath, false, []string{probePath})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{explicitDER}, nil); err != nil {
		t.Errorf("explicit cert rejected: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{probeDER}, nil); err == nil {
		t.Error("probe cert accepted despite explicit pin")
	}
}

func TestGarbagePEMRejected(t *testing.T) {
	path := writeCert(t, []byte("not a pem"))
	if _, _, err := buildTLSConfig(path, false, nil); err == nil {
		t.Error("expected error for non-PEM cert file")
	}
}
