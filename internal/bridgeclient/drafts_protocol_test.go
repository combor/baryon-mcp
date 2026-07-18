package bridgeclient

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

var draftTestCaps = imap.CapSet{
	imap.CapIMAP4rev1: {},
	imap.CapUIDPlus:   {},
}

type draftTestSession struct {
	imapserver.Session
	failExpunge bool
	failAppend  bool
	listData    []imap.ListData
}

func (s *draftTestSession) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	if s.failAppend {
		return nil, errors.New("forced append failure")
	}
	return s.Session.Append(mailbox, r, options)
}

func (s *draftTestSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if options != nil && (options.SelectSpecialUse || options.ReturnSpecialUse) {
		return errors.New("extended special-use LIST is not supported")
	}
	if s.listData == nil {
		return s.Session.List(w, ref, patterns, options)
	}
	for i := range s.listData {
		if err := w.WriteList(&s.listData[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *draftTestSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if s.failExpunge {
		return errors.New("forced expunge failure")
	}
	return s.Session.Expunge(w, uids)
}

func seedDraftMailbox(t *testing.T, failExpunge bool) *Client {
	t.Helper()
	return startMemServerWithOptions(t, func(u *imapmemserver.User) {
		if err := u.Create("Drafts", nil); err != nil {
			t.Fatal(err)
		}
	}, draftTestCaps, func(session imapserver.Session) imapserver.Session {
		return &draftTestSession{Session: session, failExpunge: failExpunge}
	})
}

func draftMessageID(t *testing.T, client *Client, uid uint32) string {
	t.Helper()
	var messageID string
	err := client.withSession(context.Background(), func(cli *imapclient.Client) error {
		if _, err := cli.Select("Drafts", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return err
		}
		messages, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{Envelope: true}).Collect()
		if err != nil {
			return err
		}
		if len(messages) != 1 || messages[0].Envelope == nil {
			return errors.New("draft envelope missing")
		}
		messageID = messages[0].Envelope.MessageID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return messageID
}

func TestProtocolSaveDraftCreatesMultipartDraft(t *testing.T) {
	client := seedDraftMailbox(t, false)
	saved, err := client.SaveDraft(context.Background(), Draft{
		From:     "alice@example.org",
		To:       []string{"bob@example.org"},
		Bcc:      []string{"hidden@example.org"},
		Subject:  "first draft",
		TextBody: "plain body",
		HTMLBody: "<p>html body</p>",
		Attachments: []DraftAttachment{{
			Filename: "note.txt", ContentType: "text/plain", Data: []byte("attachment"),
		}},
	})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if saved.Folder != "Drafts" || saved.UID == 0 || saved.UIDValidity == 0 || saved.ReplacedUID != 0 {
		t.Fatalf("saved = %+v", saved)
	}

	email, err := client.GetEmail(context.Background(), saved.Folder, saved.UID, saved.UIDValidity)
	if err != nil {
		t.Fatal(err)
	}
	if email.Summary.Subject != "first draft" || email.Plain == nil || email.Plain.Text != "plain body" || email.HTML == nil || email.HTML.Text != "<p>html body</p>" {
		t.Errorf("email = %+v", email)
	}
	if !slices.Equal(email.Summary.Bcc, []string{"hidden@example.org"}) {
		t.Errorf("bcc = %+v", email.Summary.Bcc)
	}
	if len(email.Attachments) != 1 || email.Attachments[0].Filename != "note.txt" {
		t.Errorf("attachments = %+v", email.Attachments)
	}

	var hasDraftFlag bool
	err = client.withSession(context.Background(), func(cli *imapclient.Client) error {
		if _, err := cli.Select("Drafts", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return err
		}
		messages, err := cli.Fetch(imap.UIDSetNum(imap.UID(saved.UID)), &imap.FetchOptions{Flags: true}).Collect()
		if err != nil {
			return err
		}
		hasDraftFlag = len(messages) == 1 && slices.Contains(messages[0].Flags, imap.FlagDraft)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDraftFlag {
		t.Error("saved message is missing the \\Draft flag")
	}
}

func TestProtocolSavedDraftMaximumNonASCIIBodyIsReadable(t *testing.T) {
	client := seedDraftMailbox(t, false)
	body := strings.Repeat("🙂", MaxDraftBodyChars)
	saved, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org", TextBody: body,
	})
	if err != nil {
		t.Fatal(err)
	}

	email, err := client.GetEmail(context.Background(), saved.Folder, saved.UID, saved.UIDValidity)
	if err != nil {
		t.Fatal(err)
	}
	if email.Plain == nil || email.Plain.Truncated || email.Plain.Text != body {
		t.Fatalf("plain body was not fully readable: %+v", email.Plain)
	}
}

func TestProtocolSavedDraftMaximumAttachmentIsReadable(t *testing.T) {
	client := seedDraftMailbox(t, false)
	want := bytes.Repeat([]byte{0xAB}, MaxDraftAttachmentBytes)
	saved, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org",
		Attachments: []DraftAttachment{{
			Filename: "maximum.bin", ContentType: "application/octet-stream", Data: want,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	attachment, err := client.GetAttachment(context.Background(), saved.Folder, saved.UID, saved.UIDValidity, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(attachment.Data, want) {
		t.Fatal("retrieved attachment differs from the saved attachment")
	}
}

func TestProtocolSaveDraftDiscoversDraftsWithOrdinaryList(t *testing.T) {
	tests := []struct {
		name      string
		mailboxes []string
		listData  []imap.ListData
		want      string
	}{
		{
			name:      "special-use attribute wins over name fallback",
			mailboxes: []string{"Drafts", "Localized Drafts"},
			listData: []imap.ListData{
				{Mailbox: "Drafts", Delim: '/'},
				{Mailbox: "Localized Drafts", Delim: '/', Attrs: []imap.MailboxAttr{imap.MailboxAttrDrafts}},
			},
			want: "Localized Drafts",
		},
		{
			name:      "case-insensitive name fallback",
			mailboxes: []string{"dRaFtS"},
			listData:  []imap.ListData{{Mailbox: "dRaFtS", Delim: '/'}},
			want:      "dRaFtS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := startMemServerWithOptions(t, func(u *imapmemserver.User) {
				for _, mailbox := range tt.mailboxes {
					if err := u.Create(mailbox, nil); err != nil {
						t.Fatal(err)
					}
				}
			}, draftTestCaps, func(session imapserver.Session) imapserver.Session {
				return &draftTestSession{Session: session, listData: tt.listData}
			})

			saved, err := client.SaveDraft(context.Background(), Draft{From: "alice@example.org"})
			if err != nil {
				t.Fatalf("SaveDraft: %v", err)
			}
			if saved.Folder != tt.want {
				t.Errorf("folder = %q, want %q", saved.Folder, tt.want)
			}
		})
	}
}

func TestProtocolSaveDraftReplacesUIDAndPreservesMessageID(t *testing.T) {
	client := seedDraftMailbox(t, false)
	first, err := client.SaveDraft(context.Background(), Draft{From: "alice@example.org", Subject: "first", TextBody: "one"})
	if err != nil {
		t.Fatal(err)
	}
	messageID := draftMessageID(t, client, first.UID)

	second, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org", Subject: "second", TextBody: "two",
		Replace: &DraftRef{UID: first.UID, UIDValidity: first.UIDValidity},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.UID == first.UID || second.ReplacedUID != first.UID || !second.PreviousDraftRemoved || second.Warning != "" {
		t.Fatalf("replacement = %+v", second)
	}
	if got := draftMessageID(t, client, second.UID); got != messageID {
		t.Errorf("replacement Message-ID = %q, want %q", got, messageID)
	}
	page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Emails[0].UID != second.UID || page.Emails[0].Subject != "second" {
		t.Errorf("Drafts page = %+v", page)
	}
}

func TestProtocolSaveDraftReplacementIgnoresBridgeSelfReference(t *testing.T) {
	var ref DraftRef
	client := startMemServerWithOptions(t, func(u *imapmemserver.User) {
		if err := u.Create("Drafts", nil); err != nil {
			t.Fatal(err)
		}
		raw := "From: alice@example.org\r\n" +
			"Subject: ordinary draft\r\n" +
			"Message-ID: <draft@example.org>\r\n" +
			"X-Pm-Internal-Id: bridge-draft-id\r\n" +
			"References: <bridge-draft-id@protonmail.internalid>\r\n\r\nbody\r\n"
		data, err := u.Append("Drafts", bytes.NewReader([]byte(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft}})
		if err != nil {
			t.Fatal(err)
		}
		ref = DraftRef{UID: uint32(data.UID), UIDValidity: data.UIDValidity}
	}, draftTestCaps, func(session imapserver.Session) imapserver.Session {
		return &draftTestSession{Session: session}
	})

	saved, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org", Subject: "edited", TextBody: "replacement", Replace: &ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.PreviousDraftRemoved || saved.ReplacedUID != ref.UID {
		t.Fatalf("replacement = %+v", saved)
	}
	if got := draftMessageID(t, client, saved.UID); got != "draft@example.org" {
		t.Errorf("replacement Message-ID = %q", got)
	}
}

func TestProtocolSaveDraftReplacementRejectsThreadHeaders(t *testing.T) {
	for _, tt := range []struct {
		name    string
		headers string
	}{
		{name: "In-Reply-To", headers: "In-Reply-To: <parent@example.org>\r\n"},
		{name: "References", headers: "X-Pm-Internal-Id: bridge-draft-id\r\nReferences: <root@example.org> <bridge-draft-id@protonmail.internalid>\r\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var ref DraftRef
			client := startMemServerWithOptions(t, func(u *imapmemserver.User) {
				if err := u.Create("Drafts", nil); err != nil {
					t.Fatal(err)
				}
				raw := "From: alice@example.org\r\n" +
					"Subject: reply\r\n" +
					"Message-ID: <draft@example.org>\r\n" + tt.headers + "\r\nbody\r\n"
				data, err := u.Append("Drafts", bytes.NewReader([]byte(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft}})
				if err != nil {
					t.Fatal(err)
				}
				ref = DraftRef{UID: uint32(data.UID), UIDValidity: data.UIDValidity}
			}, draftTestCaps, func(session imapserver.Session) imapserver.Session {
				return &draftTestSession{Session: session}
			})

			_, err := client.SaveDraft(context.Background(), Draft{
				From: "alice@example.org", Subject: "edited reply", TextBody: "replacement", Replace: &ref,
			})
			if err == nil || !strings.Contains(err.Error(), "cannot preserve") {
				t.Fatalf("error = %v, want reply-thread preservation refusal", err)
			}
			page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || page.Emails[0].UID != ref.UID || page.Emails[0].Subject != "reply" {
				t.Errorf("refused reply replacement mutated Drafts: %+v", page)
			}
		})
	}
}

func TestProtocolSaveDraftRejectsMalformedThreadHeadersBeforeAppend(t *testing.T) {
	var ref DraftRef
	client := startMemServerWithOptions(t, func(u *imapmemserver.User) {
		if err := u.Create("Drafts", nil); err != nil {
			t.Fatal(err)
		}
		raw := "From: alice@example.org\r\n" +
			"Subject: malformed thread\r\n" +
			"Message-ID: <draft@example.org>\r\n" +
			"References: not-a-message-id\r\n\r\nbody\r\n"
		data, err := u.Append("Drafts", bytes.NewReader([]byte(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft}})
		if err != nil {
			t.Fatal(err)
		}
		ref = DraftRef{UID: uint32(data.UID), UIDValidity: data.UIDValidity}
	}, draftTestCaps, func(session imapserver.Session) imapserver.Session {
		return &draftTestSession{Session: session}
	})

	_, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org", TextBody: "replacement", Replace: &ref,
	})
	if err == nil || !strings.Contains(err.Error(), "References") {
		t.Fatalf("error = %v, want malformed References refusal", err)
	}
	page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Emails[0].UID != ref.UID {
		t.Errorf("malformed replacement mutated Drafts: %+v", page)
	}
}

func TestProtocolSaveDraftRejectsStaleOrMissingUIDBeforeAppend(t *testing.T) {
	for _, mutate := range []func(DraftRef) DraftRef{
		func(ref DraftRef) DraftRef { ref.UIDValidity++; return ref },
		func(ref DraftRef) DraftRef { ref.UID += 100; return ref },
	} {
		client := seedDraftMailbox(t, false)
		first, err := client.SaveDraft(context.Background(), Draft{From: "alice@example.org", TextBody: "original"})
		if err != nil {
			t.Fatal(err)
		}
		ref := mutate(DraftRef{UID: first.UID, UIDValidity: first.UIDValidity})
		_, err = client.SaveDraft(context.Background(), Draft{From: "alice@example.org", TextBody: "replacement", Replace: &ref})
		if err == nil {
			t.Fatal("expected stale reference error")
		}
		page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || page.Emails[0].UID != first.UID {
			t.Errorf("failed replacement mutated Drafts: %+v", page)
		}
	}
}

func TestProtocolSaveDraftReportsCleanupFailure(t *testing.T) {
	client := seedDraftMailbox(t, true)
	first, err := client.SaveDraft(context.Background(), Draft{From: "alice@example.org", TextBody: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org", TextBody: "second",
		Replace: &DraftRef{UID: first.UID, UIDValidity: first.UIDValidity},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.PreviousDraftRemoved || !strings.Contains(second.Warning, "previous draft may remain") {
		t.Errorf("replacement = %+v", second)
	}
	page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Errorf("Drafts total = %d, want old and replacement after cleanup failure", page.Total)
	}
}

func TestProtocolSaveDraftAppendFailurePreservesPreviousDraft(t *testing.T) {
	var ref DraftRef
	client := startMemServerWithOptions(t, func(u *imapmemserver.User) {
		if err := u.Create("Drafts", nil); err != nil {
			t.Fatal(err)
		}
		data, err := u.Append("Drafts", bytes.NewReader([]byte("From: alice@example.org\r\nSubject: original\r\nMessage-ID: <original@example.org>\r\n\r\nbody\r\n")), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft}})
		if err != nil {
			t.Fatal(err)
		}
		ref = DraftRef{UID: uint32(data.UID), UIDValidity: data.UIDValidity}
	}, draftTestCaps, func(session imapserver.Session) imapserver.Session {
		return &draftTestSession{Session: session, failAppend: true}
	})

	_, err := client.SaveDraft(context.Background(), Draft{
		From: "alice@example.org", TextBody: "replacement", Replace: &ref,
	})
	if err == nil || !strings.Contains(err.Error(), "appending draft") {
		t.Fatalf("error = %v", err)
	}
	page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Emails[0].UID != ref.UID || page.Emails[0].Subject != "original" {
		t.Errorf("append failure mutated Drafts: %+v", page)
	}
}

func TestProtocolSaveDraftRequiresUIDPlusBeforeMutation(t *testing.T) {
	client := startMemServerWithOptions(t, func(u *imapmemserver.User) {
		if err := u.Create("Drafts", nil); err != nil {
			t.Fatal(err)
		}
	}, imap.CapSet{
		imap.CapIMAP4rev1: {},
	}, func(session imapserver.Session) imapserver.Session {
		return &draftTestSession{Session: session}
	})

	_, err := client.SaveDraft(context.Background(), Draft{From: "alice@example.org"})
	if err == nil || !strings.Contains(err.Error(), "UIDPLUS") {
		t.Fatalf("error = %v", err)
	}
	page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Errorf("Drafts total = %d after capability refusal", page.Total)
	}
}

func TestProtocolSaveDraftSerializesConcurrentReplacement(t *testing.T) {
	client := seedDraftMailbox(t, false)
	first, err := client.SaveDraft(context.Background(), Draft{From: "alice@example.org", TextBody: "first"})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, body := range []string{"second", "third"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := client.SaveDraft(context.Background(), Draft{
				From: "alice@example.org", TextBody: body,
				Replace: &DraftRef{UID: first.UID, UIDValidity: first.UIDValidity},
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, failures int
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("successes=%d failures=%d", successes, failures)
	}
	page, err := client.ListMessages(context.Background(), "Drafts", SearchCriteria{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Errorf("Drafts total = %d, want one replacement", page.Total)
	}
}
