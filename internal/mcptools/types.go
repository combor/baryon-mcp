package mcptools

import (
	"time"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

const (
	defaultLimit = 20
	maxLimit     = 100
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
	UID      uint32   `json:"uid" jsonschema:"message UID within the folder"`
	Subject  string   `json:"subject"`
	From     []string `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	Date     string   `json:"date,omitempty" jsonschema:"send date, RFC 3339"`
	Seen     bool     `json:"seen"`
	Flagged  bool     `json:"flagged,omitempty"`
	Answered bool     `json:"answered,omitempty"`
}

func toEmailSummaries(in []bridgeclient.EmailSummary) []emailSummary {
	out := make([]emailSummary, 0, len(in))
	for _, e := range in {
		s := emailSummary{
			UID:      e.UID,
			Subject:  e.Subject,
			From:     e.From,
			To:       e.To,
			Seen:     e.Seen,
			Flagged:  e.Flagged,
			Answered: e.Answered,
		}
		if !e.Date.IsZero() {
			s.Date = e.Date.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out
}
