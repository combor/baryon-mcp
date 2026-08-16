package bridgeclient

import (
	"strings"

	"github.com/emersion/go-imap/v2"
)

// formatAddresses renders envelope addresses as "Name <mailbox@host>".
func formatAddresses(addrs []imap.Address) []string {
	var out []string
	for _, a := range addrs {
		if a.IsGroupStart() || a.IsGroupEnd() {
			continue
		}
		if formatted := FormatAddress(a.Name, a.Addr()); formatted != "" {
			out = append(out, formatted)
		}
	}
	return out
}

// FormatAddress renders one address the way every tool reports them. A bare
// mailbox stays bare rather than gaining empty brackets.
func FormatAddress(name, address string) string {
	switch {
	case name != "" && address != "":
		return quoteDisplayName(name) + " <" + address + ">"
	case address != "":
		return address
	default:
		return quoteDisplayName(name)
	}
}

// quoteDisplayName quotes a display name that would otherwise make the address
// unparseable: these go straight back to save_draft, where "Doe, John" would
// read as two recipients. Anything else is left as it is, non-ASCII included,
// so a listing shows the name rather than its RFC 2047 encoding.
func quoteDisplayName(name string) string {
	if !strings.ContainsAny(name, "\"\\()<>[]:;@,") {
		return name
	}
	var b strings.Builder
	b.Grow(len(name) + 2)
	b.WriteByte('"')
	for _, r := range name {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
