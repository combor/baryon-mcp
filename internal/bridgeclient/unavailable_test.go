package bridgeclient

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestUnavailableFailsEveryCall(t *testing.T) {
	ctx := context.Background()
	var b Bridge = Unavailable{}

	calls := map[string]func() error{
		"ListFolders": func() error { _, err := b.ListFolders(ctx); return err },
		"ListMessages": func() error {
			_, err := b.ListMessages(ctx, "INBOX", SearchCriteria{}, PageRequest{Limit: 10})
			return err
		},
		"GetEmail":        func() error { _, err := b.GetEmail(ctx, "INBOX", 1, 1); return err },
		"GetThread":       func() error { _, err := b.GetThread(ctx, ThreadRef{}); return err },
		"ListAttachments": func() error { _, err := b.ListAttachments(ctx, "INBOX", 1, 1); return err },
		"GetAttachment":   func() error { _, err := b.GetAttachment(ctx, "INBOX", 1, 1, 0); return err },
		"SaveDraft":       func() error { _, err := b.SaveDraft(ctx, Draft{}); return err },
	}

	// A method added to Bridge later must be given the same treatment.
	if want := reflect.TypeOf(&b).Elem().NumMethod(); len(calls) != want {
		t.Fatalf("covering %d methods, Bridge has %d", len(calls), want)
	}

	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrUnconfigured) {
			t.Errorf("%s returned %v, want ErrUnconfigured", name, err)
		}
	}
}
