package bridgeclient

import (
	"context"
	"fmt"
	"slices"

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
)

// TextBody is one decoded body part.
type TextBody struct {
	Text            string
	Truncated       bool
	CharsetFallback bool
}

// AttachmentInfo is envelope-level attachment metadata.
type AttachmentInfo struct {
	Index       int
	Filename    string
	ContentType string
	EncodedSize uint32
}

// EmailContent is a full single-message view.
type EmailContent struct {
	Summary     EmailSummary
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
// caller's uidvalidity verified against the live one.
func (c *Client) withMessage(ctx context.Context, folder string, uidvalidity uint32, fn func(cli *imapclient.Client) error) error {
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
		content = &EmailContent{Summary: summarize(msg)}
		for _, a := range outline.Attachments {
			content.Attachments = append(content.Attachments, AttachmentInfo{
				Index:       a.Index,
				Filename:    a.Filename,
				ContentType: a.ContentType,
				EncodedSize: a.EncodedSize,
			})
		}

		var sections []*imap.FetchItemBodySection
		for _, tp := range []*mailparse.TextPart{outline.Plain, outline.HTML} {
			if tp == nil {
				continue
			}
			sections = append(sections, &imap.FetchItemBodySection{
				Part:    tp.Path,
				Peek:    true,
				Partial: &imap.SectionPartial{Offset: 0, Size: textPartCap},
			})
		}
		if len(sections) == 0 {
			return nil
		}

		msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID:         true,
			BodySection: sections,
		}).Collect()
		if err != nil {
			return fmt.Errorf("fetching message body: %w", err)
		}
		if len(msgs) == 0 {
			return fmt.Errorf("message with uid %d disappeared while fetching its body", uid)
		}

		decode := func(tp *mailparse.TextPart) *TextBody {
			raw, ok := findSection(msgs[0], tp.Path)
			if !ok {
				return nil
			}
			preTruncated := tp.EncodedSize > textPartCap
			res := mailparse.DecodeText(raw, tp.Encoding, tp.Charset, preTruncated, bodyCharCap)
			return &TextBody{Text: res.Text, Truncated: res.Truncated, CharsetFallback: res.CharsetFallback}
		}
		if outline.Plain != nil {
			content.Plain = decode(outline.Plain)
		}
		if outline.HTML != nil {
			content.HTML = decode(outline.HTML)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return content, nil
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
