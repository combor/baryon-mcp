package bridgeclient

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func TestFolderPolicyMatching(t *testing.T) {
	policy := folderPolicy{allowed: []string{"INBOX", "Folders/Invoices", "All Mail"}}
	for name, want := range map[string]bool{
		"INBOX":             true,
		"inbox":             true,
		"Folders/Invoices":  true,
		"folders/invoices":  false,
		"Folders/Invoices2": false,
		"All Mail":          true,
		"Sent":              false,
		"":                  false,
	} {
		if got := policy.permits(name); got != want {
			t.Errorf("permits(%q) = %v, want %v", name, got, want)
		}
	}

	if err := (folderPolicy{}).check("anything"); err != nil {
		t.Errorf("unset policy refused %v", err)
	}
	err := policy.check("Sent")
	if err == nil || !strings.Contains(err.Error(), "BARYON_ALLOWED_FOLDERS") || !strings.Contains(err.Error(), "All Mail") {
		t.Errorf("check(Sent) = %v, want a refusal naming the allowed folders", err)
	}
}

func TestFolderPolicyFilter(t *testing.T) {
	folders := []Folder{{Name: "INBOX"}, {Name: "Sent"}, {Name: "Folders/Invoices"}}
	kept := folderPolicy{allowed: []string{"inbox", "Folders/Invoices"}}.filter(folders)
	if len(kept) != 2 || kept[0].Name != "INBOX" || kept[1].Name != "Folders/Invoices" {
		t.Errorf("filter kept %+v, want INBOX and Folders/Invoices", kept)
	}
	if all := (folderPolicy{}).filter(folders); len(all) != 3 {
		t.Errorf("unset policy filtered to %+v", all)
	}
}

// selectRecorder notes every mailbox the server was asked to open, so a
// refusal can be shown to precede the select.
type selectRecorder struct {
	imapserver.Session
	mu       *sync.Mutex
	selected *[]string
}

func (s *selectRecorder) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	s.mu.Lock()
	*s.selected = append(*s.selected, mailbox)
	s.mu.Unlock()
	return s.Session.Select(mailbox, options)
}

func TestProtocolFolderScopeDeniesBeforeSelect(t *testing.T) {
	var mu sync.Mutex
	var selected []string
	c := startMemServerWithOptions(t, func(u *imapmemserver.User) {
		for _, name := range []string{"INBOX", "Sent"} {
			if err := u.Create(name, nil); err != nil && name != "INBOX" {
				t.Fatal(err)
			}
		}
	}, nil, func(session imapserver.Session) imapserver.Session {
		return &selectRecorder{Session: session, mu: &mu, selected: &selected}
	})
	c.policy = folderPolicy{allowed: []string{"INBOX"}}
	ctx := context.Background()

	calls := map[string]func() error{
		"list_emails": func() error {
			_, err := c.ListMessages(ctx, "Sent", SearchCriteria{}, PageRequest{Limit: 1})
			return err
		},
		"get_email":        func() error { _, err := c.GetEmail(ctx, "Sent", 1, 1); return err },
		"list_attachments": func() error { _, err := c.ListAttachments(ctx, "Sent", 1, 1); return err },
		"get_attachment":   func() error { _, err := c.GetAttachment(ctx, "Sent", 1, 1, 0); return err },
		"get_thread": func() error {
			_, err := c.GetThread(ctx, ThreadRef{Folder: "Sent", UID: 1, UIDValidity: 1})
			return err
		},
		"get_thread search": func() error {
			_, err := c.GetThread(ctx, ThreadRef{Folder: "INBOX", SearchFolder: "Sent", UID: 1, UIDValidity: 1})
			return err
		},
	}
	for name, call := range calls {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "BARYON_ALLOWED_FOLDERS") {
			t.Errorf("%s on a disallowed folder: got %v, want a scope refusal", name, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for _, mailbox := range selected {
		if mailbox == "Sent" {
			t.Fatalf("Sent was selected despite the scope; selects: %q", selected)
		}
	}
}

func TestProtocolFolderScopeFiltersListingAndAllowsScopedReads(t *testing.T) {
	c := seedInbox(t)
	c.policy = folderPolicy{allowed: []string{"inbox"}}

	folders, err := c.ListFolders(context.Background())
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 || !strings.EqualFold(folders[0].Name, "INBOX") {
		t.Fatalf("folders = %+v, want only INBOX", folders)
	}

	// A mailbox in scope keeps working, spelled as the server spells it.
	page, err := c.ListMessages(context.Background(), "INBOX", SearchCriteria{}, PageRequest{Limit: 1})
	if err != nil || len(page.Emails) != 1 {
		t.Fatalf("scoped read: got (%+v, %v), want the page", page, err)
	}
}

// Draft saving is outside the read scope, or a scope without Drafts would
// disable the server's one write.
func TestProtocolFolderScopeLeavesDraftsWritable(t *testing.T) {
	client := seedDraftMailbox(t, false)
	client.policy = folderPolicy{allowed: []string{"INBOX"}}

	saved, err := client.SaveDraft(context.Background(), Draft{
		From: "me@example.org", To: []string{"you@example.org"}, Subject: "hi", TextBody: "hello",
	})
	if err != nil {
		t.Fatalf("SaveDraft under a folder scope: %v", err)
	}
	if saved.UID == 0 {
		t.Error("draft saved without a uid")
	}
}
