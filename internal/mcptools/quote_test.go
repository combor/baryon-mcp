package mcptools

import (
	"strings"
	"testing"
	"time"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func originalEmail(plain, html string) *bridgeclient.EmailContent {
	email := &bridgeclient.EmailContent{
		Summary: bridgeclient.EmailSummary{
			Subject: "Q3 invoice",
			From:    []string{"Acme & Co <billing@acme.example>"},
			To:      []string{"me@proton.me"},
			Date:    time.Date(2026, 10, 12, 9, 14, 0, 0, time.FixedZone("", 3600)),
		},
	}
	if plain != "" {
		email.Plain = &bridgeclient.TextBody{Text: plain}
	}
	if html != "" {
		email.HTML = &bridgeclient.TextBody{Text: html}
	}
	return email
}

func TestPrefixSubject(t *testing.T) {
	cases := []struct{ in, forward, reply string }{
		{in: "Q3 invoice", forward: "Fwd: Q3 invoice", reply: "Re: Q3 invoice"},
		{in: "  FW: Q3 invoice ", forward: "FW: Q3 invoice", reply: "Re: FW: Q3 invoice"},
		{in: "fwd: Q3 invoice", forward: "fwd: Q3 invoice", reply: "Re: fwd: Q3 invoice"},
		{in: "RE: Q3 invoice", forward: "Fwd: RE: Q3 invoice", reply: "RE: Q3 invoice"},
		{in: "", forward: "Fwd:", reply: "Re:"},
	}
	for _, tc := range cases {
		if got := forwardSubject(tc.in); got != tc.forward {
			t.Errorf("forwardSubject(%q) = %q, want %q", tc.in, got, tc.forward)
		}
		if got := replySubject(tc.in); got != tc.reply {
			t.Errorf("replySubject(%q) = %q, want %q", tc.in, got, tc.reply)
		}
	}
}

func TestForwardBodiesPlainOnlyOriginal(t *testing.T) {
	plain, html := forwardBodies(originalEmail("line one\r\nline two\r\n", ""), "Bob, see below.", "")
	if html != "" {
		t.Errorf("html = %q, want none for a plain original and note", html)
	}
	want := "Bob, see below.\n\n" +
		"---------- Forwarded message ----------\n" +
		"From: Acme & Co <billing@acme.example>\n" +
		"Date: Mon, 12 Oct 2026 09:14:00 +0100\n" +
		"Subject: Q3 invoice\n" +
		"To: me@proton.me\n\n" +
		"line one\nline two\n"
	if plain != want {
		t.Errorf("plain =\n%q\nwant\n%q", plain, want)
	}
}

func TestForwardBodiesCopiesOriginalHTML(t *testing.T) {
	originalHTML := `<html><head><style>p{color:red}</style></head><body><p>Hi <img src="cid:logo"></p></body></html>`
	plain, html := forwardBodies(originalEmail("", originalHTML), "Bob,\nsee <below>.", "")

	if !strings.HasPrefix(html, "<div>Bob,<br>see &lt;below&gt;.</div><br><br>") {
		t.Errorf("html should open with the escaped note: %q", html)
	}
	if !strings.Contains(html, "From: Acme &amp; Co &lt;billing@acme.example&gt;<br>") {
		t.Errorf("header values should be escaped: %q", html)
	}
	if !strings.HasSuffix(html, "</div><br>"+originalHTML) {
		t.Errorf("html should end with the original exactly: %q", html)
	}
	if !strings.HasSuffix(plain, "To: me@proton.me\n\nHi") {
		t.Errorf("plain should end with the original's text: %q", plain)
	}
}

func TestForwardBodiesUsesCallerHTML(t *testing.T) {
	plain, html := forwardBodies(originalEmail("original", ""), "", "<p>See <b>below</b></p>")
	if !strings.HasPrefix(plain, "See below\n\n"+forwardSeparator) {
		t.Errorf("plain should open with the caller's HTML as text: %q", plain)
	}
	if !strings.HasPrefix(html, "<p>See <b>below</b></p><br><br>") ||
		!strings.HasSuffix(html, `<div style="white-space: pre-wrap">original</div>`) {
		t.Errorf("html = %q", html)
	}
}

func TestForwardBodiesCarriesLongOriginalWhole(t *testing.T) {
	long := strings.Repeat("0123456789", 20_000)
	plain, html := forwardBodies(originalEmail(long, "<p>"+long+"</p>"), "", "")
	if !strings.HasSuffix(plain, long) {
		t.Errorf("plain lost the end of a %d character original", len(long))
	}
	if !strings.HasSuffix(html, "<p>"+long+"</p>") {
		t.Errorf("html lost the end of a %d character original", len(long))
	}
	if !strings.HasPrefix(plain, forwardSeparator) {
		t.Errorf("a forward without a note should open with its header block: %q", plain[:60])
	}
}

func TestQuotedReplyBodiesPlain(t *testing.T) {
	plain, html := quotedReplyBodies(originalEmail("Hello,\r\n\r\nPlease pay.\r\n", ""), "Paid today.", "")
	if html != "" {
		t.Errorf("html = %q, want none", html)
	}
	want := "Paid today.\n\n" +
		"On Mon, 12 Oct 2026 09:14:00 +0100, Acme & Co <billing@acme.example> wrote:\n" +
		"> Hello,\n>\n> Please pay."
	if plain != want {
		t.Errorf("plain =\n%q\nwant\n%q", plain, want)
	}
}

func TestQuotedReplyBodiesHTML(t *testing.T) {
	_, html := quotedReplyBodies(originalEmail("", "<p>Please pay.</p>"), "Paid today.", "")
	want := "<div>Paid today.</div><br><br>" +
		"<div>On Mon, 12 Oct 2026 09:14:00 +0100, Acme &amp; Co &lt;billing@acme.example&gt; wrote:</div>" +
		`<blockquote type="cite"><p>Please pay.</p></blockquote>`
	if html != want {
		t.Errorf("html =\n%q\nwant\n%q", html, want)
	}

	_, html = quotedReplyBodies(originalEmail("a < b", ""), "", "<p>ok</p>")
	if !strings.HasSuffix(html, `<blockquote type="cite"><div style="white-space: pre-wrap">a &lt; b</div></blockquote>`) {
		t.Errorf("a plain original should be quoted escaped: %q", html)
	}
}

func TestQuoteAttributionWithoutDateOrSender(t *testing.T) {
	if got := quoteAttribution(bridgeclient.EmailSummary{}); got != "the sender wrote:" {
		t.Errorf("attribution = %q", got)
	}
}

func TestCarriedWarning(t *testing.T) {
	source := &bridgeclient.MessageSource{Content: *originalEmail("text", "<p>text</p>")}
	if got := carriedWarning(source); got != "" {
		t.Errorf("warning = %q, want none", got)
	}
	source.Content.HTML.DecodeFallback = true
	if got := carriedWarning(source); got != decodeWarning {
		t.Errorf("warning = %q, want the decode warning", got)
	}
	source.Content.HTML.DecodeFallback = false
	source.Content.Plain.CharsetFallback = true
	if got := carriedWarning(source); got != decodeWarning {
		t.Errorf("warning = %q, want the decode warning for a charset fallback", got)
	}
	source.Content.Plain.CharsetFallback = false
	source.Damaged = []string{"a.pdf", "b.png"}
	if got := carriedWarning(source); !strings.HasSuffix(got, "only what was readable: a.pdf, b.png") {
		t.Errorf("warning = %q, want the damaged attachments named", got)
	}
}

// A line break decoded from an encoded word must not forge a header line
// inside the carried block.
func TestForwardBodiesKeepHeaderValuesOnOneLine(t *testing.T) {
	original := originalEmail("body", "")
	original.Summary.Subject = "Invoice\nFrom: ceo@corp.example"
	original.Summary.From = []string{"Mallory\r\nTo: everyone <m@example.org>"}
	plain, _ := forwardBodies(original, "", "")
	for _, line := range strings.Split(plain, "\n") {
		if strings.HasPrefix(line, "From: ceo@") || strings.HasPrefix(line, "To: everyone") {
			t.Errorf("a header value opened its own line %q in:\n%s", line, plain)
		}
	}
	if !strings.Contains(plain, "Subject: Invoice From: ceo@corp.example\n") {
		t.Errorf("subject should stay on its line: %q", plain)
	}
	if got := quoteAttribution(original.Summary); strings.ContainsAny(got, "\r\n") {
		t.Errorf("attribution = %q, want a single line", got)
	}
}

func TestOriginalPlainFallsBackFromABlankPlainPart(t *testing.T) {
	plain, _ := forwardBodies(originalEmail(" \r\n", "<p>The real content</p>"), "", "")
	if !strings.HasSuffix(plain, "\n\nThe real content") {
		t.Errorf("plain = %q, want the HTML's text in place of the blank plain part", plain)
	}
}
