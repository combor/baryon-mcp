package bridgeclient

import (
	"cmp"
	"context"
	"fmt"
	"slices"
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

// EmailSummary is one message's envelope-level view.
type EmailSummary struct {
	UID      uint32
	Subject  string
	From     []string
	To       []string
	Cc       []string
	Bcc      []string
	Date     time.Time
	Seen     bool
	Flagged  bool
	Answered bool
}

// MessagePage is one page of a folder listing, newest first.
type MessagePage struct {
	UIDValidity uint32
	Total       int
	Emails      []EmailSummary
}

// ListMessages searches folder with criteria and returns the page selected by
// limit/offset, newest first.
func (c *Client) ListMessages(ctx context.Context, folder string, criteria SearchCriteria, limit, offset int) (*MessagePage, error) {
	var page *MessagePage
	err := c.withSession(ctx, func(cli *imapclient.Client) error {
		sel, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return fmt.Errorf("selecting folder %q: %w", folder, err)
		}

		data, err := cli.UIDSearch(criteria.toIMAP(), nil).Wait()
		if err != nil {
			return fmt.Errorf("searching folder %q: %w", folder, err)
		}
		uids := data.AllUIDs()
		slices.SortFunc(uids, func(a, b imap.UID) int { return cmp.Compare(b, a) })

		page = &MessagePage{UIDValidity: sel.UIDValidity, Total: len(uids)}
		if offset >= len(uids) {
			return nil
		}
		uids = uids[offset:min(offset+limit, len(uids))]

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
		s.To = formatAddresses(env.To)
		s.Cc = formatAddresses(env.Cc)
		s.Bcc = formatAddresses(env.Bcc)
		s.Date = env.Date
	}
	return s
}
