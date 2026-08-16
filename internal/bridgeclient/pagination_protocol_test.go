package bridgeclient

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func subjects(page *MessagePage) []string {
	out := make([]string, 0, len(page.Emails))
	for _, email := range page.Emails {
		out = append(out, email.Subject)
	}
	return out
}

func TestProtocolCursorPagesWithoutOverlap(t *testing.T) {
	c := seedInbox(t)
	ctx := context.Background()

	first, err := c.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if got := subjects(first); got[0] != "Message 5" || got[1] != "Message 4" {
		t.Fatalf("first page = %q, want messages 5 and 4", got)
	}
	if first.NextBeforeUID != first.Emails[1].UID {
		t.Errorf("NextBeforeUID = %d, want the last uid of the page (%d)", first.NextBeforeUID, first.Emails[1].UID)
	}

	second, err := c.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{
		Limit: 2, BeforeUID: first.NextBeforeUID, UIDValidity: first.UIDValidity,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := subjects(second); len(got) != 2 || got[0] != "Message 3" || got[1] != "Message 2" {
		t.Fatalf("second page = %q, want messages 3 and 2", got)
	}
	if second.Total != 5 {
		t.Errorf("total = %d, want every match before pagination", second.Total)
	}

	last, err := c.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{
		Limit: 2, BeforeUID: second.NextBeforeUID, UIDValidity: second.UIDValidity,
	})
	if err != nil {
		t.Fatalf("last page: %v", err)
	}
	if got := subjects(last); len(got) != 1 || got[0] != "Message 1" {
		t.Fatalf("last page = %q, want message 1", got)
	}
	if last.NextBeforeUID != 0 {
		t.Errorf("NextBeforeUID = %d at the end of the results, want 0", last.NextBeforeUID)
	}
}

// Mail arriving between pages is what the cursor exists for: offset paging
// shifts every later page and repeats a message.
func TestProtocolCursorIsStableAcrossArrivals(t *testing.T) {
	var user *imapmemserver.User
	c := startMemServer(t, func(u *imapmemserver.User) {
		user = u
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 4; n++ {
			opts := &imap.AppendOptions{Time: time.Date(2026, 7, n, 10, 0, 0, 0, time.UTC)}
			if _, err := u.Append("INBOX", bytes.NewReader(testMessage(n, "alice@example.org")), opts); err != nil {
				t.Fatal(err)
			}
		}
	})
	ctx := context.Background()

	first, err := c.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	arrival := &imap.AppendOptions{Time: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)}
	if _, err := user.Append("INBOX", bytes.NewReader(testMessage(9, "carol@example.org")), arrival); err != nil {
		t.Fatal(err)
	}

	cursored, err := c.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{
		Limit: 2, BeforeUID: first.NextBeforeUID, UIDValidity: first.UIDValidity,
	})
	if err != nil {
		t.Fatalf("cursored page: %v", err)
	}
	if got := subjects(cursored); len(got) != 2 || got[0] != "Message 2" || got[1] != "Message 1" {
		t.Errorf("cursored second page = %q, want messages 2 and 1 despite the arrival", got)
	}

	offsetPage, err := c.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("offset page: %v", err)
	}
	if got := subjects(offsetPage); got[0] != "Message 3" {
		t.Errorf("offset second page = %q; the arrival is expected to shift it, which is why the cursor exists", got)
	}
}

func TestProtocolCursorRejectsStaleUIDValidity(t *testing.T) {
	c := seedInbox(t)
	_, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{
		Limit: 2, BeforeUID: 3, UIDValidity: 999999,
	})
	if err == nil || !strings.Contains(err.Error(), "UIDVALIDITY") {
		t.Fatalf("got %v, want a stale-cursor rejection", err)
	}
}

// narrowFetchSession drops one UID from every FETCH, standing in for a message
// expunged between the search and the fetch that follows it.
type narrowFetchSession struct {
	imapserver.Session
	drop imap.UID
}

func (s *narrowFetchSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if uidSet, ok := numSet.(imap.UIDSet); ok {
		if uids, ok := uidSet.Nums(); ok {
			kept := make([]imap.UID, 0, len(uids))
			for _, uid := range uids {
				if uid != s.drop {
					kept = append(kept, uid)
				}
			}
			numSet = imap.UIDSetNum(kept...)
		}
	}
	return s.Session.Fetch(w, numSet, options)
}

// The cursor comes from the searched window, not the last message fetched:
// otherwise an expunged boundary message takes its neighbours with it.
func TestProtocolCursorSurvivesExpungeBetweenSearchAndFetch(t *testing.T) {
	c := startMemServerWithOptions(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 4; n++ {
			opts := &imap.AppendOptions{Time: time.Date(2026, 7, n, 10, 0, 0, 0, time.UTC)}
			if _, err := u.Append("INBOX", bytes.NewReader(testMessage(n, "alice@example.org")), opts); err != nil {
				t.Fatal(err)
			}
		}
	}, nil, func(session imapserver.Session) imapserver.Session {
		// UID 3 is the boundary of a two-message page over four messages.
		return &narrowFetchSession{Session: session, drop: 3}
	})

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if got := subjects(page); len(got) != 1 || got[0] != "Message 4" {
		t.Fatalf("page = %q, want only the message that survived the fetch", got)
	}
	if page.NextBeforeUID != 3 {
		t.Errorf("NextBeforeUID = %d, want the expunged boundary 3 so message 2 is not skipped", page.NextBeforeUID)
	}
	if page.Total != 4 {
		t.Errorf("total = %d, want all four matches", page.Total)
	}
}

func TestProtocolSummaryCarriesEnvelopeIdentity(t *testing.T) {
	raw := []byte("From: \"Doe, Alice\" <alice@example.org>\r\n" +
		"Sender: secretary@example.org\r\n" +
		"Reply-To: Alice Replies <replies@example.org>\r\n" +
		"To: me@example.org\r\n" +
		"Subject: Quarterly\r\n" +
		"Date: Wed, 01 Jul 2026 10:00:00 +0000\r\n" +
		"Message-ID: <quarterly-1@test>\r\n\r\nbody\r\n")
	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	got := page.Emails[0]
	if got.MessageID != "quarterly-1@test" {
		t.Errorf("MessageID = %q, want it bare", got.MessageID)
	}
	if len(got.Sender) != 1 || got.Sender[0] != "secretary@example.org" {
		t.Errorf("Sender = %q", got.Sender)
	}
	if len(got.ReplyTo) != 1 || got.ReplyTo[0] != "Alice Replies <replies@example.org>" {
		t.Errorf("ReplyTo = %q", got.ReplyTo)
	}
	if len(got.From) != 1 || got.From[0] != `"Doe, Alice" <alice@example.org>` {
		t.Errorf("From = %q, want the comma-bearing name quoted", got.From)
	}
}
