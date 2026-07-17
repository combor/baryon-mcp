package mailparse

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func textPart(subtype, charset string, size uint32) *imap.BodyStructureSinglePart {
	return &imap.BodyStructureSinglePart{
		Type: "text", Subtype: subtype,
		Params:   map[string]string{"charset": charset},
		Encoding: "quoted-printable", Size: size,
	}
}

func TestWalkSinglePartMessage(t *testing.T) {
	o := Walk(textPart("plain", "utf-8", 100))
	if o.Plain == nil || len(o.Plain.Path) != 1 || o.Plain.Path[0] != 1 {
		t.Fatalf("plain = %+v, want part path [1]", o.Plain)
	}
	if o.HTML != nil || len(o.Attachments) != 0 {
		t.Errorf("unexpected html/attachments: %+v", o)
	}
}

func TestWalkAlternativeWithAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			&imap.BodyStructureMultiPart{
				Subtype: "alternative",
				Children: []imap.BodyStructure{
					textPart("plain", "utf-8", 10),
					textPart("html", "utf-8", 20),
				},
			},
			&imap.BodyStructureSinglePart{
				Type: "application", Subtype: "pdf",
				Encoding: "base64", Size: 5000,
				Extended: &imap.BodyStructureSinglePartExt{
					Disposition: &imap.BodyStructureDisposition{
						Value:  "attachment",
						Params: map[string]string{"filename": "report.pdf"},
					},
				},
			},
		},
	}
	o := Walk(bs)
	if o.Plain == nil || len(o.Plain.Path) != 2 || o.Plain.Path[0] != 1 || o.Plain.Path[1] != 1 {
		t.Errorf("plain path = %v, want [1 1]", o.Plain)
	}
	if o.HTML == nil {
		t.Error("html body missing")
	}
	if len(o.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want 1", o.Attachments)
	}
	a := o.Attachments[0]
	if a.Filename != "report.pdf" || a.ContentType != "application/pdf" || a.Index != 0 {
		t.Errorf("attachment = %+v", a)
	}
	if len(a.Path) != 1 || a.Path[0] != 2 {
		t.Errorf("attachment path = %v, want [2]", a.Path)
	}
}

func TestWalkOrphanLeafBecomesAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			textPart("plain", "utf-8", 10),
			// A PDF with no disposition and no filename must still be reachable.
			&imap.BodyStructureSinglePart{Type: "application", Subtype: "pdf", Encoding: "base64", Size: 100},
		},
	}
	o := Walk(bs)
	if len(o.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want the orphan pdf", o.Attachments)
	}
	if o.Attachments[0].Filename != "part-2.pdf" {
		t.Errorf("generated filename = %q, want part-2.pdf", o.Attachments[0].Filename)
	}
}

func TestWalkAttachmentContainerPropagates(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			textPart("plain", "utf-8", 10),
			&imap.BodyStructureMultiPart{
				Subtype: "alternative",
				Children: []imap.BodyStructure{
					textPart("plain", "utf-8", 30),
					textPart("html", "utf-8", 40),
				},
				Extended: &imap.BodyStructureMultiPartExt{
					Disposition: &imap.BodyStructureDisposition{Value: "attachment"},
				},
			},
		},
	}
	o := Walk(bs)
	if o.Plain == nil || o.Plain.Path[0] != 1 {
		t.Fatalf("plain = %+v, want the top-level text part", o.Plain)
	}
	if o.HTML != nil {
		t.Error("html inside attachment container must not become a body")
	}
	if len(o.Attachments) != 2 {
		t.Errorf("attachment container leaves = %+v, want both text children", o.Attachments)
	}
}

func TestWalkSecondTextPartBecomesAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			textPart("plain", "utf-8", 10),
			textPart("plain", "utf-8", 20),
		},
	}
	o := Walk(bs)
	if o.Plain == nil || len(o.Attachments) != 1 {
		t.Errorf("want first plain as body, second as attachment: %+v", o)
	}
}

func TestWalkNameParamTextIsAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			&imap.BodyStructureSinglePart{
				Type: "text", Subtype: "plain",
				Params:   map[string]string{"charset": "utf-8", "name": "notes.txt"},
				Encoding: "7bit", Size: 50,
			},
			textPart("plain", "utf-8", 10),
		},
	}
	o := Walk(bs)
	if o.Plain == nil || o.Plain.Path[0] != 2 {
		t.Errorf("plain = %+v, want the second (filename-less) part", o.Plain)
	}
	if len(o.Attachments) != 1 || o.Attachments[0].Filename != "notes.txt" {
		t.Errorf("attachments = %+v, want notes.txt", o.Attachments)
	}
}

func TestDecodeTextInvalidUTF8SetsFallback(t *testing.T) {
	res := DecodeText([]byte("ok \xC3\x28 bad"), "7bit", "utf-8", false, 0)
	if !res.CharsetFallback {
		t.Error("invalid utf-8 bytes replaced silently; want charset_fallback")
	}
	res = DecodeText([]byte("all fine"), "7bit", "", false, 0)
	if res.CharsetFallback {
		t.Error("clean ascii flagged as fallback")
	}
}

func TestWalkMessageRFC822IsOneAttachment(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			textPart("plain", "utf-8", 10),
			&imap.BodyStructureSinglePart{
				Type: "message", Subtype: "rfc822", Encoding: "7bit", Size: 999,
			},
		},
	}
	o := Walk(bs)
	if len(o.Attachments) != 1 || o.Attachments[0].ContentType != "message/rfc822" {
		t.Errorf("attachments = %+v, want single rfc822", o.Attachments)
	}
}

func TestDecodeTextQuotedPrintableWindows1252(t *testing.T) {
	// "café" in Windows-1252 quoted-printable: 0xE9 = é
	res := DecodeText([]byte("caf=E9"), "quoted-printable", "windows-1252", false, 0)
	if res.Text != "café" || res.CharsetFallback || res.Truncated {
		t.Errorf("got %+v, want café without flags", res)
	}
}

func TestDecodeTextUnknownCharsetFallsBack(t *testing.T) {
	res := DecodeText([]byte("hello \xff world"), "7bit", "x-no-such-charset", false, 0)
	if !res.CharsetFallback {
		t.Error("expected charset_fallback flag")
	}
	if !strings.Contains(res.Text, "hello") || !strings.Contains(res.Text, "world") {
		t.Errorf("text mangled: %q", res.Text)
	}
	if !strings.ContainsRune(res.Text, '�') {
		t.Errorf("invalid byte not replaced: %q", res.Text)
	}
}

func TestDecodeTextBase64PreTruncated(t *testing.T) {
	full := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	cut := full[:len(full)-3] // mid-quantum cut, as a Partial fetch would produce
	res := DecodeText([]byte(cut), "base64", "utf-8", true, 0)
	if !res.Truncated {
		t.Error("preTruncated must set Truncated")
	}
	if !strings.HasPrefix("0123456789abcdef", res.Text) || res.Text == "" {
		t.Errorf("best-effort decode = %q", res.Text)
	}
}

func TestDecodeTextCharCap(t *testing.T) {
	res := DecodeText([]byte(strings.Repeat("é", 100)), "7bit", "utf-8", false, 10)
	if !res.Truncated || len([]rune(res.Text)) != 10 {
		t.Errorf("got %d runes truncated=%v", len([]rune(res.Text)), res.Truncated)
	}
}

func TestDecodeBinaryBase64Wrapped(t *testing.T) {
	payload := []byte{0x25, 0x50, 0x44, 0x46, 0x00, 0x01, 0x02}
	enc := base64.StdEncoding.EncodeToString(payload)
	wrapped := enc[:5] + "\r\n" + enc[5:]
	got, err := DecodeBinary([]byte(wrapped), "base64")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("roundtrip mismatch: %v", got)
	}
}
