package bridgeclient

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// seedOneMessage puts a single raw message in INBOX.
func seedOneMessage(t *testing.T, raw string) *Client {
	t.Helper()
	return startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader([]byte(raw)), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestProtocolListMessagesIncludesBodies(t *testing.T) {
	c := seedInbox(t)

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 5, IncludeBodies: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(page.Emails) != 5 {
		t.Fatalf("returned %d messages, want 5", len(page.Emails))
	}
	// seedInbox numbers its bodies to match its subjects, newest last.
	for i, e := range page.Emails {
		want := fmt.Sprintf("body %d", 5-i)
		if got := strings.TrimSpace(e.Body); got != want {
			t.Errorf("message %d body = %q, want %q", i, got, want)
		}
		if e.BodyFromHTML {
			t.Errorf("message %d reported HTML for a plain text part", i)
		}
		if e.BodyTruncated {
			t.Errorf("message %d reported a short body as truncated", i)
		}
	}
}

func TestProtocolListMessagesOmitsBodiesByDefault(t *testing.T) {
	c := seedInbox(t)

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 5})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, e := range page.Emails {
		if e.Body != "" {
			t.Errorf("uid %d returned a body without IncludeBodies: %q", e.UID, e.Body)
		}
	}
}

// Previews must not cost the unread flag.
func TestProtocolListMessagesPreviewsDoNotMarkRead(t *testing.T) {
	c := seedInbox(t)
	ctx := context.Background()
	unread := SearchCriteria{UnreadOnly: true}

	before, err := c.ListMessages(ctx, "INBOX", unread, PageRequest{Limit: 10, IncludeBodies: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(before.Emails) != 4 {
		t.Fatalf("seeded inbox returned %d unread messages, want 4", len(before.Emails))
	}

	after, err := c.ListMessages(ctx, "INBOX", unread, PageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(after.Emails) != len(before.Emails) {
		t.Fatalf("%d messages still unread after previewing %d of them", len(after.Emails), len(before.Emails))
	}
	for i, e := range after.Emails {
		if e.UID != before.Emails[i].UID || e.Seen {
			t.Errorf("uid %d changed state after a preview: seen=%v", e.UID, e.Seen)
		}
	}
}

func TestProtocolListMessagesPreviewsFallBackToHTML(t *testing.T) {
	c := seedOneMessage(t, "From: a@x\r\nTo: b@x\r\nSubject: html only\r\n"+
		"Content-Type: text/html; charset=utf-8\r\n\r\n"+
		"<html><head><style>.a{color:red}</style></head><body><p>html body</p></body></html>\r\n")

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 1, IncludeBodies: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	e := page.Emails[0]
	if !e.BodyFromHTML {
		t.Error("a message with no plain text part should report body_from_html")
	}
	if got := strings.TrimSpace(e.Body); got != "html body" {
		t.Errorf("body = %q, want the markup reduced to its text", got)
	}
}

// The cap applies to what a reader would see, so a message that is mostly
// markup still returns its whole message rather than a shortened stylesheet.
func TestProtocolListMessagesPreviewsSpendTheBudgetOnText(t *testing.T) {
	buried := "Your invoice is ready."
	c := seedOneMessage(t, "From: a@x\r\nTo: b@x\r\nSubject: markup heavy\r\n"+
		"Content-Type: text/html; charset=utf-8\r\n\r\n"+
		"<html><head><style>"+strings.Repeat(".cls{padding:0}", 500)+
		"</style></head><body><div>"+buried+"</div></body></html>\r\n")

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 1, IncludeBodies: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	e := page.Emails[0]
	if got := strings.TrimSpace(e.Body); got != buried {
		t.Errorf("body = %q, want %q", got, buried)
	}
	if e.BodyTruncated {
		t.Error("a short message was reported truncated because of its markup")
	}
}

func TestProtocolListMessagesPreviewsTruncate(t *testing.T) {
	long := strings.Repeat("x", previewBodyCharCap+500)
	c := seedOneMessage(t, fmt.Sprintf("From: a@x\r\nTo: b@x\r\nSubject: long\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", long))

	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 1, IncludeBodies: true})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	e := page.Emails[0]
	if !e.BodyTruncated {
		t.Error("an oversized body should report body_truncated")
	}
	if got := len([]rune(e.Body)); got != previewBodyCharCap {
		t.Errorf("body length = %d, want the char cap %d", got, previewBodyCharCap)
	}
}
