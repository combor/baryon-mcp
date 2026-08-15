package credstore

import (
	"errors"

	zkeyring "github.com/zalando/go-keyring"
)

// systemKeyring adapts the platform credential store (Secret Service over
// D-Bus on Linux, Keychain on macOS, Credential Manager on Windows) to the
// Keyring interface.
type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) {
	return zkeyring.Get(service, user)
}

func (systemKeyring) Set(service, user, pass string) error {
	return zkeyring.Set(service, user, pass)
}

func (systemKeyring) Delete(service, user string) error {
	err := zkeyring.Delete(service, user)
	if errors.Is(err, zkeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
