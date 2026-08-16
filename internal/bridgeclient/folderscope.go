package bridgeclient

import (
	"fmt"
	"strings"
)

// folderPolicy is the optional BARYON_ALLOWED_FOLDERS allowlist. The zero
// value permits every mailbox.
type folderPolicy struct {
	allowed []string
}

// permits reports whether name may be opened. Names match exactly, since
// Bridge mailbox names are case-sensitive — except INBOX, which IMAP itself
// defines case-insensitively.
func (p folderPolicy) permits(name string) bool {
	if len(p.allowed) == 0 {
		return true
	}
	for _, candidate := range p.allowed {
		if strings.EqualFold(candidate, "INBOX") {
			if strings.EqualFold(name, "INBOX") {
				return true
			}
			continue
		}
		if candidate == name {
			return true
		}
	}
	return false
}

// check refuses a mailbox outside the scope, naming the allowed ones: the
// caller cannot see the server's configuration.
func (p folderPolicy) check(name string) error {
	if p.permits(name) {
		return nil
	}
	return fmt.Errorf("folder %q is not allowed by BARYON_ALLOWED_FOLDERS; this server may read only: %s", name, strings.Join(p.allowed, ", "))
}

// filter drops mailboxes out of scope, so a listing never advertises a folder
// every other tool refuses.
func (p folderPolicy) filter(folders []Folder) []Folder {
	if len(p.allowed) == 0 {
		return folders
	}
	kept := make([]Folder, 0, len(folders))
	for _, folder := range folders {
		if p.permits(folder.Name) {
			kept = append(kept, folder)
		}
	}
	return kept
}
