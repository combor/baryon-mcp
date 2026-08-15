//go:build !linux

package setup

// verifyBridgeListener is Linux-only: identifying a socket's owner needs
// procfs. On other platforms Bridge's GUI certificate export exists, so setup
// falls back to asking for an exported cert.pem instead of capturing one.
func verifyBridgeListener(procRoot string, port int) (*bridgeListener, error) {
	return nil, errCaptureUnsupported
}
