package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// A link's URI is normalised the way a browser's URL parser reads it before
// its scheme is checked, the scheme must be one a link may carry, and what is
// written is 7-bit ASCII (audit 2026-09-22 C132).

func writtenURI(t *testing.T, uri string) (string, error) {
	t.Helper()
	d := NewDocument()
	ref, err := d.AddPage(Page{Width: 100, Height: 100, Content: square(),
		Links: []Link{{Rect: [4]float64{0, 0, 10, 10}, URI: uri}}})
	if err != nil {
		return "", err
	}
	annots, _ := d.Resolve(d.ResolveDict(ref).Get("Annots")).(object.Array)
	action := d.ResolveDict(d.ResolveDict(annots[0]).Get("A"))
	s, _ := action.Get("URI").(object.String)
	return string(s.Value), nil
}

// TestScriptURIsAreRefusedHoweverTheyAreSpelled is the audit's scenario. The
// WHATWG URL parser strips leading and trailing C0 controls and spaces and
// removes every tab and newline before it reads the scheme, so a browser
// handed "java\tscript:alert(1)" or " javascript:alert(1)" runs the script.
func TestScriptURIsAreRefusedHoweverTheyAreSpelled(t *testing.T) {
	for _, uri := range []string{
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"java\tscript:alert(1)",
		"java\nscript:alert(1)",
		"java\rscript:alert(1)",
		" javascript:alert(1)",
		"\x01\x02javascript:alert(1)",
		"\x00javascript:alert(1)",
		"javascript:alert(1) \x1f",
		"vbscript:msgbox(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"blob:https://example.com/uuid",
		"example.com/no-scheme",
		"",
		"   ",
		"https://example.com/\x00hidden",
		"https://example.com/\x7fdel",
		"ht\ttps://exa\nmple.com/", // a browser would read https://example.com/; refused, not guessed
	} {
		if _, err := writtenURI(t, uri); err == nil {
			t.Errorf("%q was accepted", uri)
		}
	}
}

// TestURIsAreWrittenAsABrowserReadsThem pins what is written for the URIs that
// are accepted: stripped as a browser strips them, with anything outside 7-bit printable
// ASCII — and the characters no URI may carry unescaped — percent-encoded as
// UTF-8 (ISO 32000-2 12.6.4.8 makes /URI 7-bit ASCII).
func TestURIsAreWrittenAsABrowserReadsThem(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/":              "https://example.com/",
		"  https://example.com/x \n":        "https://example.com/x",
		"HTTPS://Example.com/Path":          "HTTPS://Example.com/Path",
		"https://example.com/über?q=ß#frag": "https://example.com/%C3%BCber?q=%C3%9F#frag",
		"https://example.com/a b":           "https://example.com/a%20b",
		"https://example.com/%41":           "https://example.com/%41",
		"https://example.com/<\"x\">":       "https://example.com/%3C%22x%22%3E",
		"mailto:someone@example.com":        "mailto:someone@example.com",
		"http://example.com":                "http://example.com",
		"ftp://example.com/file":            "ftp://example.com/file",
		"tel:+44-20-7946-0000":              "tel:+44-20-7946-0000",
	} {
		got, err := writtenURI(t, in)
		if err != nil {
			t.Errorf("%q was refused: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q was written as %q, want %q", in, got, want)
		}
		for i := 0; i < len(got); i++ {
			if got[i] < 0x21 || got[i] > 0x7e {
				t.Errorf("%q was written with byte %#x, which is not 7-bit printable ASCII", in, got[i])
				break
			}
		}
	}
	if _, err := writtenURI(t, "javascript:x"); err == nil || !strings.Contains(err.Error(), "javascript") {
		t.Errorf("err = %v; it should name the scheme it refused", err)
	}
}
