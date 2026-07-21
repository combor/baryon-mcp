package bridgeclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	messageMail "github.com/emersion/go-message/mail"
)

const (
	// MaxDraftAttachments bounds multipart fan-out and tool input size.
	MaxDraftAttachments = 100
	// MaxDraftAttachmentBytes bounds one decoded attachment in decimal MB.
	MaxDraftAttachmentBytes = 25_000_000
	// MaxDraftAttachmentTotalBytes bounds all decoded attachments together in decimal MB.
	MaxDraftAttachmentTotalBytes = 25_000_000
	// MaxDraftMessageBytes is the exclusive limit for the generated RFC822/MIME message.
	MaxDraftMessageBytes = 70 * 1024 * 1024
	// MaxDraftBodyChars bounds each decoded text body.
	MaxDraftBodyChars = 50_000
)

type draftAddresses struct {
	from []*mail.Address
	to   []*mail.Address
	cc   []*mail.Address
	bcc  []*mail.Address
}

func parseDraftContentType(index int, contentType string) (string, map[string]string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	// ParseMediaType also accepts Content-Disposition tokens such as "attachment".
	if err != nil || !strings.Contains(mediaType, "/") {
		return "", nil, fmt.Errorf("attachment %d has invalid content_type %q", index, contentType)
	}
	return mediaType, params, nil
}

func validateDraft(draft Draft) (draftAddresses, error) {
	var addresses draftAddresses
	if strings.TrimSpace(draft.From) == "" {
		return addresses, fmt.Errorf("from is required")
	}
	from, err := mail.ParseAddress(draft.From)
	if err != nil {
		return addresses, fmt.Errorf("invalid from address: %w", err)
	}
	addresses.from = []*mail.Address{from}
	for _, field := range []struct {
		name   string
		values []string
		target *[]*mail.Address
	}{
		{name: "to", values: draft.To, target: &addresses.to},
		{name: "cc", values: draft.Cc, target: &addresses.cc},
		{name: "bcc", values: draft.Bcc, target: &addresses.bcc},
	} {
		parsed := make([]*mail.Address, 0, len(field.values))
		for i, value := range field.values {
			address, err := mail.ParseAddress(value)
			if err != nil {
				return addresses, fmt.Errorf("invalid %s address at index %d: %w", field.name, i, err)
			}
			parsed = append(parsed, address)
		}
		*field.target = parsed
	}
	if draft.Replace != nil {
		if draft.Replace.UID == 0 {
			return addresses, fmt.Errorf("uid is required when replacing a draft")
		}
		if draft.Replace.UIDValidity == 0 {
			return addresses, fmt.Errorf("uidvalidity is required when replacing a draft")
		}
	}
	if utf8.RuneCountInString(draft.TextBody) > MaxDraftBodyChars {
		return addresses, fmt.Errorf("text_body is above the %d character limit", MaxDraftBodyChars)
	}
	if utf8.RuneCountInString(draft.HTMLBody) > MaxDraftBodyChars {
		return addresses, fmt.Errorf("html_body is above the %d character limit", MaxDraftBodyChars)
	}
	if len(draft.Attachments) > MaxDraftAttachments {
		return addresses, fmt.Errorf("a draft may contain at most %d attachments", MaxDraftAttachments)
	}
	total := 0
	for i, attachment := range draft.Attachments {
		if strings.TrimSpace(attachment.Filename) == "" {
			return addresses, fmt.Errorf("attachment %d filename is required", i)
		}
		if strings.ContainsAny(attachment.Filename, "\x00\r\n") {
			return addresses, fmt.Errorf("attachment %d filename contains control characters", i)
		}
		if _, _, err := parseDraftContentType(i, attachment.ContentType); err != nil {
			return addresses, err
		}
		if len(attachment.Data) > MaxDraftAttachmentBytes {
			return addresses, fmt.Errorf("attachment %q is %d bytes, above the %d byte limit", attachment.Filename, len(attachment.Data), MaxDraftAttachmentBytes)
		}
		if total > MaxDraftAttachmentTotalBytes-len(attachment.Data) {
			return addresses, fmt.Errorf("attachments total %d bytes, above the %d byte limit", total+len(attachment.Data), MaxDraftAttachmentTotalBytes)
		}
		total += len(attachment.Data)
	}
	return addresses, nil
}

func draftHeader(draft Draft, addresses draftAddresses, thread threadHeaders, now time.Time) (messageMail.Header, error) {
	var header messageMail.Header
	header.SetDate(now)
	header.SetAddressList("From", addresses.from)
	header.SetAddressList("To", addresses.to)
	header.SetAddressList("Cc", addresses.cc)
	header.SetAddressList("Bcc", addresses.bcc)
	header.SetSubject(draft.Subject)
	if thread.messageID != "" {
		header.SetMessageID(thread.messageID)
	} else if err := header.GenerateMessageIDWithHostname("baryon-mcp.local"); err != nil {
		return messageMail.Header{}, fmt.Errorf("generating Message-ID: %w", err)
	}
	if len(thread.inReplyTo) > 0 {
		header.SetMsgIDList("In-Reply-To", thread.inReplyTo)
	}
	if len(thread.references) > 0 {
		header.SetMsgIDList("References", thread.references)
	}
	return header, nil
}

func inlineHeader(contentType string) messageMail.InlineHeader {
	var header messageMail.InlineHeader
	header.SetContentType(contentType, map[string]string{"charset": "utf-8"})
	return header
}

func writePart(w io.WriteCloser, body string) error {
	if _, err := io.WriteString(w, body); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func writeAlternative(w *messageMail.InlineWriter, draft Draft) error {
	plain, err := w.CreatePart(inlineHeader("text/plain"))
	if err != nil {
		return err
	}
	if err := writePart(plain, draft.TextBody); err != nil {
		return err
	}
	html, err := w.CreatePart(inlineHeader("text/html"))
	if err != nil {
		return err
	}
	if err := writePart(html, draft.HTMLBody); err != nil {
		return err
	}
	return w.Close()
}

func buildDraftMessage(draft Draft, addresses draftAddresses, thread threadHeaders, now time.Time) ([]byte, error) {
	header, err := draftHeader(draft, addresses, thread, now)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if len(draft.Attachments) == 0 {
		if draft.HTMLBody != "" {
			writer, err := messageMail.CreateInlineWriter(&buf, header)
			if err != nil {
				return nil, err
			}
			if err := writeAlternative(writer, draft); err != nil {
				return nil, err
			}
		} else {
			header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
			writer, err := messageMail.CreateSingleInlineWriter(&buf, header)
			if err != nil {
				return nil, err
			}
			if err := writePart(writer, draft.TextBody); err != nil {
				return nil, err
			}
		}
		return buf.Bytes(), nil
	}

	writer, err := messageMail.CreateWriter(&buf, header)
	if err != nil {
		return nil, err
	}
	if draft.HTMLBody != "" {
		inline, err := writer.CreateInline()
		if err != nil {
			return nil, err
		}
		if err := writeAlternative(inline, draft); err != nil {
			return nil, err
		}
	} else {
		plain, err := writer.CreateSingleInline(inlineHeader("text/plain"))
		if err != nil {
			return nil, err
		}
		if err := writePart(plain, draft.TextBody); err != nil {
			return nil, err
		}
	}
	for i, attachment := range draft.Attachments {
		mediaType, params, err := parseDraftContentType(i, attachment.ContentType)
		if err != nil {
			return nil, err
		}
		var attachmentHeader messageMail.AttachmentHeader
		attachmentHeader.SetContentType(mediaType, params)
		attachmentHeader.SetFilename(attachment.Filename)
		part, err := writer.CreateAttachment(attachmentHeader)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(attachment.Data); err != nil {
			_ = part.Close()
			return nil, err
		}
		if err := part.Close(); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func findDraftsMailbox(cli *imapclient.Client) (string, error) {
	caps := cli.Caps()
	if !caps.Has(imap.CapUIDPlus) {
		return "", fmt.Errorf("bridge does not advertise UIDPLUS; refusing to save a draft without reliable UID results and targeted expunge")
	}
	mailboxes, err := cli.List("", "*", nil).Collect()
	if err != nil {
		return "", fmt.Errorf("listing mailboxes: %w", err)
	}
	var nameFallback string
	for _, mailbox := range mailboxes {
		if slices.Contains(mailbox.Attrs, imap.MailboxAttrDrafts) {
			return mailbox.Mailbox, nil
		}
		if nameFallback == "" && strings.EqualFold(mailbox.Mailbox, "Drafts") {
			nameFallback = mailbox.Mailbox
		}
	}
	if nameFallback != "" {
		return nameFallback, nil
	}
	return "", fmt.Errorf("bridge returned no Drafts mailbox by special-use attribute or name")
}

// fetchDraftMetadata reads the headers a replacement must carry over. Malformed
// identification headers are an error here: silently dropping them would
// detach the draft from its conversation.
func fetchDraftMetadata(cli *imapclient.Client, uid uint32) (threadHeaders, error) {
	section := threadHeaderSection()
	messages, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID:         true,
		Flags:       true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return threadHeaders{}, fmt.Errorf("fetching draft uid %d: %w", uid, err)
	}
	if len(messages) == 0 {
		return threadHeaders{}, fmt.Errorf("draft with uid %d not found in Drafts", uid)
	}
	if slices.Contains(messages[0].Flags, imap.FlagDeleted) {
		return threadHeaders{}, fmt.Errorf("draft with uid %d is already marked deleted", uid)
	}
	thread, err := parseThreadHeaders(messages[0].FindBodySection(section))
	if err != nil {
		return threadHeaders{}, fmt.Errorf("draft uid %d: %w", uid, err)
	}
	thread.fillFromEnvelope(messages[0].Envelope)
	return thread, nil
}

func appendDraft(cli *imapclient.Client, folder string, raw []byte, now time.Time) (*imap.AppendData, error) {
	cmd := cli.Append(folder, int64(len(raw)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagDraft},
		Time:  now,
	})
	n, err := cmd.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = cmd.Close()
		_, _ = cmd.Wait()
		return nil, fmt.Errorf("writing draft to bridge: %w", err)
	}
	if err := cmd.Close(); err != nil {
		_, _ = cmd.Wait()
		return nil, fmt.Errorf("finishing draft upload: %w", err)
	}
	data, err := cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("appending draft to %q: %w", folder, err)
	}
	if data.UID == 0 || data.UIDValidity == 0 {
		return nil, fmt.Errorf("bridge accepted the draft but returned no APPENDUID; the draft may have been saved, re-list Drafts before retrying")
	}
	return data, nil
}

func validateDraftMessageSize(size int) error {
	if size >= MaxDraftMessageBytes {
		return fmt.Errorf("generated draft message is %d bytes; it must be below the %d byte limit", size, MaxDraftMessageBytes)
	}
	return nil
}

func removePreviousDraft(cli *imapclient.Client, uid uint32) error {
	uids := imap.UIDSetNum(imap.UID(uid))
	if err := cli.Store(uids, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagDeleted},
	}, nil).Close(); err != nil {
		return fmt.Errorf("marking previous draft uid %d deleted: %w", uid, err)
	}
	if _, err := cli.UIDExpunge(uids).Collect(); err != nil {
		return fmt.Errorf("expunging previous draft uid %d: %w", uid, err)
	}
	return nil
}

// SaveDraft creates a complete draft, or appends a replacement before
// removing the previous UID. Appending first keeps the old draft intact if
// Bridge rejects the new message.
func (c *Client) SaveDraft(ctx context.Context, draft Draft) (*SavedDraft, error) {
	addresses, err := validateDraft(draft)
	if err != nil {
		return nil, err
	}
	thread, err := draftThreadHeaders(draft)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.draftGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.draftGate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var saved *SavedDraft
	err = c.withSession(ctx, func(cli *imapclient.Client) error {
		folder, err := findDraftsMailbox(cli)
		if err != nil {
			return err
		}
		selected, err := cli.Select(folder, nil).Wait()
		if err != nil {
			return fmt.Errorf("selecting Drafts mailbox %q: %w", folder, err)
		}

		if draft.Replace != nil {
			if selected.UIDValidity != draft.Replace.UIDValidity {
				return fmt.Errorf("Drafts UIDVALIDITY changed (now %d, expected %d): draft UIDs are stale, re-run list_emails", selected.UIDValidity, draft.Replace.UIDValidity)
			}
			previous, err := fetchDraftMetadata(cli, draft.Replace.UID)
			if err != nil {
				return err
			}
			thread.messageID = previous.messageID
			// An omitted header is kept; an empty one is a request to remove it.
			if thread.inReplyTo == nil {
				thread.inReplyTo = previous.inReplyTo
			}
			if thread.references == nil {
				thread.references = previous.references
			}
		}

		now := time.Now()
		raw, err := buildDraftMessage(draft, addresses, thread, now)
		if err != nil {
			return err
		}
		if err := validateDraftMessageSize(len(raw)); err != nil {
			return err
		}
		appended, err := appendDraft(cli, folder, raw, now)
		if err != nil {
			return err
		}
		saved = &SavedDraft{
			Folder:      folder,
			UID:         uint32(appended.UID),
			UIDValidity: appended.UIDValidity,
		}
		if draft.Replace == nil {
			return nil
		}

		saved.ReplacedUID = draft.Replace.UID
		if err := removePreviousDraft(cli, draft.Replace.UID); err != nil {
			saved.Warning = fmt.Sprintf("replacement draft saved as uid %d, but the previous draft may remain: %v", saved.UID, err)
			return nil
		}
		saved.PreviousDraftRemoved = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}
