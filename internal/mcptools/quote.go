package mcptools

// Carrying an original into a draft: the header block of a forward, the
// attribution and quoting of a reply, and the HTML both are written in. The
// original is copied whole; only its line endings are normalised in plain text.

import (
	"html"
	"strings"
	"time"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/mailparse"
)

const forwardSeparator = "---------- Forwarded message ----------"

// decodeWarning is reported when the original's text did not decode exactly,
// so the carried copy may differ from what its sender wrote.
const decodeWarning = "part of the original's text could not be decoded exactly; the draft may hold replacement characters or undecoded text"

// forwardBodies composes a forward: the caller's note, a header block naming
// the original, then the original itself. An HTML body is written when the
// caller or the original has one; otherwise the forward is plain text only.
func forwardBodies(original *bridgeclient.EmailContent, text, htmlBody string) (string, string) {
	summary := original.Summary
	fields := []struct{ name, value string }{
		{"From", headerText(strings.Join(summary.From, ", "))},
		{"Date", formatQuoteDate(summary.Date)},
		{"Subject", headerText(summary.Subject)},
		{"To", headerText(strings.Join(summary.To, ", "))},
		{"Cc", headerText(strings.Join(summary.Cc, ", "))},
	}
	plainHeader := []string{forwardSeparator}
	htmlHeader := []string{forwardSeparator}
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		plainHeader = append(plainHeader, field.name+": "+field.value)
		htmlHeader = append(htmlHeader, field.name+": "+html.EscapeString(field.value))
	}

	plain := joinNonEmpty("\n\n", callerPlain(text, htmlBody), strings.Join(plainHeader, "\n")+"\n\n"+originalPlain(original))
	if htmlBody == "" && original.HTML == nil {
		return plain, ""
	}
	forwarded := "<div>" + strings.Join(htmlHeader, "<br>") + "</div><br>" + originalHTML(original)
	return plain, joinNonEmpty("<br><br>", callerHTML(text, htmlBody), forwarded)
}

// quotedReplyBodies composes a reply that quotes its original under an
// attribution line: "> " prefixes in plain text, a cite blockquote in HTML.
func quotedReplyBodies(original *bridgeclient.EmailContent, text, htmlBody string) (string, string) {
	attribution := quoteAttribution(original.Summary)
	plain := joinNonEmpty("\n\n", callerPlain(text, htmlBody), attribution+"\n"+quoteLines(originalPlain(original)))
	if htmlBody == "" && original.HTML == nil {
		return plain, ""
	}
	quoted := "<div>" + html.EscapeString(attribution) + `</div><blockquote type="cite">` + originalHTML(original) + "</blockquote>"
	return plain, joinNonEmpty("<br><br>", callerHTML(text, htmlBody), quoted)
}

// carriedWarning reports where the carried copy may differ from the original:
// a body that did not decode exactly, and attachments whose encoding was
// damaged, which carry only what was readable.
func carriedWarning(source *bridgeclient.MessageSource) string {
	var warnings []string
	for _, body := range []*bridgeclient.TextBody{source.Content.Plain, source.Content.HTML} {
		if body != nil && (body.CharsetFallback || body.DecodeFallback) {
			warnings = append(warnings, decodeWarning)
			break
		}
	}
	if len(source.Damaged) > 0 {
		warnings = append(warnings, "these attachments did not decode cleanly and carry only what was readable: "+strings.Join(source.Damaged, ", "))
	}
	return strings.Join(warnings, "; ")
}

// headerText keeps a sender-written header value on one line. A line break
// decoded from an encoded word would otherwise open a line of its own in the
// carried header block, where it could pass for another field.
func headerText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '\u0085' || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s)
}

func quoteAttribution(summary bridgeclient.EmailSummary) string {
	sender := headerText(strings.Join(summary.From, ", "))
	if sender == "" {
		sender = "the sender"
	}
	if date := formatQuoteDate(summary.Date); date != "" {
		return "On " + date + ", " + sender + " wrote:"
	}
	return sender + " wrote:"
}

func formatQuoteDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC1123Z)
}

// quoteLines prefixes every line with "> ", and an empty line with ">" alone.
func quoteLines(s string) string {
	lines := strings.Split(strings.TrimRight(normalizeNewlines(s), "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

// callerPlain is the caller's text, or the text of their HTML when that is
// all they wrote.
func callerPlain(text, htmlBody string) string {
	if text == "" && htmlBody != "" {
		return mailparse.HTMLToText(htmlBody)
	}
	return text
}

// callerHTML is the caller's HTML, or their text escaped with its line breaks
// kept.
func callerHTML(text, htmlBody string) string {
	if htmlBody != "" || text == "" {
		return htmlBody
	}
	return "<div>" + strings.ReplaceAll(html.EscapeString(normalizeNewlines(text)), "\n", "<br>") + "</div>"
}

// originalPlain is the original's plain text, or the text of its HTML when
// the plain part is missing or blank, as some senders leave it.
func originalPlain(original *bridgeclient.EmailContent) string {
	plainBlank := original.Plain == nil || strings.TrimSpace(original.Plain.Text) == ""
	switch {
	case plainBlank && original.HTML != nil:
		return mailparse.HTMLToText(original.HTML.Text)
	case original.Plain != nil:
		return normalizeNewlines(original.Plain.Text)
	}
	return ""
}

// originalHTML is the original's HTML as it was sent, or its plain text
// escaped with whitespace preserved.
func originalHTML(original *bridgeclient.EmailContent) string {
	switch {
	case original.HTML != nil:
		return original.HTML.Text
	case original.Plain != nil:
		return `<div style="white-space: pre-wrap">` + html.EscapeString(original.Plain.Text) + "</div>"
	}
	return ""
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}
