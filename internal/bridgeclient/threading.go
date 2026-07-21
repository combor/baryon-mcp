package bridgeclient

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"
	messageMail "github.com/emersion/go-message/mail"
)

const protonBridgeInternalIDDomain = "protonmail.internalid"

const (
	// MaxThreadReferences bounds the References chain carried into a draft and
	// reported for a message.
	MaxThreadReferences = 100
	// MaxMsgIDBytes keeps one identifier well inside the 998-octet line limit:
	// a longer token cannot be folded and would be corrupted on serialization.
	MaxMsgIDBytes = 512
	// threadHeaderCap bounds a fetched identification header block: a real chain
	// is a few kilobytes, a hostile one is not self-limiting. It stays above both
	// identifier lists at their accepted maxima so a draft this package saves can
	// always be read back and replaced.
	threadHeaderCap = 2*MaxThreadReferences*(MaxMsgIDBytes+8) + 8*1024
)

// threadHeaderFields are the RFC 5322 identification fields, plus the Proton
// internal ID needed to recognise Bridge's self-reference.
var threadHeaderFields = []string{"Message-ID", "In-Reply-To", "References", "X-Pm-Internal-Id"}

// threadHeaders places a message in a conversation. Identifiers are bare, with
// no angle brackets.
type threadHeaders struct {
	messageID  string
	inReplyTo  []string
	references []string
}

func threadHeaderSection() *imap.FetchItemBodySection {
	return &imap.FetchItemBodySection{
		Specifier:    imap.PartSpecifierHeader,
		HeaderFields: threadHeaderFields,
		Peek:         true,
		Partial:      &imap.SectionPartial{Offset: 0, Size: threadHeaderCap},
	}
}

// parseThreadHeaders decodes a fetched header block. Fields that parse are
// always returned, so callers that prefer degraded metadata over failure can
// ignore the error.
func parseThreadHeaders(raw []byte) (threadHeaders, error) {
	// A truncated block ends mid-chain, so its last identifiers are not the recent
	// ancestry they look like. Reporting them would thread a reply into the wrong
	// place, and rebuilding a draft from them would corrupt its chain.
	if len(raw) >= threadHeaderCap {
		return threadHeaders{}, fmt.Errorf("identification headers reached the %d byte fetch limit", threadHeaderCap)
	}
	reader, err := messageMail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return threadHeaders{}, fmt.Errorf("parsing headers: %w", err)
	}
	defer reader.Close()

	var headers threadHeaders
	var firstErr error
	record := func(field string, err error) {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("parsing %s: %w", field, err)
		}
	}
	headers.messageID, err = reader.Header.MessageID()
	record("Message-ID", err)
	headers.inReplyTo, err = reader.Header.MsgIDList("In-Reply-To")
	record("In-Reply-To", err)
	headers.references, err = reader.Header.MsgIDList("References")
	record("References", err)

	// Bridge adds its own message ID to References on every rebuilt message for
	// client compatibility. That exact self-reference is not reply metadata.
	if internalID := strings.TrimSpace(reader.Header.Get("X-Pm-Internal-Id")); internalID != "" {
		selfReference := internalID + "@" + protonBridgeInternalIDDomain
		headers.references = slices.DeleteFunc(headers.references, func(reference string) bool {
			return reference == selfReference
		})
	}
	headers.inReplyTo = trimMsgIDs(headers.inReplyTo, MaxThreadReferences)
	// A reply appends the parent's Message-ID to this chain, so leave it room:
	// what a caller reads here must stay within what a save will accept back.
	headers.references = trimMsgIDs(headers.references, MaxThreadReferences-1)
	return headers, firstErr
}

// trimMsgIDs keeps the most recent identifiers, which are the ones that place a
// message in its conversation. RFC 5322 section 3.6.4 allows this trimming.
func trimMsgIDs(ids []string, limit int) []string {
	if len(ids) <= limit {
		return ids
	}
	return ids[len(ids)-limit:]
}

// fillFromEnvelope supplies identifiers the header fetch did not yield.
func (t *threadHeaders) fillFromEnvelope(envelope *imap.Envelope) {
	if envelope == nil {
		return
	}
	if t.messageID == "" {
		t.messageID = envelope.MessageID
	}
	if len(t.inReplyTo) == 0 {
		t.inReplyTo = envelope.InReplyTo
	}
}

// draftThreadHeaders normalizes the conversation headers supplied by the caller.
func draftThreadHeaders(draft Draft) (threadHeaders, error) {
	var headers threadHeaders
	var err error
	if headers.inReplyTo, err = normalizeMsgIDs("in_reply_to", draft.InReplyTo); err != nil {
		return threadHeaders{}, err
	}
	if headers.references, err = normalizeMsgIDs("references", draft.References); err != nil {
		return threadHeaders{}, err
	}
	return headers, nil
}

// normalizeMsgIDs accepts identifiers with or without angle brackets and
// returns them bare. Angle brackets, whitespace, and control characters are
// rejected so an identifier cannot forge or split a header.
func normalizeMsgIDs(field string, values []string) ([]string, error) {
	// Nil and empty are returned unchanged: on a replacement they mean "leave the
	// header alone" and "remove it", and collapsing them would lose that.
	if len(values) == 0 {
		return values, nil
	}
	if len(values) > MaxThreadReferences {
		return nil, fmt.Errorf("%s may list at most %d message ids", field, MaxThreadReferences)
	}
	normalized := make([]string, 0, len(values))
	for i, value := range values {
		id := strings.TrimSpace(value)
		id = strings.TrimPrefix(id, "<")
		id = strings.TrimSuffix(id, ">")
		if id == "" {
			return nil, fmt.Errorf("%s message id at index %d is empty", field, i)
		}
		if strings.ContainsAny(id, "<> ") || strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, fmt.Errorf("%s message id at index %d is malformed: %q", field, i, value)
		}
		if len(id) > MaxMsgIDBytes {
			return nil, fmt.Errorf("%s message id at index %d is %d bytes, above the %d byte limit", field, i, len(id), MaxMsgIDBytes)
		}
		if !readableMsgID(id) {
			return nil, fmt.Errorf("%s message id at index %d is not a valid RFC 5322 message id (expected id-left@id-right): %q", field, i, value)
		}
		normalized = append(normalized, id)
	}
	return normalized, nil
}

// readableMsgID reports whether writing id back out survives the reader used on
// every fetch. Saving an identifier this parser rejects would leave a draft that
// can be read only in degraded form and can never be replaced.
func readableMsgID(id string) bool {
	var probe messageMail.Header
	probe.SetMsgIDList("References", []string{id})
	parsed, err := probe.MsgIDList("References")
	return err == nil && len(parsed) == 1 && parsed[0] == id
}
