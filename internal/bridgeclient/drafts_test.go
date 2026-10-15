package bridgeclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message"
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
	raw, err := buildDraftMessage(draft, addresses, threadHeaders{messageID: "existing@example.org"}, time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
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

func TestBuildDraftMessageNestsInlinePartsInRelated(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nlogo")
	draft := Draft{
		From:     "alice@example.org",
		Subject:  "Fwd: newsletter",
		TextBody: "plain body",
		HTMLBody: `<p>html body <img src="cid:logo@acme.example"></p>`,
		Attachments: []DraftAttachment{
			{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.7")},
			{Filename: "logo.png", ContentType: "image/png", ContentID: "logo@acme.example", Data: png},
		},
	}
	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatalf("validateDraft: %v", err)
	}
	raw, err := buildDraftMessage(draft, addresses, threadHeaders{}, time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildDraftMessage: %v", err)
	}
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("message.Read: %v", err)
	}
	if got := entity.Header.Get("MIME-Version"); got != "1.0" {
		t.Errorf("MIME-Version = %q", got)
	}

	type leaf struct{ mediaType, params, disposition, filename, contentID, body string }
	got := make(map[string]leaf)
	err = entity.Walk(func(path []int, part *message.Entity, err error) error {
		if err != nil {
			return err
		}
		mediaType, params, _ := part.Header.ContentType()
		l := leaf{mediaType: mediaType, params: params["type"], contentID: part.Header.Get("Content-Id")}
		if disposition, dispParams, err := part.Header.ContentDisposition(); err == nil {
			l.disposition, l.filename = disposition, dispParams["filename"]
		}
		if !strings.HasPrefix(mediaType, "multipart/") {
			body, err := io.ReadAll(part.Body)
			if err != nil {
				return err
			}
			l.body = string(body)
		}
		got[fmt.Sprint(path)] = l
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	want := map[string]leaf{
		"[]":      {mediaType: "multipart/mixed"},
		"[0]":     {mediaType: "multipart/related", params: "multipart/alternative"},
		"[0 0]":   {mediaType: "multipart/alternative"},
		"[0 0 0]": {mediaType: "text/plain", disposition: "inline", body: draft.TextBody},
		"[0 0 1]": {mediaType: "text/html", disposition: "inline", body: draft.HTMLBody},
		"[0 1]":   {mediaType: "image/png", disposition: "inline", filename: "logo.png", contentID: "<logo@acme.example>", body: string(png)},
		"[1]":     {mediaType: "application/pdf", disposition: "attachment", filename: "report.pdf", body: "%PDF-1.7"},
	}
	if len(got) != len(want) {
		t.Errorf("parts = %#v, want %d parts", got, len(want))
	}
	for path, w := range want {
		if got[path] != w {
			t.Errorf("part %s = %#v, want %#v", path, got[path], w)
		}
	}
}

// RFC 2046 section 5.2.1 confines message/rfc822 to 7bit, 8bit or binary, so an
// attached message must not be base64-encoded the way other files are.
func TestBuildDraftMessageKeepsAttachedMessagesUnencoded(t *testing.T) {
	ascii := []byte("From: a@example.org\r\nSubject: nested\r\n\r\nhello\r\n")
	utf8Message := []byte("From: a@example.org\r\nSubject: nested\r\n\r\ncaf\xc3\xa9\r\n")
	withNUL := []byte("From: a@example.org\r\n\r\nnul\x00here\r\n")
	bareLF := []byte("From: a@example.org\nSubject: unix\n\nhello\n")
	bareCR := []byte("From: a@example.org\r\n\r\nold\rmac\r\n")
	longLine := []byte("From: a@example.org\r\n\r\n" + strings.Repeat("x", 999) + "\r\n")
	maxLine := []byte("From: a@example.org\r\n\r\n" + strings.Repeat("x", 998) + "\r\n")
	cases := []struct {
		name              string
		data              []byte
		contentType       string
		wantType, wantCTE string
	}{
		{name: "ascii", data: ascii, wantType: "message/rfc822", wantCTE: "7bit"},
		{name: "delivery status", data: ascii, contentType: "message/delivery-status", wantType: "message/delivery-status", wantCTE: "7bit"},
		{name: "global", data: utf8Message, contentType: "message/global", wantType: "message/global", wantCTE: "base64"},
		{name: "8bit", data: utf8Message, wantType: "message/rfc822", wantCTE: "8bit"},
		{name: "nul", data: withNUL, wantType: "application/octet-stream", wantCTE: "base64"},
		{name: "bare lf", data: bareLF, wantType: "application/octet-stream", wantCTE: "base64"},
		{name: "bare cr", data: bareCR, wantType: "application/octet-stream", wantCTE: "base64"},
		{name: "overlong line", data: longLine, wantType: "application/octet-stream", wantCTE: "base64"},
		{name: "longest line", data: maxLine, wantType: "message/rfc822", wantCTE: "7bit"},
	}
	for _, tc := range cases {
		for _, withInline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s inline=%v", tc.name, withInline), func(t *testing.T) {
				draft := Draft{
					From:        "alice@example.org",
					TextBody:    "plain",
					HTMLBody:    `<p><img src="cid:logo@x"></p>`,
					Attachments: []DraftAttachment{{Filename: "nested.eml", ContentType: "message/rfc822", Data: tc.data}},
				}
				if tc.contentType != "" {
					draft.Attachments[0].ContentType = tc.contentType
				}
				if withInline {
					draft.Attachments = append(draft.Attachments, DraftAttachment{Filename: "logo.png", ContentType: "image/png", ContentID: "logo@x", Data: []byte("png")})
				}
				addresses, err := validateDraft(draft)
				if err != nil {
					t.Fatalf("validateDraft: %v", err)
				}
				raw, err := buildDraftMessage(draft, addresses, threadHeaders{}, time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatalf("buildDraftMessage: %v", err)
				}
				entity, err := message.Read(bytes.NewReader(raw))
				if err != nil {
					t.Fatalf("message.Read: %v", err)
				}
				found := false
				err = entity.Walk(func(path []int, part *message.Entity, err error) error {
					if err != nil {
						return err
					}
					if _, params, _ := part.Header.ContentDisposition(); params["filename"] != "nested.eml" {
						return nil
					}
					found = true
					if mediaType, _, _ := part.Header.ContentType(); mediaType != tc.wantType {
						t.Errorf("content type = %q, want %q", mediaType, tc.wantType)
					}
					if got := part.Header.Get("Content-Transfer-Encoding"); got != tc.wantCTE {
						t.Errorf("transfer encoding = %q, want %q", got, tc.wantCTE)
					}
					body, err := io.ReadAll(part.Body)
					if err != nil {
						return err
					}
					if !bytes.Equal(body, tc.data) {
						t.Errorf("body = %q, want %q", body, tc.data)
					}
					return nil
				})
				if err != nil {
					t.Fatalf("Walk: %v", err)
				}
				if !found {
					t.Fatal("attached message missing")
				}
			})
		}
	}
}

func TestValidateDraftContentIDRules(t *testing.T) {
	inline := func(ids ...string) []DraftAttachment {
		var out []DraftAttachment
		for i, id := range ids {
			out = append(out, DraftAttachment{Filename: fmt.Sprintf("%d.png", i), ContentType: "image/png", ContentID: id})
		}
		return out
	}
	html := "<p>hi</p>"
	tests := []struct {
		name  string
		draft Draft
		want  string
	}{
		{name: "no html body", draft: Draft{From: "a@example.org", TextBody: "hi", Attachments: inline("a@x")}, want: "no HTML body"},
		{name: "angle brackets", draft: Draft{From: "a@example.org", HTMLBody: html, Attachments: inline("<a@x>")}, want: "not a valid Content-ID"},
		{name: "space", draft: Draft{From: "a@example.org", HTMLBody: html, Attachments: inline("a x")}, want: "not a valid Content-ID"},
		{name: "too long", draft: Draft{From: "a@example.org", HTMLBody: html, Attachments: inline(strings.Repeat("a", MaxMsgIDBytes+1))}, want: "not a valid Content-ID"},
		{name: "repeated", draft: Draft{From: "a@example.org", HTMLBody: html, Attachments: inline("logo@x", "logo@x")}, want: "repeats"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateDraft(tt.draft)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}

	// Gmail's identifiers have no "@"; refusing them would refuse its images.
	if _, err := validateDraft(Draft{From: "a@example.org", HTMLBody: html, Attachments: inline("ii_l4f8z0w70")}); err != nil {
		t.Errorf("Gmail-style content ID refused: %v", err)
	}
	// Identifiers are compared exactly, so ones differing only in case are distinct.
	if _, err := validateDraft(Draft{From: "a@example.org", HTMLBody: html, Attachments: inline("Logo@x", "logo@x")}); err != nil {
		t.Errorf("content IDs differing in case refused: %v", err)
	}
}

func TestBuildDraftMessageWritesThreadHeaders(t *testing.T) {
	draft := Draft{
		From:       "alice@example.org",
		To:         []string{"bob@example.org"},
		Subject:    "Re: plans",
		TextBody:   "reply body",
		InReplyTo:  []string{"<parent@example.org>"},
		References: []string{"root@example.org", " <parent@example.org> "},
	}

	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatalf("validateDraft: %v", err)
	}
	thread, err := draftThreadHeaders(draft)
	if err != nil {
		t.Fatalf("draftThreadHeaders: %v", err)
	}
	raw, err := buildDraftMessage(draft, addresses, thread, time.Now())
	if err != nil {
		t.Fatalf("buildDraftMessage: %v", err)
	}

	mr, err := messageMail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	inReplyTo, err := mr.Header.MsgIDList("In-Reply-To")
	if err != nil || !slices.Equal(inReplyTo, []string{"parent@example.org"}) {
		t.Errorf("In-Reply-To = %v, %v", inReplyTo, err)
	}
	references, err := mr.Header.MsgIDList("References")
	if err != nil || !slices.Equal(references, []string{"root@example.org", "parent@example.org"}) {
		t.Errorf("References = %v, %v", references, err)
	}
	// A reply gets its own identity; only replacement reuses a Message-ID.
	if messageID, err := mr.Header.MessageID(); err != nil || messageID == "" || messageID == "parent@example.org" {
		t.Errorf("Message-ID = %q, %v", messageID, err)
	}
}

func TestDraftThreadHeadersRejectsMalformedMsgIDs(t *testing.T) {
	tests := []struct {
		name  string
		draft Draft
		want  string
	}{
		{name: "empty", draft: Draft{InReplyTo: []string{"<>"}}, want: "in_reply_to message id at index 0 is empty"},
		{name: "header injection", draft: Draft{InReplyTo: []string{"a@example.org>\r\nBcc: eve@example.org"}}, want: "malformed"},
		{name: "space separated pair", draft: Draft{References: []string{"a@example.org b@example.org"}}, want: "references message id at index 0 is malformed"},
		{name: "too many references", draft: Draft{References: make([]string, MaxThreadReferences+1)}, want: "at most"},
		// Accepting these would save a draft that can never be read back or replaced.
		{name: "no domain", draft: Draft{InReplyTo: []string{"not-a-message-id"}}, want: "valid RFC 5322 message id"},
		{name: "empty domain", draft: Draft{References: []string{"foo@"}}, want: "valid RFC 5322 message id"},
		{name: "empty local part", draft: Draft{References: []string{"@example.org"}}, want: "valid RFC 5322 message id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := draftThreadHeaders(tt.draft)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestDraftThreadHeadersAcceptsRealWorldMsgIDs(t *testing.T) {
	ids := []string{
		"CAF=vf9abc123+xyz@mail.gmail.com",
		"20260718120000.GA1234$5@sub.domain.example.co.uk",
		"a_b-c.d'e@example.org",
		"<bracketed@example.org>",
	}
	headers, err := draftThreadHeaders(Draft{References: ids})
	if err != nil {
		t.Fatalf("draftThreadHeaders: %v", err)
	}
	want := append(slices.Clone(ids[:3]), "bracketed@example.org")
	if !slices.Equal(headers.references, want) {
		t.Errorf("references = %v, want %v", headers.references, want)
	}
}

func TestMaximumThreadHeadersSurviveSerialization(t *testing.T) {
	// Folding cannot break a single over-long token, so the accepted maximum has
	// to round-trip or a saved draft becomes unreadable and unreplaceable.
	ids := make([]string, 0, MaxThreadReferences)
	for i := 0; i < MaxThreadReferences; i++ {
		local := fmt.Sprintf("%0*d", MaxMsgIDBytes-len("@example.org"), i)
		ids = append(ids, local+"@example.org")
	}
	draft := Draft{From: "alice@example.org", TextBody: "body", InReplyTo: ids[:1], References: ids}

	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := draftThreadHeaders(draft)
	if err != nil {
		t.Fatalf("draftThreadHeaders rejected the documented maximum: %v", err)
	}
	raw, err := buildDraftMessage(draft, addresses, thread, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mr, err := messageMail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	references, err := mr.Header.MsgIDList("References")
	if err != nil || !slices.Equal(references, ids) {
		t.Errorf("References round-trip failed: %v (got %d of %d ids)", err, len(references), len(ids))
	}

	// The fetch cap must stay above what a save accepts, or reading the draft
	// back would truncate it and replacement would refuse to parse it.
	headerBytes := bytes.Index(raw, []byte("\r\n\r\n"))
	if headerBytes < 0 {
		t.Fatal("generated message has no header terminator")
	}
	if headerBytes > threadHeaderCap {
		t.Errorf("maximum identification headers are %d bytes, above the %d byte fetch cap", headerBytes, threadHeaderCap)
	}
}

func TestParseThreadHeadersTrimsUnboundedChain(t *testing.T) {
	var b strings.Builder
	b.WriteString("Message-ID: <last@example.org>\r\nReferences:")
	for i := 0; i < MaxThreadReferences+50; i++ {
		fmt.Fprintf(&b, " <%d@example.org>", i)
	}
	b.WriteString("\r\n\r\n")

	headers, err := parseThreadHeaders([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	// A reported chain must still accept the parent id a reply appends to it.
	reply := append(slices.Clone(headers.references), "last@example.org")
	if _, err := normalizeMsgIDs("references", reply); err != nil {
		t.Fatalf("a chain read from a long thread cannot be replied to: %v", err)
	}
	// The immediate ancestry is what threads a reply, so the tail must survive.
	if headers.references[len(headers.references)-1] != fmt.Sprintf("%d@example.org", MaxThreadReferences+49) {
		t.Errorf("trim dropped the most recent references: %v", headers.references[len(headers.references)-1])
	}
}

func TestParseThreadHeadersRejectsTruncatedBlock(t *testing.T) {
	var b strings.Builder
	b.WriteString("Message-ID: <last@example.org>\r\nReferences:")
	for b.Len() < threadHeaderCap {
		b.WriteString(" <padding0000000000000000@example.org>")
	}
	// The fetch returns at most the cap, cutting the chain mid-flight.
	truncated := b.String()[:threadHeaderCap]

	headers, err := parseThreadHeaders([]byte(truncated))
	if err == nil || !strings.Contains(err.Error(), "fetch limit") {
		t.Fatalf("error = %v, want the truncation refusal that keeps a replacement from rebuilding a partial chain", err)
	}
	if len(headers.references) != 0 {
		t.Errorf("references = %d, want none rather than a mid-chain prefix presented as recent ancestry", len(headers.references))
	}
}

func TestGeneratedMessageIDSurvivesNormalization(t *testing.T) {
	// A generated draft Message-ID becomes the parent id of the next reply.
	var header messageMail.Header
	if err := header.GenerateMessageIDWithHostname("baryon-mcp.local"); err != nil {
		t.Fatal(err)
	}
	generated, err := header.MessageID()
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeMsgIDs("in_reply_to", []string{generated})
	if err != nil || !slices.Equal(normalized, []string{generated}) {
		t.Fatalf("normalizeMsgIDs(%q) = %v, %v", generated, normalized, err)
	}
}

func TestBuildDraftMessageGeneratesMessageID(t *testing.T) {
	draft := Draft{From: "alice@example.org"}
	addresses, err := validateDraft(draft)
	if err != nil {
		t.Fatalf("validateDraft: %v", err)
	}
	raw, err := buildDraftMessage(draft, addresses, threadHeaders{}, time.Now())
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

	_, err = buildDraftMessage(draft, addresses, threadHeaders{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "invalid content_type") {
		t.Fatalf("error = %v, want invalid content_type", err)
	}
}

func TestDraftLimits(t *testing.T) {
	if MaxDraftAttachments != 100 {
		t.Errorf("MaxDraftAttachments = %d, want 100", MaxDraftAttachments)
	}
	if MaxDraftAttachmentBytes != 25_000_000 {
		t.Errorf("MaxDraftAttachmentBytes = %d, want 25000000", MaxDraftAttachmentBytes)
	}
	if MaxDraftAttachmentTotalBytes != 25_000_000 {
		t.Errorf("MaxDraftAttachmentTotalBytes = %d, want 25000000", MaxDraftAttachmentTotalBytes)
	}
	if MaxDraftMessageBytes != 70*1024*1024 {
		t.Errorf("MaxDraftMessageBytes = %d, want %d", MaxDraftMessageBytes, 70*1024*1024)
	}
}

func TestValidateDraftMessageSize(t *testing.T) {
	if err := validateDraftMessageSize(MaxDraftMessageBytes - 1); err != nil {
		t.Fatalf("message below limit rejected: %v", err)
	}
	if err := validateDraftMessageSize(MaxDraftMessageBytes); err == nil || !strings.Contains(err.Error(), "below") {
		t.Fatalf("error = %v, want strict generated-message limit", err)
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

// A forward carries the original body whole, so the character limit on what a
// caller writes must not refuse it here.
func TestValidateDraftAcceptsComposedBodyAboveCharLimit(t *testing.T) {
	body := strings.Repeat("x", MaxDraftBodyChars+1)
	if _, err := validateDraft(Draft{From: "a@example.org", TextBody: body, HTMLBody: body}); err != nil {
		t.Fatalf("validateDraft: %v", err)
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
