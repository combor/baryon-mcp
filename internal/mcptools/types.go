package mcptools

import (
	"time"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	// maxPreviewLimit bounds a page that carries bodies, which are far larger
	// than the envelopes maxLimit was sized for.
	maxPreviewLimit = 10
)

func clampLimit(l int) int {
	switch {
	case l <= 0:
		return defaultLimit
	case l > maxLimit:
		return maxLimit
	}
	return l
}

func clampOffset(o int) int {
	if o < 0 {
		return 0
	}
	return o
}

type emailSummary struct {
	UID           uint32   `json:"uid" jsonschema:"message UID within the folder"`
	Subject       string   `json:"subject"`
	From          []string `json:"from,omitempty"`
	To            []string `json:"to,omitempty"`
	Date          string   `json:"date,omitempty" jsonschema:"send date, RFC 3339"`
	MessageID     string   `json:"message_id,omitempty" jsonschema:"RFC 5322 Message-ID without angle brackets, for correlating this message across folders"`
	Seen          bool     `json:"seen"`
	Flagged       bool     `json:"flagged,omitempty"`
	Answered      bool     `json:"answered,omitempty"`
	Body          string   `json:"body,omitempty" jsonschema:"present only when include_bodies was set"`
	BodyFromHTML  bool     `json:"body_from_html,omitempty" jsonschema:"the text was taken from the message's HTML part, with markup stripped, because it carries no plain text part"`
	BodyTruncated bool     `json:"body_truncated,omitempty" jsonschema:"body was cut short; use get_email for the whole message"`
}

func toEmailSummaries(in []bridgeclient.EmailSummary) []emailSummary {
	out := make([]emailSummary, 0, len(in))
	for _, e := range in {
		s := emailSummary{
			UID:           e.UID,
			Subject:       e.Subject,
			From:          e.From,
			To:            e.To,
			MessageID:     e.MessageID,
			Seen:          e.Seen,
			Flagged:       e.Flagged,
			Answered:      e.Answered,
			Body:          e.Body,
			BodyFromHTML:  e.BodyFromHTML,
			BodyTruncated: e.BodyTruncated,
		}
		if !e.Date.IsZero() {
			s.Date = e.Date.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out
}
