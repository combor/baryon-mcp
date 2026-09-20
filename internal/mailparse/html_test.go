package mailparse

import (
	"strings"
	"testing"
	"time"
)

func TestHTMLToText(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain text survives":   {"hello", "hello"},
		"tags are dropped":      {"<p>hello <b>there</b></p>", "hello there"},
		"entities are decoded":  {"caf&eacute; &amp; bar &#39;s", "café & bar 's"},
		"blocks become breaks":  {"<div>one</div><div>two</div>", "one\ntwo"},
		"br becomes a break":    {"one<br/>two", "one\ntwo"},
		"style is not text":     {"<style>.a{color:red}</style><p>body</p>", "body"},
		"script is not text":    {"<script>var x = 1 < 2;</script>hi", "hi"},
		"head is not text":      {"<html><head><title>t</title></head><body>hi</body></html>", "hi"},
		"comments are dropped":  {"a<!-- hidden -->b", "ab"},
		"quoted gt in attr":     {`<a title="a > b">link</a>`, "link"},
		"whitespace collapses":  {"<p>a   \t  b</p>", "a b"},
		"blank lines collapse":  {"<p>a</p><p></p><p></p><p>b</p>", "a\nb"},
		"unterminated tag ends": {"text<div", "text"},
		"empty input":           {"", ""},

		// A '<' with no name after it is a comparison, not a tag.
		"comparison survives":   {"<p>Pay if total < 10 USD</p>", "Pay if total < 10 USD"},
		"bare lt at the end":    {"<p>a <", "a <"},
		"stray angles are text": {"<<>>text", "<<>>text"},

		// head may leave its closing tag out, which must not cost the body.
		"implicit head close": {"<html><head><title>Receipt</title><body><p>Paid</p></body></html>", "Paid"},
		"doctype is dropped":  {"<!DOCTYPE html><p>hi</p>", "hi"},

		// An element written as empty has no content to skip.
		"self closed script":     {`<script src="track.js"/><p>Paid</p>`, "Paid"},
		"self closed with space": {`<style />` + "<p>Paid</p>", "Paid"},
		"slash in a quoted attr": {`<script src="a/b.js">x</script><p>Paid</p>`, "Paid"},

		// A "</" inside raw text must not be mistaken for the element's close.
		"lt slash inside style":  {`<style>/* </ */</style><p>Pay now</p>`, "Pay now"},
		"lt slash inside script": {`<script>var s = "</";</script><p>Pay now</p>`, "Pay now"},
		"longer name is not it":  {"<style>a</styles>b</style><p>hi</p>", "hi"},

		// Cells separate along a row; rows start a new line.
		"cells stay distinct": {"<table><tr><td>12</td><td>34</td></tr></table>", "12 34"},
		"rows break":          {"<table><tr><td>a</td></tr><tr><td>b</td></tr></table>", "a\nb"},
	}
	for name, tc := range cases {
		if got := HTMLToText(tc.in); got != tc.want {
			t.Errorf("%s: HTMLToText(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}

// Preheader padding is invisible, so it must not reach the preview at all.
func TestHTMLToTextDropsInvisiblePadding(t *testing.T) {
	// Soft hyphen, ZWNJ, combining grapheme joiner, ZWSP, word joiner, BOM.
	pad := strings.Repeat("\u00ad\u200c\u034f\u200b\u2060\ufeff ", 300)
	got := HTMLToText("<p>Ordered: a bed</p>" + pad + "<p>Arriving Tuesday</p>")

	if want := "Ordered: a bed\nArriving Tuesday"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// Dropping format characters must not reach the combining marks that carry
// real accents.
func TestHTMLToTextKeepsCombiningAccents(t *testing.T) {
	cases := map[string]string{
		"decomposed e-acute":  "cafe\u0301",  // e + COMBINING ACUTE ACCENT
		"decomposed o-umlaut": "scho\u0308n", // o + COMBINING DIAERESIS
		"devanagari":          "\u0939\u093f\u0928\u094d\u0926\u0940",
	}
	for name, in := range cases {
		if got := HTMLToText("<p>" + in + "</p>"); got != in {
			t.Errorf("%s: HTMLToText(%q) = %q, want it unchanged", name, in, got)
		}
	}
}

// A preview is shortened after stripping, so the budget has to buy readable
// text rather than markup.
func TestHTMLToTextRecoversBudgetFromMarkup(t *testing.T) {
	body := `<html><head><style>` + strings.Repeat(".cls{padding:0}", 200) +
		`</style></head><body><table><tr><td><div class="x" style="font-size:12px">` +
		`Your invoice is ready.</div></td></tr></table></body></html>`

	got := HTMLToText(body)
	if !strings.Contains(got, "Your invoice is ready.") {
		t.Fatalf("readable text lost: %q", got)
	}
	if len(got) > 100 {
		t.Errorf("markup survived stripping: %d chars, %q", len(got), got)
	}
}

// Skipping a raw element must stay linear: a sender controls this input, and a
// listing converts ten of them. The quadratic version of skipElement spent
// seconds on this body; the linear one is microseconds, so the bound is loose
// enough not to be timing-sensitive.
func TestHTMLToTextSkipsRawElementsLinearly(t *testing.T) {
	body := "<style>/*" + strings.Repeat("</", 30_000) + "*/</style><p>Pay now</p>"

	start := time.Now()
	got := HTMLToText(body)
	elapsed := time.Since(start)

	if got != "Pay now" {
		t.Errorf("body = %q, want %q", got, "Pay now")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %s on a %d byte body; the closing-tag search is not linear", elapsed, len(body))
	}
}

// Malformed markup is ordinary in email and must not cost the whole body.
func TestHTMLToTextToleratesMalformedMarkup(t *testing.T) {
	cases := []string{
		"<p>unclosed",
		"<script>never closed",
		"<!-- unterminated comment",
		"</p></div>stray closers",
		`<a href='single "quoted"'>x</a>`,
		"<html><head><style>.a{color:red}<body><p>hi</p>",
	}
	for _, in := range cases {
		if got := HTMLToText(in); strings.Contains(got, ">") {
			t.Errorf("HTMLToText(%q) = %q, still carries markup", in, got)
		}
	}
}
