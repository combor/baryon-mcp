package bridgeclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3, 4}

func multipartMessage() []byte {
	var b strings.Builder
	b.WriteString("From: alice@example.org\r\n")
	b.WriteString("To: me@example.org\r\n")
	b.WriteString("Cc: carol@example.org\r\n")
	b.WriteString("Subject: multipart test\r\n")
	b.WriteString("Date: Wed, 01 Jul 2026 10:00:00 +0000\r\n")
	b.WriteString("Message-ID: <mp@test>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=OUTER\r\n\r\n")

	b.WriteString("--OUTER\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=INNER\r\n\r\n")
	b.WriteString("--INNER\r\n")
	b.WriteString("Content-Type: text/plain; charset=windows-1252\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString("caf=E9 body\r\n")
	b.WriteString("--INNER\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
	b.WriteString("<p>html body</p>\r\n")
	b.WriteString("--INNER--\r\n")

	b.WriteString("--OUTER\r\n")
	b.WriteString("Content-Type: image/png\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"tiny.png\"\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString(pngBytes) + "\r\n")
	b.WriteString("--OUTER--\r\n")
	return []byte(b.String())
}

func seedContentInbox(t *testing.T) *Client {
	t.Helper()
	return startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader(multipartMessage()), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}

func liveRef(t *testing.T, c *Client) (uint32, uint32) {
	t.Helper()
	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Emails) != 1 {
		t.Fatalf("seeded message missing: %+v", page)
	}
	return page.Emails[0].UID, page.UIDValidity
}

func TestProtocolGetEmail(t *testing.T) {
	c := seedContentInbox(t)
	uid, uidval := liveRef(t, c)

	email, err := c.GetEmail(context.Background(), "INBOX", uid, uidval)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if email.Summary.Subject != "multipart test" {
		t.Errorf("subject = %q", email.Summary.Subject)
	}
	if len(email.Summary.Cc) != 1 || email.Summary.Cc[0] != "carol@example.org" {
		t.Errorf("cc = %+v", email.Summary.Cc)
	}
	if email.Plain == nil || email.Plain.Text != "café body" {
		t.Errorf("plain = %+v", email.Plain)
	}
	if email.Plain.CharsetFallback || email.Plain.Truncated {
		t.Errorf("unexpected flags: %+v", email.Plain)
	}
	if email.HTML == nil || !strings.Contains(email.HTML.Text, "<p>html body</p>") {
		t.Errorf("html = %+v", email.HTML)
	}
	if len(email.Attachments) != 1 || email.Attachments[0].Filename != "tiny.png" || email.Attachments[0].ContentType != "image/png" {
		t.Errorf("attachments = %+v", email.Attachments)
	}
}

func TestProtocolGetEmailExposesThreadHeaders(t *testing.T) {
	raw := "From: bob@example.org\r\n" +
		"To: me@example.org\r\n" +
		"Subject: Re: plans\r\n" +
		"Message-ID: <parent@example.org>\r\n" +
		"X-Pm-Internal-Id: bridge-id\r\n" +
		"In-Reply-To: <root@example.org>\r\n" +
		"References: <root@example.org> <bridge-id@protonmail.internalid>\r\n" +
		"\r\nbody\r\n"
	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader([]byte(raw)), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	uid, uidval := liveRef(t, c)

	email, err := c.GetEmail(context.Background(), "INBOX", uid, uidval)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if email.MessageID != "parent@example.org" {
		t.Errorf("message-id = %q", email.MessageID)
	}
	if len(email.InReplyTo) != 1 || email.InReplyTo[0] != "root@example.org" {
		t.Errorf("in-reply-to = %v", email.InReplyTo)
	}
	// Bridge's self-reference would poison a reply's References chain.
	if len(email.References) != 1 || email.References[0] != "root@example.org" {
		t.Errorf("references = %v", email.References)
	}
	// The added header section must not disturb body decoding.
	if email.Plain == nil || strings.TrimSpace(email.Plain.Text) != "body" {
		t.Errorf("plain = %+v", email.Plain)
	}
}

func TestProtocolGetEmailWrongUIDValidity(t *testing.T) {
	c := seedContentInbox(t)
	uid, uidval := liveRef(t, c)

	_, err := c.GetEmail(context.Background(), "INBOX", uid, uidval+1)
	if err == nil || !strings.Contains(err.Error(), "UIDVALIDITY changed") {
		t.Errorf("err = %v, want UIDVALIDITY mismatch", err)
	}
}

func TestProtocolGetEmailMissingUID(t *testing.T) {
	c := seedContentInbox(t)
	_, uidval := liveRef(t, c)

	_, err := c.GetEmail(context.Background(), "INBOX", 9999, uidval)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not-found", err)
	}
}

func TestProtocolAttachmentRoundtrip(t *testing.T) {
	c := seedContentInbox(t)
	uid, uidval := liveRef(t, c)

	infos, err := c.ListAttachments(context.Background(), "INBOX", uid, uidval)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Filename != "tiny.png" {
		t.Fatalf("infos = %+v", infos)
	}

	att, err := c.GetAttachment(context.Background(), "INBOX", uid, uidval, infos[0].Index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(att.Data, pngBytes) {
		t.Errorf("data mismatch: got %v want %v", att.Data, pngBytes)
	}
	if att.ContentType != "image/png" || att.Filename != "tiny.png" {
		t.Errorf("meta = %+v", att)
	}

	if _, err := c.GetAttachment(context.Background(), "INBOX", uid, uidval, 5); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("err = %v, want out-of-range", err)
	}
}

func TestProtocolAttachmentSizeCapRefusal(t *testing.T) {
	big := bytes.Repeat([]byte{0xAB}, attachmentCap) // above the transfer cap once base64-encoded
	var b strings.Builder
	b.WriteString("From: a@x\r\nTo: b@x\r\nSubject: big\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=B\r\n\r\n")
	b.WriteString("--B\r\nContent-Type: text/plain\r\n\r\nsee attachment\r\n")
	b.WriteString("--B\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"big.bin\"\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString(big) + "\r\n")
	b.WriteString("--B--\r\n")

	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader([]byte(b.String())), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	uid, uidval := liveRef(t, c)

	_, err := c.GetAttachment(context.Background(), "INBOX", uid, uidval, 0)
	if err == nil || !strings.Contains(err.Error(), "above the") {
		t.Errorf("err = %v, want size-cap refusal", err)
	}
}

func TestProtocolTextPartPartialCap(t *testing.T) {
	longBody := strings.Repeat("x", textPartCap+50_000)
	msg := fmt.Sprintf("From: a@x\r\nTo: b@x\r\nSubject: long\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", longBody)

	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader([]byte(msg)), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	uid, uidval := liveRef(t, c)

	email, err := c.GetEmail(context.Background(), "INBOX", uid, uidval)
	if err != nil {
		t.Fatal(err)
	}
	if email.Plain == nil || !email.Plain.Truncated {
		t.Fatalf("plain = %+v, want truncated", email.Plain)
	}
	if got := len([]rune(email.Plain.Text)); got != bodyCharCap {
		t.Errorf("text length = %d, want char cap %d", got, bodyCharCap)
	}
}

var pdfBytes = []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj")

// latin2CSV is ISO-8859-2 text in a part labelled with that charset, a label
// the draft writer refuses, so carrying it must keep the bytes and drop the label.
var latin2CSV = []byte("miasto;kurs\n\xa3\xf3d\xbc;4,21\n")

// newsletterPlain is a plain body far past the 50,000 characters a read for
// the conversation keeps.
var newsletterPlain = strings.Repeat(strings.Repeat("x", 98)+"\r\n", 1200)

// invite is a calendar request, whose method parameter makes it one.
var invite = []byte("BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nEND:VCALENDAR\r\n")

// relatedMessage carries an inline logo the HTML references, a PDF with an
// unreferenced Content-ID as Apple Mail writes them, a Latin-2 CSV and a
// calendar invitation.
func relatedMessage(plain string) []byte {
	var b strings.Builder
	b.WriteString("From: Acme News <news@acme.example>\r\n")
	b.WriteString("To: me@example.org\r\n")
	b.WriteString("Subject: newsletter\r\n")
	b.WriteString("Date: Mon, 12 Oct 2026 09:14:00 +0100\r\n")
	b.WriteString("Message-ID: <news@acme.example>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=OUTER\r\n\r\n")

	b.WriteString("--OUTER\r\n")
	b.WriteString("Content-Type: multipart/related; boundary=REL; type=\"multipart/alternative\"\r\n\r\n")
	b.WriteString("--REL\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=ALT\r\n\r\n")
	b.WriteString("--ALT\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
	b.WriteString(plain + "\r\n")
	b.WriteString("--ALT\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
	b.WriteString("<p>Hello <img src=\"cid:logo@acme.example\"></p>\r\n")
	b.WriteString("--ALT--\r\n")
	b.WriteString("--REL\r\n")
	b.WriteString("Content-Type: image/png\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-ID: <logo@acme.example>\r\n")
	b.WriteString("Content-Disposition: inline; filename=\"logo.png\"\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString(pngBytes) + "\r\n")
	b.WriteString("--REL--\r\n")

	b.WriteString("--OUTER\r\n")
	b.WriteString("Content-Type: application/pdf\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-ID: <pdf@apple.example>\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"report.pdf\"\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString(pdfBytes) + "\r\n")
	b.WriteString("--OUTER\r\n")
	b.WriteString("Content-Type: text/csv; charset=iso-8859-2\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"rates.csv\"\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString(latin2CSV) + "\r\n")
	b.WriteString("--OUTER\r\n")
	b.WriteString("Content-Type: text/calendar; charset=utf-8; method=REQUEST\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"invite.ics\"\r\n\r\n")
	b.Write(invite)
	b.WriteString("\r\n--OUTER--\r\n")
	return []byte(b.String())
}

func seedSourceInbox(t *testing.T, raw []byte) (*Client, uint32, uint32) {
	t.Helper()
	c := startMemServer(t, func(u *imapmemserver.User) {
		if err := u.Create("INBOX", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := u.Append("INBOX", bytes.NewReader(raw), &imap.AppendOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	uid, uidval := liveRef(t, c)
	return c, uid, uidval
}

func TestProtocolGetMessageSourceCarriesEverything(t *testing.T) {
	c, uid, uidval := seedSourceInbox(t, relatedMessage(newsletterPlain))

	source, err := c.GetMessageSource(context.Background(), "INBOX", uid, uidval, AllParts)
	if err != nil {
		t.Fatalf("GetMessageSource: %v", err)
	}
	plain := source.Content.Plain
	if plain == nil {
		t.Fatal("plain body missing")
	}
	if plain.Truncated || plain.Text != newsletterPlain {
		t.Fatalf("plain body was not carried whole: %d chars, truncated %v", len(plain.Text), plain.Truncated)
	}
	if source.Content.HTML == nil || !strings.Contains(source.Content.HTML.Text, "cid:logo@acme.example") {
		t.Errorf("html = %+v", source.Content.HTML)
	}
	if source.Content.MessageID != "news@acme.example" || source.Content.Summary.Subject != "newsletter" {
		t.Errorf("content = %+v", source.Content.Summary)
	}

	want := []DraftAttachment{
		{Filename: "logo.png", ContentType: "image/png", ContentID: "logo@acme.example", Data: pngBytes},
		{Filename: "report.pdf", ContentType: "application/pdf", Data: pdfBytes},
		{Filename: "rates.csv", ContentType: "text/csv", Data: latin2CSV},
		{Filename: "invite.ics", ContentType: "text/calendar; charset=utf-8; method=REQUEST", Data: invite},
	}
	if len(source.Attachments) != len(want) {
		t.Fatalf("attachments = %+v, want %d", source.Attachments, len(want))
	}
	for i, w := range want {
		got := source.Attachments[i]
		if got.Filename != w.Filename || got.ContentType != w.ContentType || got.ContentID != w.ContentID || !bytes.Equal(got.Data, w.Data) {
			t.Errorf("attachment %d = %s %s %q %d bytes, want %s %s %q %d bytes", i,
				got.Filename, got.ContentType, got.ContentID, len(got.Data), w.Filename, w.ContentType, w.ContentID, len(w.Data))
		}
	}
}

func TestProtocolGetMessageSourceReferencedInlineOnly(t *testing.T) {
	c, uid, uidval := seedSourceInbox(t, relatedMessage("short"))

	source, err := c.GetMessageSource(context.Background(), "INBOX", uid, uidval, ReferencedInlineParts)
	if err != nil {
		t.Fatalf("GetMessageSource: %v", err)
	}
	if len(source.Attachments) != 1 || source.Attachments[0].ContentID != "logo@acme.example" {
		t.Fatalf("attachments = %+v, want only the referenced logo", source.Attachments)
	}
}

func TestProtocolGetMessageSourceRefusesOversizedBody(t *testing.T) {
	c, uid, uidval := seedSourceInbox(t, relatedMessage(strings.Repeat("y", sourceTextPartCap+1)))

	_, err := c.GetMessageSource(context.Background(), "INBOX", uid, uidval, AllParts)
	if err == nil || !strings.Contains(err.Error(), "above the") {
		t.Fatalf("error = %v, want the body refused", err)
	}
}

func TestProtocolGetMessageSourceRefusesTooManyAttachments(t *testing.T) {
	var b strings.Builder
	b.WriteString("From: alice@example.org\r\nSubject: many\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=B\r\n\r\n")
	b.WriteString("--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n")
	for i := 0; i <= MaxDraftAttachments; i++ {
		fmt.Fprintf(&b, "--B\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"%d.bin\"\r\n\r\nx\r\n", i)
	}
	b.WriteString("--B--\r\n")
	c, uid, uidval := seedSourceInbox(t, []byte(b.String()))

	_, err := c.GetMessageSource(context.Background(), "INBOX", uid, uidval, AllParts)
	if err == nil || !strings.Contains(err.Error(), "attachments") {
		t.Fatalf("error = %v, want too many attachments refused", err)
	}
}

func TestProtocolGetMessageSourceRefusesOversizedAttachments(t *testing.T) {
	var b strings.Builder
	b.WriteString("From: alice@example.org\r\nSubject: large\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=B\r\n\r\n")
	b.WriteString("--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n")
	line := strings.Repeat("A", 76) + "\r\n"
	for i := 0; i < 2; i++ {
		fmt.Fprintf(&b, "--B\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"%d.bin\"\r\n\r\n", i)
		b.WriteString(strings.Repeat(line, sourceAttachmentCap/2/len(line)+1))
	}
	b.WriteString("--B--\r\n")
	c, uid, uidval := seedSourceInbox(t, []byte(b.String()))

	_, err := c.GetMessageSource(context.Background(), "INBOX", uid, uidval, AllParts)
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("error = %v, want oversized attachments refused", err)
	}
}

// A sloppy original must still forward: damage is reported, not fatal.
func TestProtocolGetMessageSourceToleratesDamagedEncodings(t *testing.T) {
	var b strings.Builder
	b.WriteString("From: alice@example.org\r\nSubject: sloppy\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=B\r\n\r\n")
	b.WriteString("--B\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n@@@@not base64!!\r\n")
	b.WriteString("--B\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"unpadded.pdf\"\r\n\r\nJVBERg\r\n")
	b.WriteString("--B\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"broken.bin\"\r\n\r\nJVBE@@@@\r\n")
	b.WriteString("--B--\r\n")
	c, uid, uidval := seedSourceInbox(t, []byte(b.String()))

	source, err := c.GetMessageSource(context.Background(), "INBOX", uid, uidval, AllParts)
	if err != nil {
		t.Fatalf("GetMessageSource: %v", err)
	}
	if plain := source.Content.Plain; plain == nil || !plain.DecodeFallback {
		t.Errorf("plain = %+v, want the undecodable body flagged", plain)
	}
	if len(source.Attachments) != 2 || string(source.Attachments[0].Data) != "%PDF" || string(source.Attachments[1].Data) != "%PD" {
		t.Fatalf("attachments = %+v, want the unpadded PDF whole and the readable start of the broken file", source.Attachments)
	}
	if len(source.Damaged) != 1 || source.Damaged[0] != "broken.bin" {
		t.Errorf("damaged = %v, want only broken.bin", source.Damaged)
	}
}
