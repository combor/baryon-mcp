package bridgeclient

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
)

// formatAddresses renders envelope addresses as "Name <mailbox@host>".
func formatAddresses(addrs []imap.Address) []string {
	var out []string
	for _, a := range addrs {
		if a.IsGroupStart() || a.IsGroupEnd() {
			continue
		}
		addr := a.Addr()
		switch {
		case a.Name != "" && addr != "":
			out = append(out, fmt.Sprintf("%s <%s>", a.Name, addr))
		case addr != "":
			out = append(out, addr)
		case a.Name != "":
			out = append(out, a.Name)
		}
	}
	return out
}
