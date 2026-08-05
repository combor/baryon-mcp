package bridgeclient

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/combor/baryon-mcp/internal/mailparse"
)

const (
	// MaxThreadMessages bounds one assembled conversation.
	MaxThreadMessages = 50
	// threadBodyCharCap bounds each body in a thread. It sits far below
	// bodyCharCap because a thread multiplies it by the message count: a whole
	// conversation should cost about what a single get_email does.
	threadBodyCharCap = 2_000
	// threadTextPartCap covers the worst-case encoding of threadBodyCharCap
	// characters, in the same spirit as textPartCap.
	threadTextPartCap = 64 * 1024
)

// ThreadRef selects the conversation to assemble. Folder, UID and UIDValidity
// identify the message to start from; SearchFolder names the folder the
// conversation is gathered from, defaulting to Folder.
type ThreadRef struct {
	Folder        string
	SearchFolder  string
	UID           uint32
	UIDValidity   uint32
	IncludeBodies bool
}

func (r ThreadRef) searchFolder() string {
	if r.SearchFolder != "" {
		return r.SearchFolder
	}
	return r.Folder
}

// ThreadMessage is one message in a conversation. Body is populated only when
// the request asked for it, and holds the plain text part when there is one,
// the HTML part otherwise.
type ThreadMessage struct {
	Summary       EmailSummary
	MessageID     string
	Body          string
	BodyIsHTML    bool
	BodyTruncated bool
}

// Thread is one assembled conversation, oldest first. UID values belong to
// Folder, which is the folder that was searched rather than the one the
// starting message was read from.
type Thread struct {
	Folder      string
	UIDValidity uint32
	Total       int
	Messages    []ThreadMessage
}

// GetThread assembles the conversation containing the referenced message.
//
// Threading is anchored on the conversation's root identifier: Bridge matches
// HEADER searches by substring, so one search for the root inside References
// returns the whole descendant tree, and the results are then verified exactly
// against the parsed headers.
//
// It relies on References chains. A reply carrying only In-Reply-To is found
// when it answers the root directly, but a run of such replies is followed only
// that far, so generations below the first are absent.
func (c *Client) GetThread(ctx context.Context, ref ThreadRef) (*Thread, error) {
	searchFolder := ref.searchFolder()
	var thread *Thread
	err := c.withMessage(ctx, ref.Folder, ref.UIDValidity, func(cli *imapclient.Client) error {
		root, err := threadRoot(cli, ref.UID)
		if err != nil {
			return err
		}

		uidValidity := ref.UIDValidity
		if searchFolder != ref.Folder {
			sel, err := cli.Select(searchFolder, &imap.SelectOptions{ReadOnly: true}).Wait()
			if err != nil {
				return fmt.Errorf("selecting folder %q: %w", searchFolder, err)
			}
			uidValidity = sel.UIDValidity
		}

		members, err := threadMembers(cli, root, ref.IncludeBodies)
		if err != nil {
			return err
		}
		thread = &Thread{Folder: searchFolder, UIDValidity: uidValidity, Total: len(members)}
		// Truncation drops the oldest. The recent end of a long conversation is
		// the part worth reading, and it is where the message the caller started
		// from almost always sits.
		if len(members) > MaxThreadMessages {
			members = members[len(members)-MaxThreadMessages:]
		}
		for _, m := range members {
			msg := ThreadMessage{Summary: m.summary, MessageID: m.messageID}
			if ref.IncludeBodies {
				if err := fillThreadBody(cli, &msg, m.outline); err != nil {
					return err
				}
			}
			thread.Messages = append(thread.Messages, msg)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return thread, nil
}

// threadRoot returns the identifier that anchors the message's conversation:
// the oldest entry of its References chain, or its own Message-ID when it
// starts the conversation.
func threadRoot(cli *imapclient.Client, uid uint32) (string, error) {
	section := threadHeaderSection()
	msgs, err := cli.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return "", fmt.Errorf("fetching identification headers: %w", err)
	}
	if len(msgs) == 0 {
		return "", fmt.Errorf("message with uid %d not found in folder", uid)
	}
	// A malformed header must not make the conversation unreachable; whatever
	// parsed is still usable as an anchor.
	headers, _ := parseThreadHeaders(msgs[0].FindBodySection(section))
	headers.fillFromEnvelope(msgs[0].Envelope)
	if headers.root != "" {
		return headers.root, nil
	}
	// A reply with no chain still names its parent, which anchors the
	// conversation better than the reply's own identifier does.
	if len(headers.inReplyTo) > 0 {
		return headers.inReplyTo[0], nil
	}
	if headers.messageID != "" {
		return headers.messageID, nil
	}
	return "", fmt.Errorf("message with uid %d carries no Message-ID or References header, so its conversation cannot be identified", uid)
}

// threadMember is one verified conversation member, oldest first once sorted.
type threadMember struct {
	summary   EmailSummary
	messageID string
	outline   mailparse.Outline
}

// threadMembers searches the selected folder for root and keeps the messages
// that reference it exactly. Body structures are walked only when bodies are
// wanted, since they cost transfer and parsing on every match.
func threadMembers(cli *imapclient.Client, root string, withBodies bool) ([]threadMember, error) {
	data, err := cli.UIDSearch(threadCriteria(root), nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("searching for conversation %q: %w", root, err)
	}
	uids := data.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	section := threadHeaderSection()
	opts := &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		Flags:       true,
		BodySection: []*imap.FetchItemBodySection{section},
	}
	if withBodies {
		opts.BodyStructure = &imap.FetchItemBodyStructure{Extended: true}
	}
	msgs, err := cli.Fetch(imap.UIDSetNum(uids...), opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetching conversation summaries: %w", err)
	}

	members := make([]threadMember, 0, len(msgs))
	for _, msg := range msgs {
		headers, _ := parseThreadHeaders(msg.FindBodySection(section))
		headers.fillFromEnvelope(msg.Envelope)
		// The search matched a substring, which can catch an identifier that
		// merely contains root. Only an exact reference makes a member.
		if !mentions(headers, root) {
			continue
		}
		member := threadMember{summary: summarize(msg), messageID: headers.messageID}
		if msg.BodyStructure != nil {
			member.outline = mailparse.Walk(msg.BodyStructure)
		}
		members = append(members, member)
	}
	slices.SortFunc(members, func(a, b threadMember) int {
		if d := a.summary.Date.Compare(b.summary.Date); d != 0 {
			return d
		}
		return cmp.Compare(a.summary.UID, b.summary.UID)
	})
	return members, nil
}

// threadCriteria matches the root message and everything descending from it.
// Bridge compares HEADER values by substring, so the References arm matches any
// message whose chain contains root anywhere. In-Reply-To is searched too,
// because a reply is allowed to name its parent without carrying a chain.
func threadCriteria(root string) *imap.SearchCriteria {
	header := func(key string) imap.SearchCriteria {
		return imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: key, Value: root}}}
	}
	// Or holds one pair, so a third term nests inside the second.
	return &imap.SearchCriteria{
		Or: [][2]imap.SearchCriteria{{
			header("References"),
			{Or: [][2]imap.SearchCriteria{{header("Message-ID"), header("In-Reply-To")}}},
		}},
	}
}

// mentions reports whether the message is exactly the root or names it as an
// ancestor. It consults the recorded root as well as the chain, so a descendant
// deep enough to have its chain trimmed is still recognised.
func mentions(headers threadHeaders, root string) bool {
	return headers.messageID == root ||
		headers.root == root ||
		slices.Contains(headers.references, root) ||
		slices.Contains(headers.inReplyTo, root)
}

// fillThreadBody fetches one capped body part, preferring plain text. Each
// message needs its own fetch because the part path comes from its own
// structure.
func fillThreadBody(cli *imapclient.Client, msg *ThreadMessage, outline mailparse.Outline) error {
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
		Partial: &imap.SectionPartial{Offset: 0, Size: threadTextPartCap},
	}
	uid := imap.UID(msg.Summary.UID)
	msgs, err := cli.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{section},
	}).Collect()
	if err != nil {
		return fmt.Errorf("fetching body of uid %d: %w", msg.Summary.UID, err)
	}
	if len(msgs) == 0 {
		// Expunged between search and fetch; the summary still stands.
		return nil
	}
	raw, ok := findSection(msgs[0], part.Path)
	if !ok {
		return nil
	}
	res := mailparse.DecodeText(raw, part.Encoding, part.Charset, part.EncodedSize > threadTextPartCap, threadBodyCharCap)
	msg.Body = res.Text
	msg.BodyIsHTML = isHTML
	msg.BodyTruncated = res.Truncated
	return nil
}
