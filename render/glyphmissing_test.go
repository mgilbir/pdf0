package render

import "testing"

// The glyph-missing guardrail, and the characters it must not fire on.
//
// It is an Error by default, which is the severity that stops a document being
// produced, so a false positive here is not a nuisance — it is a refusal to
// render a page that would have been correct. It was firing on the no-break
// space, which is in a large share of all HTML ever written.

// glyphFindings counts the missing-glyph reports for a document.
func glyphFindings(t *testing.T, text string) []Finding {
	t.Helper()
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: "<p>" + text + "</p>",
		CSS:  []Stylesheet{{Source: "p { font-family: Helvetica }"}},
	})
	Layout(built.Root, A4.Content(), nil, rec)
	var out []Finding
	for _, f := range append(append([]Finding{}, built.Findings...), rec.Findings()...) {
		if f.Rule == RuleGlyphMissing {
			out = append(out, f)
		}
	}
	return out
}

func TestInvisibleCharactersAreNotMissingGlyphs(t *testing.T) {
	// None of these puts ink on the page, so a face without a glyph for one is
	// not a fault: the encoder substitutes a space, and for a character that was
	// never going to be seen that is either exactly right or wrong by a fraction
	// of an em.
	cases := []struct {
		name string
		text string
	}{
		{"no-break space", "a b"},
		{"en space", "a b"},
		{"em space", "a b"},
		{"thin space", "a b"},
		{"narrow no-break space", "a b"},
		{"ideographic space", "a　b"},
		{"zero-width space", "a\u200bb"},
		{"soft hyphen", "a\u00adb"},
		{"right-to-left override", "a\u202eb\u202c"},
		{"left-to-right mark", "a\u200eb"},
		{"byte order mark", "a\ufeffb"},
		{"word joiner", "a\u2060b"},
	}
	for _, tc := range cases {
		if got := glyphFindings(t, tc.text); len(got) != 0 {
			t.Errorf("%s was reported as a missing glyph: %s", tc.name, got[0].Message)
		}
	}
}

func TestAVanishedLetterIsStillReported(t *testing.T) {
	// The other half, and the reason the rule exists. A letter the face cannot
	// encode is *also* set as a space — so the word does not appear as a row of
	// boxes, it simply is not there, on the page or in the text extracted from
	// it. That has to be reported, or the guardrail has been traded away rather
	// than corrected.
	for _, tc := range []struct{ name, text string }{
		{"Hebrew", "shalom שלום"},
		{"Greek", "alpha α"},
		{"Hiragana", "kana あ"},
	} {
		got := glyphFindings(t, tc.text)
		if len(got) == 0 {
			t.Errorf("%s in a Latin-only face was not reported at all", tc.name)
			continue
		}
		// And the message has to say what actually happens. It used to promise a
		// blank box, which no reader would ever see: nothing in this engine
		// draws tofu, because the encoder's substitute for an unrepresentable
		// character is a space.
		if want := "set as a space"; !contains(got[0].Message, want) {
			t.Errorf("%s reported %q, which does not say that the character is %s",
				tc.name, got[0].Message, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
