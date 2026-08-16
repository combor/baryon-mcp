package bridgeclient

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// threadMessage builds a message placed in a conversation by its own id and the
// chain it references.
func threadMessage(day int, subject, msgID string, references []string) []byte {
	var refs string
	if len(references) > 0 {
		refs = "References:"
		for _, r := range references {
			refs += fmt.Sprintf(" <%s>", r)
		}
		refs += "\r\n"
	}
	return []byte(fmt.Sprintf(
		"From: alice@example.org\r\nTo: me@example.org\r\nSubject: %s\r\nDate: Wed, %02d Jul 2026 10:00:00 +0000\r\nMessage-ID: <%s>\r\n%s\r\nbody of %s\r\n",
		subject, day, msgID, refs, msgID))
}

func appendMsg(t *testing.T, u *imapmemserver.User, folder string, day int, raw []byte) {
	t.Helper()
	opts := &imap.AppendOptions{Time: time.Date(2026, 7, day, 10, 0, 0, 0, time.UTC)}
	if _, err := u.Append(folder, bytes.NewReader(raw), opts); err != nil {
		t.Fatal(err)
	}
}

// seedThread builds a three-message conversation in INBOX plus an unrelated
// message, and a reply filed in Sent.
func seedThread(t *testing.T) *Client {
	t.Helper()
	return startMemServer(t, func(u *imapmemserver.User) {
		for _, folder := range []string{"INBOX", "Sent"} {
			if err := u.Create(folder, nil); err != nil {
				t.Fatal(err)
			}
		}
		appendMsg(t, u, "INBOX", 1, threadMessage(1, "Plans", "root@test", nil))
		appendMsg(t, u, "INBOX", 2, threadMessage(2, "Re: Plans", "reply1@test", []string{"root@test"}))
		appendMsg(t, u, "INBOX", 3, threadMessage(3, "Re: Plans", "reply2@test", []string{"root@test", "reply1@test"}))
		appendMsg(t, u, "INBOX", 4, threadMessage(4, "Unrelated", "other@test", nil))
		appendMsg(t, u, "Sent", 5, threadMessage(5, "Re: Plans", "mine@test", []string{"root@test", "reply2@test"}))
	})
}

// seedUID returns the UID and UIDVALIDITY of the message with the given subject.
func seedUID(t *testing.T, c *Client, folder, subject string) (uint32, uint32) {
	t.Helper()
	page, err := c.ListMessages(context.Background(), folder, SearchCriteria{Subject: subject}, PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Emails) == 0 {
		t.Fatalf("no message with subject %q in %s", subject, folder)
	}
	return page.Emails[0].UID, page.UIDValidity
}

func TestProtocolGetThreadAssemblesConversation(t *testing.T) {
	c := seedThread(t)
	uid, validity := seedUID(t, c, "INBOX", "Plans")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if thread.Folder != "INBOX" || thread.UIDValidity != validity {
		t.Errorf("folder/validity = %q/%d", thread.Folder, thread.UIDValidity)
	}
	if thread.Total != 3 || len(thread.Messages) != 3 {
		t.Fatalf("total=%d returned=%d, want 3/3: %+v", thread.Total, len(thread.Messages), thread.Messages)
	}
	wantIDs := []string{"root@test", "reply1@test", "reply2@test"}
	for i, want := range wantIDs {
		if got := thread.Messages[i].MessageID; got != want {
			t.Errorf("message %d id = %q, want %q (not oldest first?)", i, got, want)
		}
	}
	for _, m := range thread.Messages {
		if m.Body != "" {
			t.Errorf("body returned without include_bodies: %q", m.Body)
		}
		if m.MessageID == "other@test" {
			t.Error("unrelated message pulled into the conversation")
		}
	}
}

// A reply seen from mid-conversation still yields the whole conversation,
// because the root is taken from its References chain rather than its own id.
func TestProtocolGetThreadFromReply(t *testing.T) {
	c := seedThread(t)
	uid, validity := seedUID(t, c, "INBOX", "Re: Plans")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != 3 {
		t.Errorf("total = %d, want 3: %+v", thread.Total, thread.Messages)
	}
	if thread.Messages[0].MessageID != "root@test" {
		t.Errorf("first message = %q, want the root", thread.Messages[0].MessageID)
	}
}

func TestProtocolGetThreadSearchesAnotherFolder(t *testing.T) {
	c := seedThread(t)
	uid, validity := seedUID(t, c, "INBOX", "Plans")
	_, sentValidity := seedUID(t, c, "Sent", "Re: Plans")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity, SearchFolder: "Sent",
	})
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if thread.Folder != "Sent" {
		t.Errorf("folder = %q, want Sent", thread.Folder)
	}
	// The uids belong to the searched folder, so its generation must come back.
	if thread.UIDValidity != sentValidity {
		t.Errorf("uidvalidity = %d, want Sent's %d", thread.UIDValidity, sentValidity)
	}
	if thread.Total != 1 || thread.Messages[0].MessageID != "mine@test" {
		t.Errorf("messages = %+v, want only the Sent reply", thread.Messages)
	}
}

// Bridge matches HEADER values by substring, so a message referencing an
// identifier that merely contains the root must be rejected after parsing.
func TestProtocolGetThreadRejectsSubstringMatch(t *testing.T) {
	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		appendMsg(t, u, "INBOX", 1, threadMessage(1, "Plans", "a@test", nil))
		appendMsg(t, u, "INBOX", 2, threadMessage(2, "Re: Plans", "real@test", []string{"a@test"}))
		// "a@test" is a substring of "bba@test": the server returns this one.
		appendMsg(t, u, "INBOX", 3, threadMessage(3, "Decoy", "decoy@test", []string{"bba@test"}))
	})
	uid, validity := seedUID(t, c, "INBOX", "Plans")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != 2 {
		t.Fatalf("total = %d, want 2: %+v", thread.Total, thread.Messages)
	}
	for _, m := range thread.Messages {
		if m.MessageID == "decoy@test" {
			t.Error("substring match survived verification")
		}
	}
}

func TestProtocolGetThreadIncludesBodies(t *testing.T) {
	c := seedThread(t)
	uid, validity := seedUID(t, c, "INBOX", "Plans")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity, IncludeBodies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range thread.Messages {
		if m.Body == "" {
			t.Errorf("message %q returned no body", m.MessageID)
		}
		if m.BodyIsHTML {
			t.Errorf("message %q reported HTML for a plain text part", m.MessageID)
		}
	}
	if got := thread.Messages[0].Body; got != "body of root@test\r\n" && got != "body of root@test\n" {
		t.Errorf("body = %q", got)
	}
}

// A message that starts a conversation nobody answered is a thread of one.
func TestProtocolGetThreadLoneMessage(t *testing.T) {
	c := seedThread(t)
	uid, validity := seedUID(t, c, "INBOX", "Unrelated")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != 1 || thread.Messages[0].MessageID != "other@test" {
		t.Errorf("messages = %+v, want just the message itself", thread.Messages)
	}
}

// inReplyToMessage names its parent without carrying a References chain, which
// RFC 5322 allows and simpler clients produce.
func inReplyToMessage(day int, subject, msgID, parent string) []byte {
	return []byte(fmt.Sprintf(
		"From: alice@example.org\r\nTo: me@example.org\r\nSubject: %s\r\nDate: Wed, %02d Jul 2026 10:00:00 +0000\r\nMessage-ID: <%s>\r\nIn-Reply-To: <%s>\r\n\r\nbody of %s\r\n",
		subject, day, msgID, parent, msgID))
}

func TestProtocolGetThreadFindsInReplyToOnlyReplies(t *testing.T) {
	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		appendMsg(t, u, "INBOX", 1, threadMessage(1, "Plans", "root@test", nil))
		appendMsg(t, u, "INBOX", 2, inReplyToMessage(2, "Re: Plans", "bare@test", "root@test"))
	})

	// From the root, the chainless reply must still be found.
	uid, validity := seedUID(t, c, "INBOX", "Plans")
	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != 2 {
		t.Errorf("from root: total = %d, want 2: %+v", thread.Total, thread.Messages)
	}

	// From the chainless reply, its parent anchors the conversation.
	uid, validity = seedUID(t, c, "INBOX", "Re: Plans")
	thread, err = c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != 2 {
		t.Fatalf("from reply: total = %d, want 2: %+v", thread.Total, thread.Messages)
	}
	if thread.Messages[0].MessageID != "root@test" {
		t.Errorf("first message = %q, want the parent", thread.Messages[0].MessageID)
	}
}

// A conversation past the return limit keeps its recent end, which is where the
// message the caller started from sits.
func TestProtocolGetThreadKeepsNewestWhenCapped(t *testing.T) {
	const total = MaxThreadMessages + 5
	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		appendMsg(t, u, "INBOX", 1, threadMessage(1, "Plans", "root@test", nil))
		for n := 2; n <= total; n++ {
			id := fmt.Sprintf("reply%d@test", n)
			appendMsg(t, u, "INBOX", n%28+1, threadMessage(n%28+1, "Re: Plans", id, []string{"root@test"}))
		}
	})
	uid, validity := seedUID(t, c, "INBOX", "Re: Plans")

	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != total {
		t.Errorf("total = %d, want %d", thread.Total, total)
	}
	if len(thread.Messages) != MaxThreadMessages {
		t.Fatalf("returned %d, want the %d cap", len(thread.Messages), MaxThreadMessages)
	}
	// The oldest messages are the ones dropped, so the root must be gone.
	for _, m := range thread.Messages {
		if m.MessageID == "root@test" {
			t.Error("truncation kept the oldest end of the conversation")
		}
	}
	last := thread.Messages[len(thread.Messages)-1]
	if last.MessageID != fmt.Sprintf("reply%d@test", total) {
		t.Errorf("newest kept message = %q, want the last reply", last.MessageID)
	}
}

// A chain long enough to be trimmed still names its conversation, so a deep
// descendant is recognised as a member.
func TestProtocolGetThreadHandlesTrimmedReferenceChain(t *testing.T) {
	deep := make([]string, 0, MaxThreadReferences+10)
	for n := range MaxThreadReferences + 10 {
		deep = append(deep, fmt.Sprintf("anc%d@test", n))
	}
	root := deep[0]

	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		appendMsg(t, u, "INBOX", 1, threadMessage(1, "Plans", root, nil))
		appendMsg(t, u, "INBOX", 2, threadMessage(2, "Re: Plans", "deep@test", deep))
	})
	uid, validity := seedUID(t, c, "INBOX", "Re: Plans")

	// Starting from the deep reply, its own chain is trimmed past the root.
	thread, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Total != 2 {
		t.Fatalf("total = %d, want 2: %+v", thread.Total, thread.Messages)
	}
	if thread.Messages[0].MessageID != root {
		t.Errorf("first message = %q, want the true root %q", thread.Messages[0].MessageID, root)
	}
}

func TestProtocolGetThreadRejectsStaleUIDValidity(t *testing.T) {
	c := seedThread(t)
	uid, validity := seedUID(t, c, "INBOX", "Plans")

	_, err := c.GetThread(context.Background(), ThreadRef{
		Folder: "INBOX", UID: uid, UIDValidity: validity + 1,
	})
	if err == nil {
		t.Fatal("expected an error for stale uidvalidity")
	}
}
