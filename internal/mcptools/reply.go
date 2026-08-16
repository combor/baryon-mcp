package mcptools

// Reply derivation: the recipients, subject and identification headers of an
// answer, taken from the message being answered.

import (
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
)

// replySource is the part of a parent message a reply is derived from.
type replySource struct {
	Subject    string
	From       []string
	Sender     []string
	ReplyTo    []string
	To         []string
	Cc         []string
	Bcc        []string
	MessageID  string
	InReplyTo  []string
	References []string
}

// replyOptions are the caller's choices about the reply itself.
type replyOptions struct {
	From     string
	ReplyAll bool
	Bcc      []string
}

// buildReply derives a reply's header fields. Bodies and attachments are the
// caller's: nothing of the original is quoted or copied.
func buildReply(source replySource, opts replyOptions, identities []config.Identity) (bridgeclient.Draft, error) {
	if strings.TrimSpace(source.MessageID) == "" {
		return bridgeclient.Draft{}, fmt.Errorf("the message carries no Message-ID, so a reply cannot be threaded to it; compose the draft with save_draft instead")
	}

	self := selfAddresses(identities)
	from, err := selectFrom(source, opts.From, identities, self)
	if err != nil {
		return bridgeclient.Draft{}, err
	}

	to := replyRecipients(source, self)
	if len(to) == 0 {
		return bridgeclient.Draft{}, fmt.Errorf("the message has no address to reply to: its Reply-To, From and Sender are missing, unreadable, or only this account's own; compose the draft with save_draft instead")
	}

	var cc []string
	if opts.ReplyAll {
		cc = replyAllCopies(source, self, to)
	}

	return bridgeclient.Draft{
		From:       from,
		To:         to,
		Cc:         cc,
		Bcc:        opts.Bcc,
		Subject:    replySubject(source.Subject),
		InReplyTo:  []string{source.MessageID},
		References: replyReferences(source),
	}, nil
}

// selfAddresses is the account's own mailboxes, lowercased for comparison.
func selfAddresses(identities []config.Identity) map[string]bool {
	self := make(map[string]bool, len(identities))
	for _, identity := range identities {
		self[strings.ToLower(identity.Address)] = true
	}
	return self
}

// selectFrom picks the identity that sends the reply: an explicit choice, else
// the identity the message was addressed to, else the identity it came from,
// else the default. An unconfigured explicit choice is refused rather than sent
// under an address the user may not control.
func selectFrom(source replySource, requested string, identities []config.Identity, self map[string]bool) (string, error) {
	if len(identities) == 0 {
		return "", fmt.Errorf("no sender identities are configured; set BARYON_SENDER_IDENTITIES, or compose the draft with save_draft")
	}
	if strings.TrimSpace(requested) != "" {
		parsed, err := mail.ParseAddress(requested)
		if err != nil {
			return "", fmt.Errorf("from %q is not a valid address: %w", requested, err)
		}
		match := findIdentity(identities, parsed.Address)
		if match == nil {
			return "", fmt.Errorf("from %q is not one of this server's sender identities; call list_sender_identities", requested)
		}
		return identityAddress(*match), nil
	}
	for _, field := range [][]string{source.To, source.Cc, source.Bcc, source.From} {
		for _, address := range parseAddresses(field) {
			if !self[strings.ToLower(address.Address)] {
				continue
			}
			if match := findIdentity(identities, address.Address); match != nil {
				return identityAddress(*match), nil
			}
		}
	}
	return identityAddress(identities[0]), nil
}

// identityAddress renders a configured identity for a draft's From header.
func identityAddress(identity config.Identity) string {
	return bridgeclient.FormatAddress(identity.Name, identity.Address)
}

// findIdentity returns the configured identity for address, so the reply
// carries the operator's display name rather than one from the message.
func findIdentity(identities []config.Identity, address string) *config.Identity {
	for i, identity := range identities {
		if strings.EqualFold(identity.Address, address) {
			return &identities[i]
		}
	}
	return nil
}

// replyRecipients derives the To list from what the message names as its
// origin: Reply-To, then From, then Sender. An origin of nothing but this
// account's own addresses yields none — any sender may put this account in
// From, so its other recipients are not a substitute.
func replyRecipients(source replySource, self map[string]bool) []string {
	for _, field := range [][]string{source.ReplyTo, source.From, source.Sender} {
		if recipients := dedupeExcluding(parseAddresses(field), self, nil); len(recipients) > 0 {
			return recipients
		}
	}
	return nil
}

// replyAllCopies adds everyone else on the message, never its Bcc. Bridge
// reports Bcc back on messages this account sent, so copying it would expose a
// deliberately hidden recipient to the whole conversation.
func replyAllCopies(source replySource, self map[string]bool, to []string) []string {
	already := make(map[string]bool, len(to))
	for _, address := range parseAddresses(to) {
		already[strings.ToLower(address.Address)] = true
	}
	copies := append(parseAddresses(source.To), parseAddresses(source.Cc)...)
	return dedupeExcluding(copies, self, already)
}

// dedupeExcluding keeps one entry per mailbox, dropping both exclusion sets.
// The same recipient under two display names is one recipient.
func dedupeExcluding(addresses []*mail.Address, self, already map[string]bool) []string {
	seen := make(map[string]bool, len(addresses))
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		key := strings.ToLower(address.Address)
		if self[key] || already[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, bridgeclient.FormatAddress(address.Name, address.Address))
	}
	return out
}

// parseAddresses parses reported address strings, skipping unparseable ones:
// they cannot be addressed anyway, and one bad header must not make a
// conversation unanswerable.
func parseAddresses(values []string) []*mail.Address {
	out := make([]*mail.Address, 0, len(values))
	for _, value := range values {
		address, err := mail.ParseAddress(value)
		if err != nil {
			continue
		}
		out = append(out, address)
	}
	return out
}

// replySubject prefixes "Re: " unless one is already there, in any case: a
// thread carries one prefix, not one per exchange.
func replySubject(subject string) string {
	trimmed := strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(trimmed), "re:") {
		return trimmed
	}
	return strings.TrimSpace("Re: " + trimmed)
}

// replyReferences chains the parent's References — or its In-Reply-To when it
// has none — then the parent itself, trimmed to the limit the draft layer
// accepts by dropping the oldest, as RFC 5322 section 3.6.4 allows.
func replyReferences(source replySource) []string {
	chain := source.References
	if len(chain) == 0 {
		chain = source.InReplyTo
	}
	references := slices.Clone(chain)
	if !slices.Contains(references, source.MessageID) {
		references = append(references, source.MessageID)
	}
	if len(references) > bridgeclient.MaxThreadReferences {
		references = references[len(references)-bridgeclient.MaxThreadReferences:]
	}
	return references
}
