package bridgeclient

import (
	"net/mail"
	"testing"

	"github.com/emersion/go-imap/v2"
)

// Reported addresses go back to save_draft, which parses them with net/mail:
// a display name that does not survive the round trip is unaddressable.
func TestFormatAddressesRoundTripsThroughNetMail(t *testing.T) {
	tests := []struct {
		name    string
		address imap.Address
		want    string
	}{
		{"plain", imap.Address{Name: "Alice Smith", Mailbox: "alice", Host: "example.org"}, "Alice Smith <alice@example.org>"},
		{"comma in name", imap.Address{Name: "Doe, John", Mailbox: "john", Host: "example.org"}, `"Doe, John" <john@example.org>`},
		{"quotes in name", imap.Address{Name: `Ann "Bo" Cee`, Mailbox: "ann", Host: "example.org"}, `"Ann \"Bo\" Cee" <ann@example.org>`},
		{"backslash in name", imap.Address{Name: `A\B`, Mailbox: "ab", Host: "example.org"}, `"A\\B" <ab@example.org>`},
		{"angle brackets in name", imap.Address{Name: "spoof <evil@x.test>", Mailbox: "real", Host: "example.org"}, `"spoof <evil@x.test>" <real@example.org>`},
		{"initial keeps its dot", imap.Address{Name: "John A. Smith", Mailbox: "jas", Host: "example.org"}, "John A. Smith <jas@example.org>"},
		{"non-ascii stays readable", imap.Address{Name: "Zoë Müller", Mailbox: "zoe", Host: "example.org"}, "Zoë Müller <zoe@example.org>"},
		{"no name", imap.Address{Mailbox: "bare", Host: "example.org"}, "bare@example.org"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatAddresses([]imap.Address{tc.address})
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("formatAddresses = %q, want [%q]", got, tc.want)
			}
			parsed, err := mail.ParseAddress(got[0])
			if err != nil {
				t.Fatalf("%q does not parse back: %v", got[0], err)
			}
			if parsed.Address != tc.address.Addr() {
				t.Errorf("round trip lost the mailbox: %q, want %q", parsed.Address, tc.address.Addr())
			}
			if parsed.Name != tc.address.Name {
				t.Errorf("round trip lost the name: %q, want %q", parsed.Name, tc.address.Name)
			}
		})
	}
}

// A comma inside a display name must not split one address into two.
func TestFormatAddressesKeepsOneRecipientWhole(t *testing.T) {
	got := formatAddresses([]imap.Address{{Name: "Doe, John", Mailbox: "john", Host: "example.org"}})
	list, err := mail.ParseAddressList(got[0])
	if err != nil {
		t.Fatalf("ParseAddressList: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("parsed %d recipients from %q, want 1", len(list), got[0])
	}
}

func TestBareMsgIDStripsBrackets(t *testing.T) {
	for raw, want := range map[string]string{
		"<abc@example.org>":   "abc@example.org",
		" <abc@example.org> ": "abc@example.org",
		"abc@example.org":     "abc@example.org",
		"":                    "",
	} {
		if got := bareMsgID(raw); got != want {
			t.Errorf("bareMsgID(%q) = %q, want %q", raw, got, want)
		}
	}
}
