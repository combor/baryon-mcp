package bridgeclient

import (
	"context"
	"fmt"
	"mime"
	"slices"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/combor/baryon-mcp/internal/mailparse"
)

const (
	// textPartCap covers the worst-case quoted-printable encoding of a
	// MaxDraftBodyChars UTF-8 body while still bounding transferred bytes.
	textPartCap = 768 * 1024
	// attachmentCap covers base64 and line-wrapping overhead for one maximum
	// decoded draft attachment. The decoded-size check below remains authoritative.
	attachmentCap = 3 * MaxDraftAttachmentBytes / 2
	// bodyCharCap bounds decoded body text length in characters.
	bodyCharCap = MaxDraftBodyChars
	// sourceTextPartCap bounds one body part a draft carries whole. Mail
	// clients clip far below it, and two bodies this size plus a full set of
	// attachments still fit MaxDraftMessageBytes after re-encoding.
	sourceTextPartCap = 2 << 20
	// sourceAttachmentCap bounds the encoded size of every part a draft
	// carries together, allowing for base64 overhead on the decoded total.
	sourceAttachmentCap = 3 * MaxDraftAttachmentTotalBytes / 2
)

// TextBody is one decoded body part. DecodeFallback reports a transfer
// encoding that did not decode cleanly, leaving Text raw or cut at the damage.
type TextBody struct {
	Text            string
	Truncated       bool
	CharsetFallback bool
	DecodeFallback  bool
}

// AttachmentInfo is envelope-level attachment metadata.
type AttachmentInfo struct {
	Index       int
	Filename    string
	ContentType string
	EncodedSize uint32
}

// EmailContent is a full single-message view. MessageID, InReplyTo, and
// References carry the identifiers a reply draft must quote to stay in the
// conversation; identifiers are bare, with no angle brackets.
type EmailContent struct {
	Summary     EmailSummary
	MessageID   string
	InReplyTo   []string
	References  []string
	Plain       *TextBody
	HTML        *TextBody
	Attachments []AttachmentInfo
}

// AttachmentContent is one decoded attachment payload.
type AttachmentContent struct {
	Filename    string
	ContentType string
	EncodedSize uint32
	Data        []byte
}

// withMessage runs fn in a session with folder selected read-only and the
// caller's uidvalidity verified against the live one. A mailbox outside the
// configured scope is refused before anything is opened.
func (c *Client) withMessage(ctx context.Context, folder string, uidvalidity uint32, fn func(cli *imapclient.Client) error) error {
	if err := c.policy.check(folder); err != nil {
		return err
	}
	return c.withSession(ctx, func(cli *imapclient.Client) error {
		sel, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return fmt.Errorf("selecting folder %q: %w", folder, err)
		}
		if sel.UIDValidity != uidvalidity {
			return fmt.Errorf("folder %q UIDVALIDITY changed (now %d, expected %d): message UIDs are stale, re-run list_emails or search_emails", folder, sel.UIDValidity, uidvalidity)
		}
		return fn(cli)
	})
}

// fetchOutline retrieves envelope, flags, and the walked body structure.
func fetchOutline(cli *imapclient.Client, uid uint32) (*imapclient.FetchMessageBuffer, mailparse.Outline, error) {
	msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		Envelope:      true,
		Flags:         true,
		UID:           true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, mailparse.Outline{}, fmt.Errorf("fetching message structure: %w", err)
	}
	if len(msgs) == 0 {
		return nil, mailparse.Outline{}, fmt.Errorf("message with uid %d not found in folder", uid)
	}
	if msgs[0].BodyStructure == nil {
		return nil, mailparse.Outline{}, fmt.Errorf("bridge returned no body structure for uid %d", uid)
	}
	return msgs[0], mailparse.Walk(msgs[0].BodyStructure), nil
}

// GetEmail returns the message's envelope, decoded text bodies, and
// attachment metadata.
func (c *Client) GetEmail(ctx context.Context, folder string, uid, uidvalidity uint32) (*EmailContent, error) {
	var content *EmailContent
	err := c.withMessage(ctx, folder, uidvalidity, func(cli *imapclient.Client) error {
		msg, outline, err := fetchOutline(cli, uid)
		if err != nil {
			return err
		}
		content, err = readContent(cli, uid, msg, outline, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return content, nil
}

// readContent fetches the identification headers and text bodies of a
// message whose outline is already known. A read for the conversation caps
// each body; a whole read is for a draft to carry, so it refuses a body above
// sourceTextPartCap rather than truncate it.
func readContent(cli *imapclient.Client, uid uint32, msg *imapclient.FetchMessageBuffer, outline mailparse.Outline, whole bool) (*EmailContent, error) {
	content := &EmailContent{Summary: summarize(msg)}
	for _, a := range outline.Attachments {
		content.Attachments = append(content.Attachments, AttachmentInfo{
			Index:       a.Index,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			EncodedSize: a.EncodedSize,
		})
	}

	threadSection := threadHeaderSection()
	sections := []*imap.FetchItemBodySection{threadSection}
	for _, tp := range []*mailparse.TextPart{outline.Plain, outline.HTML} {
		if tp == nil {
			continue
		}
		section := &imap.FetchItemBodySection{Part: tp.Path, Peek: true}
		if !whole {
			section.Partial = &imap.SectionPartial{Offset: 0, Size: textPartCap}
		} else if tp.EncodedSize > sourceTextPartCap {
			return nil, fmt.Errorf("the message body is %d bytes encoded, above the %d byte limit for carrying it into a draft", tp.EncodedSize, sourceTextPartCap)
		}
		sections = append(sections, section)
	}

	msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID:         true,
		BodySection: sections,
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetching message body: %w", err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("message with uid %d disappeared while fetching its body", uid)
	}

	// A malformed identification header must not make a message unreadable.
	thread, _ := parseThreadHeaders(msgs[0].FindBodySection(threadSection))
	thread.fillFromEnvelope(msg.Envelope)
	content.MessageID = thread.messageID
	content.InReplyTo = thread.inReplyTo
	content.References = thread.references

	decode := func(tp *mailparse.TextPart) (*TextBody, error) {
		raw, ok := findSection(msgs[0], tp.Path)
		if !ok {
			if whole {
				return nil, fmt.Errorf("bridge returned no data for body part %v", tp.Path)
			}
			return nil, nil
		}
		preTruncated, maxChars := tp.EncodedSize > textPartCap, bodyCharCap
		if whole {
			preTruncated, maxChars = false, 0
		}
		res := mailparse.DecodeText(raw, tp.Encoding, tp.Charset, preTruncated, maxChars)
		return &TextBody{Text: res.Text, Truncated: res.Truncated, CharsetFallback: res.CharsetFallback, DecodeFallback: res.DecodeFallback}, nil
	}
	if outline.Plain != nil {
		if content.Plain, err = decode(outline.Plain); err != nil {
			return nil, err
		}
	}
	if outline.HTML != nil {
		if content.HTML, err = decode(outline.HTML); err != nil {
			return nil, err
		}
	}
	return content, nil
}

// SourceParts selects which of a message's non-body parts GetMessageSource
// carries.
type SourceParts int

const (
	// ReferencedInlineParts carries only the parts the HTML body shows
	// through cid: URLs, which quoting that HTML needs to render.
	ReferencedInlineParts SourceParts = iota
	// AllParts carries every attachment, as a forward does.
	AllParts
)

// MessageSource is a message read whole for a draft to carry: its bodies are
// never truncated, and Attachments hold decoded bytes ready for SaveDraft.
// Only a part the HTML body references keeps a ContentID. Damaged names the
// attachments whose transfer encoding did not decode cleanly; each carries
// the bytes that decoded before the damage.
type MessageSource struct {
	Content     EmailContent
	Attachments []DraftAttachment
	Damaged     []string
}

// GetMessageSource reads a message for a draft to carry, in one session.
// Anything a draft could not hold is refused, never trimmed: a body above
// sourceTextPartCap before the bodies are fetched, and more parts than
// MaxDraftAttachments or more encoded bytes than sourceAttachmentCap before
// the parts are. A part above the decoded attachment limit shows only once
// decoded, and is refused when the draft is saved.
func (c *Client) GetMessageSource(ctx context.Context, folder string, uid, uidvalidity uint32, parts SourceParts) (*MessageSource, error) {
	var source *MessageSource
	err := c.withMessage(ctx, folder, uidvalidity, func(cli *imapclient.Client) error {
		msg, outline, err := fetchOutline(cli, uid)
		if err != nil {
			return err
		}
		content, err := readContent(cli, uid, msg, outline, true)
		if err != nil {
			return err
		}
		var html string
		if content.HTML != nil {
			html = content.HTML.Text
		}
		chosen := chooseSourceParts(outline.Attachments, mailparse.ContentIDReferences(html), parts)
		if len(chosen) > MaxDraftAttachments {
			return fmt.Errorf("the message has %d attachments, above the %d a draft may carry", len(chosen), MaxDraftAttachments)
		}
		var encoded uint64
		for _, part := range chosen {
			encoded += uint64(part.EncodedSize)
		}
		if encoded > sourceAttachmentCap {
			return fmt.Errorf("the message's attachments are %d bytes encoded, above the %d byte limit a draft may carry", encoded, sourceAttachmentCap)
		}
		attachments, damaged, err := fetchSourceParts(cli, uid, chosen)
		if err != nil {
			return err
		}
		source = &MessageSource{Content: *content, Attachments: attachments, Damaged: damaged}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return source, nil
}

// sourcePart is a part chosen to travel with the Content-ID it keeps.
type sourcePart struct {
	mailparse.Attachment
	inlineID string
}

// chooseSourceParts picks the parts a draft carries. A part stays inline only
// when the HTML body references it and its identifier is valid and not taken
// by an earlier part; anything else travels as a regular attachment when every
// part is wanted, and is left behind otherwise. A messy original therefore
// never makes the draft invalid. References match regardless of case, as
// lenient mail clients resolve them, while identifiers stay distinct exactly.
func chooseSourceParts(attachments []mailparse.Attachment, referenced map[string]bool, parts SourceParts) []sourcePart {
	seen := make(map[string]bool)
	var chosen []sourcePart
	for _, a := range attachments {
		if a.ContentID != "" && referenced[strings.ToLower(a.ContentID)] && validContentID(a.ContentID) && !seen[a.ContentID] {
			seen[a.ContentID] = true
			chosen = append(chosen, sourcePart{Attachment: a, inlineID: a.ContentID})
		} else if parts == AllParts {
			chosen = append(chosen, sourcePart{Attachment: a})
		}
	}
	return chosen
}

// fetchSourceParts fetches and decodes the chosen parts in one FETCH. A part
// whose transfer encoding is damaged still travels, with what decoded, and is
// named in the returned list rather than failing the whole read.
func fetchSourceParts(cli *imapclient.Client, uid uint32, chosen []sourcePart) ([]DraftAttachment, []string, error) {
	if len(chosen) == 0 {
		return nil, nil, nil
	}
	sections := make([]*imap.FetchItemBodySection, 0, len(chosen))
	for _, part := range chosen {
		sections = append(sections, &imap.FetchItemBodySection{Part: part.Path, Peek: true})
	}
	msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID:         true,
		BodySection: sections,
	}).Collect()
	if err != nil {
		return nil, nil, fmt.Errorf("fetching attachments: %w", err)
	}
	if len(msgs) == 0 {
		return nil, nil, fmt.Errorf("message with uid %d disappeared while fetching its attachments", uid)
	}
	attachments := make([]DraftAttachment, 0, len(chosen))
	var damaged []string
	for _, part := range chosen {
		raw, ok := findSection(msgs[0], part.Path)
		if !ok {
			return nil, nil, fmt.Errorf("bridge returned no data for attachment part %v", part.Path)
		}
		filename := carriedFilename(part.Filename)
		data, broken := mailparse.DecodeBinaryLenient(raw, part.Encoding)
		if broken {
			damaged = append(damaged, filename)
		}
		attachments = append(attachments, DraftAttachment{
			Filename:    filename,
			ContentType: carriedContentType(part.ContentType, part.Params),
			ContentID:   part.inlineID,
			Data:        data,
		})
	}
	return attachments, damaged, nil
}

// carriedFilename keeps a sender's filename where a draft can hold it: control
// characters that would break the header become underscores.
func carriedFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
	if strings.TrimSpace(name) == "" {
		return "attachment"
	}
	return name
}

// carriedContentType keeps a part's media type and parameters, such as a
// calendar invitation's method, falling back to application/octet-stream for a
// type a draft cannot write. A charset other than UTF-8 or US-ASCII stays
// behind, because the draft writer refuses to label a part with one; the
// bytes travel unchanged, so a recipient's client has to guess how to show
// such a text file.
func carriedContentType(mediaType string, params map[string]string) string {
	if parsed, _, err := mime.ParseMediaType(mediaType); err != nil || !strings.Contains(parsed, "/") {
		return "application/octet-stream"
	}
	kept := make(map[string]string, len(params))
	for name, value := range params {
		name = strings.ToLower(name)
		if name == "charset" && !writableCharset(value) {
			continue
		}
		kept[name] = value
	}
	if contentType := mime.FormatMediaType(mediaType, kept); contentType != "" {
		return contentType
	}
	return mediaType
}

// writableCharset reports whether the draft writer accepts charset as a label.
func writableCharset(charset string) bool {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "us-ascii":
		return true
	}
	return false
}

// ListAttachments returns attachment metadata from BODYSTRUCTURE only — no
// content is transferred.
func (c *Client) ListAttachments(ctx context.Context, folder string, uid, uidvalidity uint32) ([]AttachmentInfo, error) {
	var infos []AttachmentInfo
	err := c.withMessage(ctx, folder, uidvalidity, func(cli *imapclient.Client) error {
		_, outline, err := fetchOutline(cli, uid)
		if err != nil {
			return err
		}
		infos = make([]AttachmentInfo, 0, len(outline.Attachments))
		for _, a := range outline.Attachments {
			infos = append(infos, AttachmentInfo{
				Index:       a.Index,
				Filename:    a.Filename,
				ContentType: a.ContentType,
				EncodedSize: a.EncodedSize,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return infos, nil
}

// GetAttachment fetches and decodes one attachment, bounding both encoded
// transfer size and decoded output size.
func (c *Client) GetAttachment(ctx context.Context, folder string, uid, uidvalidity uint32, index int) (*AttachmentContent, error) {
	var content *AttachmentContent
	err := c.withMessage(ctx, folder, uidvalidity, func(cli *imapclient.Client) error {
		_, outline, err := fetchOutline(cli, uid)
		if err != nil {
			return err
		}
		if index < 0 || index >= len(outline.Attachments) {
			return fmt.Errorf("attachment index %d out of range: message has %d attachments (use list_attachments)", index, len(outline.Attachments))
		}
		att := outline.Attachments[index]
		if att.EncodedSize > attachmentCap {
			return fmt.Errorf("attachment %q is %d bytes encoded, above the %d byte limit for inline retrieval", att.Filename, att.EncodedSize, attachmentCap)
		}

		section := &imap.FetchItemBodySection{Part: att.Path, Peek: true}
		msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{section},
		}).Collect()
		if err != nil {
			return fmt.Errorf("fetching attachment: %w", err)
		}
		if len(msgs) == 0 {
			return fmt.Errorf("message with uid %d disappeared while fetching the attachment", uid)
		}
		raw, ok := findSection(msgs[0], att.Path)
		if !ok {
			return fmt.Errorf("bridge returned no data for attachment part %v", att.Path)
		}
		data, err := mailparse.DecodeBinary(raw, att.Encoding)
		if err != nil {
			return err
		}
		if len(data) > MaxDraftAttachmentBytes {
			return fmt.Errorf("attachment %q is %d bytes decoded, above the %d byte limit for inline retrieval", att.Filename, len(data), MaxDraftAttachmentBytes)
		}
		content = &AttachmentContent{
			Filename:    att.Filename,
			ContentType: att.ContentType,
			EncodedSize: att.EncodedSize,
			Data:        data,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return content, nil
}

func findSection(msg *imapclient.FetchMessageBuffer, path []int) ([]byte, bool) {
	for _, s := range msg.BodySection {
		if s.Section != nil && slices.Equal(s.Section.Part, path) {
			return s.Bytes, true
		}
	}
	return nil, false
}
