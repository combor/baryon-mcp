package mcptools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
	"github.com/combor/baryon-mcp/internal/config"
)

var testIdentities = []config.Identity{
	{Address: "me@proton.me", Name: "Me Myself"},
	{Address: "work@proton.me"},
}

// received is a message addressed to this account, the ordinary case.
func received() replySource {
	return replySource{
		Subject:   "Quarterly numbers",
		From:      []string{"Alice <alice@example.org>"},
		To:        []string{"Me Myself <me@proton.me>", "Bob <bob@example.org>"},
		Cc:        []string{"Carol <carol@example.org>"},
		MessageID: "parent@example.org",
	}
}

func TestBuildReplyAddressesTheSender(t *testing.T) {
	draft, err := buildReply(received(), replyOptions{}, testIdentities)
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	if want := []string{"Alice <alice@example.org>"}; !equalStrings(draft.To, want) {
		t.Errorf("To = %q, want %q", draft.To, want)
	}
	if len(draft.Cc) != 0 {
		t.Errorf("Cc = %q, want none without reply_all", draft.Cc)
	}
	if draft.From != "Me Myself <me@proton.me>" {
		t.Errorf("From = %q, want the identity the message was addressed to", draft.From)
	}
	if draft.Subject != "Re: Quarterly numbers" {
		t.Errorf("Subject = %q", draft.Subject)
	}
	if !equalStrings(draft.InReplyTo, []string{"parent@example.org"}) {
		t.Errorf("InReplyTo = %q", draft.InReplyTo)
	}
	if !equalStrings(draft.References, []string{"parent@example.org"}) {
		t.Errorf("References = %q", draft.References)
	}
	if draft.TextBody != "" || len(draft.Attachments) != 0 {
		t.Error("buildReply must not carry any of the original's content")
	}
}

func TestBuildReplyRecipientPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*replySource)
		want   []string
	}{
		{
			name:   "reply-to wins over from",
			mutate: func(s *replySource) { s.ReplyTo = []string{"list@example.org"} },
			want:   []string{"list@example.org"},
		},
		{
			name: "sender is used when from is absent",
			mutate: func(s *replySource) {
				s.From = nil
				s.Sender = []string{"secretary@example.org"}
			},
			want: []string{"secretary@example.org"},
		},
		{
			name: "aliases of this account are removed",
			mutate: func(s *replySource) {
				s.ReplyTo = []string{"WORK@proton.me", "Alice <alice@example.org>"}
			},
			want: []string{"Alice <alice@example.org>"},
		},
		{
			name: "duplicate mailboxes collapse",
			mutate: func(s *replySource) {
				s.ReplyTo = []string{"Alice <alice@example.org>", "A. <ALICE@example.org>"}
			},
			want: []string{"Alice <alice@example.org>"},
		},
		{
			name:   "unparseable addresses are skipped",
			mutate: func(s *replySource) { s.ReplyTo = []string{"@@broken", "Alice <alice@example.org>"} },
			want:   []string{"Alice <alice@example.org>"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := received()
			tc.mutate(&source)
			draft, err := buildReply(source, replyOptions{}, testIdentities)
			if err != nil {
				t.Fatalf("buildReply: %v", err)
			}
			if !equalStrings(draft.To, tc.want) {
				t.Errorf("To = %q, want %q", draft.To, tc.want)
			}
		})
	}
}

func TestBuildReplyAllCopiesEveryoneElse(t *testing.T) {
	source := received()
	source.Bcc = []string{"hidden@example.org"}
	draft, err := buildReply(source, replyOptions{ReplyAll: true, Bcc: []string{"archive@example.org"}}, testIdentities)
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	if want := []string{"Alice <alice@example.org>"}; !equalStrings(draft.To, want) {
		t.Errorf("To = %q, want %q", draft.To, want)
	}
	want := []string{"Bob <bob@example.org>", "Carol <carol@example.org>"}
	if !equalStrings(draft.Cc, want) {
		t.Errorf("Cc = %q, want %q", draft.Cc, want)
	}
	for _, recipient := range append(append([]string{}, draft.To...), draft.Cc...) {
		if strings.Contains(recipient, "hidden@example.org") {
			t.Fatalf("the original Bcc leaked into %q", recipient)
		}
		if strings.Contains(strings.ToLower(recipient), "@proton.me") {
			t.Errorf("this account was copied on its own reply: %q", recipient)
		}
	}
	if !equalStrings(draft.Bcc, []string{"archive@example.org"}) {
		t.Errorf("Bcc = %q, want only what the caller supplied", draft.Bcc)
	}
}

func TestBuildReplyAllDoesNotRepeatThePrimaryRecipient(t *testing.T) {
	source := received()
	source.To = append(source.To, "Alice <alice@example.org>")
	draft, err := buildReply(source, replyOptions{ReplyAll: true}, testIdentities)
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	for _, cc := range draft.Cc {
		if strings.Contains(strings.ToLower(cc), "alice@example.org") {
			t.Errorf("Cc %q repeats the To recipient", cc)
		}
	}
}

func TestBuildReplyIdentitySelection(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		mutate    func(*replySource)
		want      string
		wantErr   string
	}{
		{
			name:      "explicit choice wins",
			requested: "work@proton.me",
			want:      "work@proton.me",
		},
		{
			name:      "explicit choice must be configured",
			requested: "someone@elsewhere.test",
			wantErr:   "list_sender_identities",
		},
		{
			name:      "explicit choice must parse",
			requested: "not an address",
			wantErr:   "not a valid address",
		},
		{
			name:   "the addressed identity is preferred",
			mutate: func(s *replySource) { s.To = []string{"work@proton.me"} },
			want:   "work@proton.me",
		},
		{
			name:   "a cc'd identity counts too",
			mutate: func(s *replySource) { s.To, s.Cc = []string{"bob@example.org"}, []string{"work@proton.me"} },
			want:   "work@proton.me",
		},
		{
			name: "a message this account sent keeps its from",
			mutate: func(s *replySource) {
				s.To = []string{"bob@example.org"}
				s.From = []string{"work@proton.me"}
				s.ReplyTo = []string{"client@example.org"}
			},
			want: "work@proton.me",
		},
		{
			name:   "otherwise the default identity",
			mutate: func(s *replySource) { s.To = []string{"bob@example.org"} },
			want:   "Me Myself <me@proton.me>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := received()
			if tc.mutate != nil {
				tc.mutate(&source)
			}
			draft, err := buildReply(source, replyOptions{From: tc.requested}, testIdentities)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got err %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildReply: %v", err)
			}
			if draft.From != tc.want {
				t.Errorf("From = %q, want %q", draft.From, tc.want)
			}
		})
	}
}

func TestBuildReplySubjectPrefix(t *testing.T) {
	for subject, want := range map[string]string{
		"Quarterly numbers":     "Re: Quarterly numbers",
		"Re: Quarterly numbers": "Re: Quarterly numbers",
		"RE: Quarterly numbers": "RE: Quarterly numbers",
		"re:Quarterly":          "re:Quarterly",
		"  Spaced  ":            "Re: Spaced",
		"":                      "Re:",
	} {
		source := received()
		source.Subject = subject
		draft, err := buildReply(source, replyOptions{}, testIdentities)
		if err != nil {
			t.Fatalf("buildReply(%q): %v", subject, err)
		}
		if draft.Subject != want {
			t.Errorf("subject %q became %q, want %q", subject, draft.Subject, want)
		}
	}
}

func TestBuildReplyReferences(t *testing.T) {
	t.Run("appends to the parent chain", func(t *testing.T) {
		source := received()
		source.References = []string{"root@example.org", "middle@example.org"}
		draft, err := buildReply(source, replyOptions{}, testIdentities)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"root@example.org", "middle@example.org", "parent@example.org"}
		if !equalStrings(draft.References, want) {
			t.Errorf("References = %q, want %q", draft.References, want)
		}
	})

	t.Run("falls back to in-reply-to when the parent has no chain", func(t *testing.T) {
		source := received()
		source.InReplyTo = []string{"grandparent@example.org"}
		draft, err := buildReply(source, replyOptions{}, testIdentities)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"grandparent@example.org", "parent@example.org"}
		if !equalStrings(draft.References, want) {
			t.Errorf("References = %q, want %q", draft.References, want)
		}
	})

	t.Run("stays within the limit a draft accepts", func(t *testing.T) {
		source := received()
		for i := range bridgeclient.MaxThreadReferences + 10 {
			source.References = append(source.References, fmt.Sprintf("ancestor-%d@example.org", i))
		}
		draft, err := buildReply(source, replyOptions{}, testIdentities)
		if err != nil {
			t.Fatal(err)
		}
		if len(draft.References) != bridgeclient.MaxThreadReferences {
			t.Fatalf("References has %d entries, want the %d limit", len(draft.References), bridgeclient.MaxThreadReferences)
		}
		if last := draft.References[len(draft.References)-1]; last != "parent@example.org" {
			t.Errorf("trimming dropped the parent; last entry = %q", last)
		}
	})

	t.Run("a parent already in the chain is not repeated", func(t *testing.T) {
		source := received()
		source.References = []string{"root@example.org", "parent@example.org"}
		draft, err := buildReply(source, replyOptions{}, testIdentities)
		if err != nil {
			t.Fatal(err)
		}
		if !equalStrings(draft.References, []string{"root@example.org", "parent@example.org"}) {
			t.Errorf("References = %q", draft.References)
		}
	})
}

func TestBuildReplyRefusesWhatItCannotDerive(t *testing.T) {
	t.Run("no message id", func(t *testing.T) {
		source := received()
		source.MessageID = ""
		_, err := buildReply(source, replyOptions{}, testIdentities)
		if err == nil || !strings.Contains(err.Error(), "save_draft") {
			t.Errorf("got %v, want a refusal pointing at save_draft", err)
		}
	})

	t.Run("no recipient but this account", func(t *testing.T) {
		source := received()
		source.From = []string{"me@proton.me"}
		source.To = []string{"work@proton.me"}
		source.Cc = nil
		_, err := buildReply(source, replyOptions{}, testIdentities)
		if err == nil || !strings.Contains(err.Error(), "save_draft") {
			t.Errorf("got %v, want a refusal pointing at save_draft", err)
		}
	})

	t.Run("no identities configured", func(t *testing.T) {
		_, err := buildReply(received(), replyOptions{}, nil)
		if err == nil || !strings.Contains(err.Error(), "BARYON_SENDER_IDENTITIES") {
			t.Errorf("got %v, want a refusal naming the setting", err)
		}
	})
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A message with no readable Reply-To, From or Sender has nobody to answer;
// its To holds fellow recipients who never wrote it.
func TestBuildReplyRefusesAMessageWithNoReadableSender(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*replySource)
	}{
		{"no sender fields at all", func(s *replySource) { s.From, s.Sender, s.ReplyTo = nil, nil, nil }},
		{"unparseable sender fields", func(s *replySource) {
			s.From = []string{"Undisclosed recipients"}
			s.Sender = []string{"@@broken"}
			s.ReplyTo = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := received()
			tc.mutate(&source)
			// Addressed to this account and to a third party.
			source.To = []string{"me@proton.me", "Bob <bob@example.org>"}
			_, err := buildReply(source, replyOptions{}, testIdentities)
			if err == nil {
				t.Fatal("expected a refusal rather than a reply to a fellow recipient")
			}
			if !strings.Contains(err.Error(), "save_draft") {
				t.Errorf("error %v should point at save_draft", err)
			}
		})
	}
}

// Headers cannot show a message was really sent by this account, so a forged
// From must not let its To pick the reply's recipient.
func TestBuildReplyRefusesAMessageThatOnlyNamesThisAccount(t *testing.T) {
	source := received()
	source.From = []string{"Me Myself <me@proton.me>"}
	source.ReplyTo = nil
	source.To = []string{"me@proton.me", "Bob <bob@example.org>"}
	source.Cc = []string{"Carol <carol@example.org>"}

	for _, opts := range []replyOptions{{}, {ReplyAll: true}} {
		if _, err := buildReply(source, opts, testIdentities); err == nil {
			t.Errorf("reply_all=%v: expected a refusal rather than a reply to the message's own recipients", opts.ReplyAll)
		}
	}
}

// Reply-To is the sender's request, not a claim of authorship: anyone may
// point it at this account.
func TestBuildReplyDoesNotTreatReplyToAsAuthorship(t *testing.T) {
	source := received()
	source.From = []string{"Undisclosed recipients"} // unparseable
	source.Sender = nil
	source.ReplyTo = []string{"me@proton.me"} // this account, chosen by the sender
	source.To = []string{"me@proton.me", "Bob <bob@example.org>"}

	if _, err := buildReply(source, replyOptions{}, testIdentities); err == nil {
		t.Error("a self Reply-To on a message this account did not write must not address the reply to a fellow recipient")
	}
	if _, err := buildReply(source, replyOptions{ReplyAll: true}, testIdentities); err == nil {
		t.Error("reply_all must not copy the other recipients of a message with no readable sender")
	}
}

// The same with external recipients in Cc only.
func TestBuildReplyRefusesSelfOriginCcOnlyMessage(t *testing.T) {
	source := received()
	source.From = []string{"Me Myself <me@proton.me>"}
	source.ReplyTo = nil
	source.To = []string{"work@proton.me"}
	source.Cc = []string{"Bob <bob@example.org>"}

	if _, err := buildReply(source, replyOptions{ReplyAll: true}, testIdentities); err == nil {
		t.Error("expected a refusal; save_draft is where a follow-up names its own recipients")
	}
}

// A Reply-To naming only this account cannot be honoured, so derivation
// continues down the message's other origin fields — never to a recipient.
func TestBuildReplyFallsBackToTheSenderWhenReplyToIsSelfOnly(t *testing.T) {
	source := received()
	source.ReplyTo = []string{"me@proton.me"}

	draft, err := buildReply(source, replyOptions{}, testIdentities)
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	if !equalStrings(draft.To, []string{"Alice <alice@example.org>"}) {
		t.Errorf("To = %q, want the message's sender", draft.To)
	}

	// A Reply-To that names someone besides this account is still honoured in full.
	source.ReplyTo = []string{"me@proton.me", "List <list@example.org>"}
	draft, err = buildReply(source, replyOptions{}, testIdentities)
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	if !equalStrings(draft.To, []string{"List <list@example.org>"}) {
		t.Errorf("To = %q, want the non-self Reply-To address", draft.To)
	}
}
