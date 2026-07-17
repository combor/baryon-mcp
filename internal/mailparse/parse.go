// Package mailparse turns IMAP BODYSTRUCTURE trees into a usable outline
// (body text parts vs attachments) and decodes individual MIME part payloads.
package mailparse

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// TextPart locates a message's text/plain or text/html body part.
type TextPart struct {
	Path        []int // IMAP part path; a non-multipart body is [1]
	Encoding    string
	Charset     string
	EncodedSize uint32
}

// Attachment describes one non-body leaf part.
type Attachment struct {
	Index       int
	Path        []int
	Filename    string
	ContentType string
	Encoding    string
	EncodedSize uint32
}

// Outline is the walked shape of a message: at most one plain and one HTML
// body, and everything else as attachments.
type Outline struct {
	Plain       *TextPart
	HTML        *TextPart
	Attachments []Attachment
}

// Walk classifies bs. The first non-attachment text/plain and text/html
// leaves become the bodies; every other leaf is an attachment, including
// leaves without any disposition or filename. Attachment disposition
// propagates down from ancestor containers. message/rfc822 parts are treated
// as a single attachment without descending.
func Walk(bs imap.BodyStructure) Outline {
	var o Outline
	// attachmentDepth > 0 while inside a subtree whose container is an attachment.
	var attachmentPrefixes [][]int

	inAttachmentSubtree := func(path []int) bool {
		for _, p := range attachmentPrefixes {
			if len(path) >= len(p) && slices.Equal(path[:len(p)], p) {
				return true
			}
		}
		return false
	}

	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		switch part := part.(type) {
		case *imap.BodyStructureMultiPart:
			if disp := part.Disposition(); disp != nil && strings.EqualFold(disp.Value, "attachment") {
				attachmentPrefixes = append(attachmentPrefixes, slices.Clone(path))
			}
			return true
		case *imap.BodyStructureSinglePart:
			isRFC822 := strings.EqualFold(part.MediaType(), "message/rfc822")
			isAttachment := inAttachmentSubtree(path) || isRFC822
			if disp := part.Disposition(); disp != nil && strings.EqualFold(disp.Value, "attachment") {
				isAttachment = true
			}

			// A filename (disposition or legacy Content-Type name param) marks a text part as an attachment, not a body.
			if !isAttachment && part.Filename() == "" && strings.EqualFold(part.Type, "text") {
				tp := &TextPart{
					Path:        slices.Clone(path),
					Encoding:    strings.ToLower(part.Encoding),
					Charset:     part.Params["charset"],
					EncodedSize: part.Size,
				}
				switch {
				case strings.EqualFold(part.Subtype, "plain") && o.Plain == nil:
					o.Plain = tp
					return false
				case strings.EqualFold(part.Subtype, "html") && o.HTML == nil:
					o.HTML = tp
					return false
				}
			}

			o.Attachments = append(o.Attachments, Attachment{
				Index:       len(o.Attachments),
				Path:        slices.Clone(path),
				Filename:    attachmentFilename(part, path),
				ContentType: strings.ToLower(part.MediaType()),
				Encoding:    strings.ToLower(part.Encoding),
				EncodedSize: part.Size,
			})
			return !isRFC822
		}
		return true
	})
	return o
}

func attachmentFilename(part *imap.BodyStructureSinglePart, path []int) string {
	if name := part.Filename(); name != "" {
		return name
	}
	segments := make([]string, 0, len(path))
	for _, p := range path {
		segments = append(segments, strconv.Itoa(p))
	}
	label := strings.Join(segments, ".")
	if label == "" {
		label = "1"
	}
	ext := strings.ToLower(part.Subtype)
	if ext == "" {
		return fmt.Sprintf("part-%s", label)
	}
	return fmt.Sprintf("part-%s.%s", label, ext)
}
