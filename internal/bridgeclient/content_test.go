package bridgeclient

import (
	"testing"

	"github.com/combor/baryon-mcp/internal/mailparse"
)

func TestChooseSourceParts(t *testing.T) {
	attachments := []mailparse.Attachment{
		{Index: 0, Filename: "upper.png", ContentID: "Logo@x"},
		{Index: 1, Filename: "lower.png", ContentID: "logo@x"},
		{Index: 2, Filename: "again.png", ContentID: "logo@x"},
		{Index: 3, Filename: "mixed-ref.png", ContentID: "Chart@X"},
		{Index: 4, Filename: "report.pdf", ContentID: "pdf@x"},
		{Index: 5, Filename: "bad.png", ContentID: "a b"},
	}
	referenced := mailparse.ContentIDReferences(`<img src="cid:Logo@x"><img src="cid:logo@x"><img src="cid:chart@x"><img src="cid:a b">`)

	inlineIDs := func(parts []sourcePart) map[string]string {
		ids := make(map[string]string)
		for _, part := range parts {
			ids[part.Filename] = part.inlineID
		}
		return ids
	}

	all := inlineIDs(chooseSourceParts(attachments, referenced, AllParts))
	want := map[string]string{
		"upper.png":     "Logo@x",
		"lower.png":     "logo@x",
		"again.png":     "",
		"mixed-ref.png": "Chart@X",
		"report.pdf":    "",
		"bad.png":       "",
	}
	for name, id := range want {
		got, ok := all[name]
		if !ok || got != id {
			t.Errorf("all parts: %s inline ID = %q (present %v), want %q", name, got, ok, id)
		}
	}

	inline := inlineIDs(chooseSourceParts(attachments, referenced, ReferencedInlineParts))
	if len(inline) != 3 || inline["upper.png"] != "Logo@x" || inline["lower.png"] != "logo@x" || inline["mixed-ref.png"] != "Chart@X" {
		t.Errorf("referenced inline parts = %v, want the three referenced, distinct identifiers", inline)
	}
}
