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
	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, 1, 0)
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
