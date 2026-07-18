package bridgeclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	messageMail "github.com/emersion/go-message/mail"
)

func TestBuildDraftMessagePlainHTMLAndAttachment(t *testing.T) {
	draft := Draft{
		From:     "Alice Example <alice@example.org>",
		To:       []string{"Bob Example <bob@example.org>"},
		Cc:       []string{"carol@example.org"},
		Bcc:      []string{"hidden@example.org"},
		Subject:  "Café plans",
		TextBody: "plain body",
		HTMLBody: "<p>html body</p>",
		Attachments: []DraftAttachment{{
			Filename:    "café.txt",
			ContentType: "text/plain; charset=utf-8",
			Data:        []byte("attachment body"),
		}},
	}

	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatalf("validateDraft: %v", err)
	}
	raw, err := buildDraftMessage(draft, addresses, draftMetadata{messageID: "existing@example.org"}, time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildDraftMessage: %v", err)
	}
	if !bytes.Contains(raw, []byte("\r\n")) {
		t.Fatal("message should use CRLF line endings")
	}

	mr, err := messageMail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("CreateReader: %v", err)
	}
	defer mr.Close()

	if got, err := mr.Header.MessageID(); err != nil || got != "existing@example.org" {
		t.Errorf("Message-ID = %q, %v", got, err)
	}
	if got, err := mr.Header.Subject(); err != nil || got != draft.Subject {
		t.Errorf("Subject = %q, %v", got, err)
	}
	if got, err := mr.Header.AddressList("Bcc"); err != nil || len(got) != 1 || got[0].Address != "hidden@example.org" {
		t.Errorf("Bcc = %+v, %v", got, err)
	}

	parts := make(map[string]string)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		body, err := io.ReadAll(part.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch h := part.Header.(type) {
		case *messageMail.InlineHeader:
			contentType, _, _ := h.ContentType()
			parts[contentType] = string(body)
		case *messageMail.AttachmentHeader:
			filename, _ := h.Filename()
			parts[filename] = string(body)
		}
	}
	if parts["text/plain"] != draft.TextBody || parts["text/html"] != draft.HTMLBody || parts["café.txt"] != "attachment body" {
		t.Errorf("decoded parts = %#v", parts)
	}
}

func TestBuildDraftMessageGeneratesMessageID(t *testing.T) {
	draft := Draft{From: "alice@example.org"}
	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatalf("validateDraft: %v", err)
	}
	raw, err := buildDraftMessage(draft, addresses, draftMetadata{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mr, err := messageMail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := mr.Header.MessageID()
	if err != nil || messageID == "" {
		t.Errorf("generated Message-ID = %q, %v", messageID, err)
	}
	contentType, params, err := mr.Header.ContentType()
	if err != nil || contentType != "text/plain" || params["charset"] != "utf-8" {
		t.Errorf("Content-Type = %q %#v, %v", contentType, params, err)
	}
}

func TestBuildDraftMessageRejectsInvalidAttachmentContentType(t *testing.T) {
	draft := Draft{
		From: "alice@example.org",
		Attachments: []DraftAttachment{{
			Filename:    "data.bin",
			ContentType: "application/octet-stream",
		}},
	}
	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatalf("validateDraft: %v", err)
	}
	draft.Attachments[0].ContentType = "text/plain; charset"

	_, err = buildDraftMessage(draft, addresses, draftMetadata{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "invalid content_type") {
		t.Fatalf("error = %v, want invalid content_type", err)
	}
}

func TestValidateDraftRejectsInvalidAndOversizedInput(t *testing.T) {
	tests := []struct {
		name  string
		draft Draft
		want  string
	}{
		{name: "missing from", draft: Draft{}, want: "from is required"},
		{name: "invalid recipient", draft: Draft{From: "a@example.org", To: []string{"not an address"}}, want: "to address"},
		{name: "partial reference", draft: Draft{From: "a@example.org", Replace: &DraftRef{UID: 1}}, want: "uidvalidity"},
		{name: "too many attachments", draft: Draft{From: "a@example.org", Attachments: make([]DraftAttachment, MaxDraftAttachments+1)}, want: "at most"},
		{name: "oversized attachment", draft: Draft{From: "a@example.org", Attachments: []DraftAttachment{{Filename: "x", ContentType: "application/octet-stream", Data: make([]byte, MaxDraftAttachmentBytes+1)}}}, want: "above"},
		{name: "content disposition token", draft: Draft{From: "a@example.org", Attachments: []DraftAttachment{{Filename: "x", ContentType: "attachment"}}}, want: "invalid content_type"},
		{name: "oversized body", draft: Draft{From: "a@example.org", TextBody: strings.Repeat("x", MaxDraftBodyChars+1)}, want: "text_body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateDraft(tt.draft)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestSaveDraftCancellationWhileWaitingForSerialization(t *testing.T) {
	draftGate := make(chan struct{}, 1)
	draftGate <- struct{}{}
	client := &Client{draftGate: draftGate}

	base, cancel := context.WithCancel(context.Background())
	ctx := &observedDoneContext{Context: base, observed: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() {
		_, err := client.SaveDraft(ctx, Draft{From: "alice@example.org"})
		done <- err
	}()
	<-ctx.observed
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled save remained blocked behind draft serialization")
	}
	if len(draftGate) != 1 {
		t.Fatal("canceled waiter consumed the active save's serialization token")
	}
}

type observedDoneContext struct {
	context.Context
	observed chan struct{}
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	select {
	case ctx.observed <- struct{}{}:
	default:
	}
	return ctx.Context.Done()
}
