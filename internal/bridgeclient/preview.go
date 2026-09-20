package bridgeclient

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/combor/baryon-mcp/internal/mailparse"
)

const (
	// previewBodyCharCap bounds each shortened body. It sits far below
	// bodyCharCap because a caller multiplies it by a message count: a whole
	// conversation, or a whole page, should cost about what one get_email does.
	previewBodyCharCap = 2_000
	// previewTextPartCap covers the worst-case encoding of previewBodyCharCap
	// characters, in the same spirit as textPartCap.
	previewTextPartCap = 64 * 1024
)

// fillBodyPreview fetches one shortened body, preferring plain text and falling
// back to the HTML part reduced to text. Each message needs its own fetch
// because the part path comes from its own structure.
func fillBodyPreview(cli *imapclient.Client, s *EmailSummary, outline mailparse.Outline) error {
	part, isHTML := outline.Plain, false
	if part == nil {
		part, isHTML = outline.HTML, true
	}
	if part == nil {
		return nil
	}

	section := &imap.FetchItemBodySection{
		Part:    part.Path,
		Peek:    true,
		Partial: &imap.SectionPartial{Offset: 0, Size: previewTextPartCap},
	}
	msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(s.UID)), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return fmt.Errorf("fetching body of uid %d: %w", s.UID, err)
	}
	if len(msgs) == 0 {
		// Expunged between search and fetch; the summary still stands.
		return nil
	}
	raw, ok := findSection(msgs[0], part.Path)
	if !ok {
		return nil
	}
	// Shortening happens after stripping, so the budget buys readable text
	// rather than markup.
	res := mailparse.DecodeText(raw, part.Encoding, part.Charset, part.EncodedSize > previewTextPartCap, 0)
	text, truncated := res.Text, res.Truncated
	if isHTML {
		text = mailparse.HTMLToText(text)
	}
	if runes := []rune(text); len(runes) > previewBodyCharCap {
		text, truncated = string(runes[:previewBodyCharCap]), true
	}
	s.Body = text
	s.BodyFromHTML = isHTML
	s.BodyTruncated = truncated
	return nil
}
