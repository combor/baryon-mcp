package bridgeclient

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// SearchCriteria narrows a message listing. The zero value matches everything.
type SearchCriteria struct {
	Query      string // free-text search over headers and body (IMAP TEXT)
	From       string
	To         string
	Subject    string
	Since      time.Time // internal date, inclusive; zero means unset
	Before     time.Time // internal date, exclusive; zero means unset
	UnreadOnly bool
}

// EmailSummary is one message's envelope-level view. MessageID is bare, with
// no angle brackets.
type EmailSummary struct {
	UID       uint32
	Subject   string
	From      []string
	Sender    []string
	ReplyTo   []string
	To        []string
	Cc        []string
	Bcc       []string
	Date      time.Time
	MessageID string
	Seen      bool
	Flagged   bool
	Answered  bool
}

// PageRequest selects one page of a folder listing. Offset shifts when mail
// arrives between calls; BeforeUID is the stable cursor, returning only
// messages below a UID already seen, and UIDValidity guards it.
type PageRequest struct {
	Limit       int
	Offset      int
	BeforeUID   uint32
	UIDValidity uint32
}

// MessagePage is one page of a folder listing, newest first. NextBeforeUID is
// the cursor for the following page, and is zero at the end of the results.
type MessagePage struct {
	UIDValidity   uint32
	Total         int
	NextBeforeUID uint32
	Emails        []EmailSummary
}

// ListMessages searches folder with criteria and returns one page of results,
// newest first.
func (c *Client) ListMessages(ctx context.Context, folder string, criteria SearchCriteria, req PageRequest) (*MessagePage, error) {
	if err := c.policy.check(folder); err != nil {
		return nil, err
	}
	var page *MessagePage
	err := c.withSession(ctx, func(cli *imapclient.Client) error {
		sel, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return fmt.Errorf("selecting folder %q: %w", folder, err)
		}
		if req.UIDValidity != 0 && sel.UIDValidity != req.UIDValidity {
			return fmt.Errorf("folder %q UIDVALIDITY changed (now %d, expected %d): the page cursor is stale, start again without before_uid", folder, sel.UIDValidity, req.UIDValidity)
		}

		data, err := cli.UIDSearch(criteria.toIMAP(), nil).Wait()
		if err != nil {
			return fmt.Errorf("searching folder %q: %w", folder, err)
		}
		uids := data.AllUIDs()
		slices.SortFunc(uids, func(a, b imap.UID) int { return cmp.Compare(b, a) })

		page = &MessagePage{UIDValidity: sel.UIDValidity, Total: len(uids)}
		if req.BeforeUID != 0 {
			cutoff := imap.UID(req.BeforeUID)
			uids = slices.DeleteFunc(uids, func(uid imap.UID) bool { return uid >= cutoff })
		}
		if req.Offset >= len(uids) {
			return nil
		}
		end := min(req.Offset+req.Limit, len(uids))
		// The cursor is the searched window's boundary, not the last message the
		// fetch below returns: an expunge between the two would otherwise move it
		// past that message's neighbours and skip them.
		if end < len(uids) {
			page.NextBeforeUID = uint32(uids[end-1])
		}
		uids = uids[req.Offset:end]

		msgs, err := cli.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
			Envelope: true,
			Flags:    true,
			UID:      true,
		}).Collect()
		if err != nil {
			return fmt.Errorf("fetching message summaries: %w", err)
		}

		// FETCH responses arrive in mailbox order; re-emit in the requested newest-first order.
		byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(msgs))
		for _, m := range msgs {
			byUID[m.UID] = m
		}
		for _, uid := range uids {
			m, ok := byUID[uid]
			if !ok {
				continue // expunged between search and fetch
			}
			page.Emails = append(page.Emails, summarize(m))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

func (sc SearchCriteria) toIMAP() *imap.SearchCriteria {
	crit := &imap.SearchCriteria{Since: sc.Since, Before: sc.Before}
	if sc.Query != "" {
		crit.Text = []string{sc.Query}
	}
	header := func(key, value string) {
		if value != "" {
			crit.Header = append(crit.Header, imap.SearchCriteriaHeaderField{Key: key, Value: value})
		}
	}
	header("From", sc.From)
	header("To", sc.To)
	header("Subject", sc.Subject)
	if sc.UnreadOnly {
		crit.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	return crit
}

func summarize(m *imapclient.FetchMessageBuffer) EmailSummary {
	s := EmailSummary{UID: uint32(m.UID)}
	for _, f := range m.Flags {
		switch f {
		case imap.FlagSeen:
			s.Seen = true
		case imap.FlagFlagged:
			s.Flagged = true
		case imap.FlagAnswered:
			s.Answered = true
		}
	}
	if env := m.Envelope; env != nil {
		s.Subject = env.Subject
		s.From = formatAddresses(env.From)
		s.Sender = formatAddresses(env.Sender)
		s.ReplyTo = formatAddresses(env.ReplyTo)
		s.To = formatAddresses(env.To)
		s.Cc = formatAddresses(env.Cc)
		s.Bcc = formatAddresses(env.Bcc)
		s.Date = env.Date
		s.MessageID = bareMsgID(env.MessageID)
	}
	return s
}

// bareMsgID strips the angle brackets an ENVELOPE carries, so envelope and
// header identifiers read alike.
func bareMsgID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	return strings.TrimSuffix(id, ">")
}
