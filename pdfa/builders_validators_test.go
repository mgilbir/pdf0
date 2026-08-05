package pdfa

import (
	"strings"
	"testing"
)

// TestPDFAPartConformanceLevelA pins the per-level builder metadata.
func TestPDFAPartConformanceLevelA(t *testing.T) {
	cases := []struct {
		level Level
		part  int
		conf  string
		ver   string
	}{
		{PDFA1a, 1, "A", "1.4"},
		{PDFA2a, 2, "A", "1.7"},
		{PDFA3a, 3, "A", "1.7"},
		{PDFA1b, 1, "B", "1.4"},
		{PDFA4, 4, "", "2.0"},
	}
	for _, c := range cases {
		if got := pdfaPart(c.level); got != c.part {
			t.Errorf("pdfaPart(%v) = %d, want %d", c.level, got, c.part)
		}
		if got := pdfaConformance(c.level); got != c.conf {
			t.Errorf("pdfaConformance(%v) = %q, want %q", c.level, got, c.conf)
		}
		if got := pdfaVersion(c.level); got != c.ver {
			t.Errorf("pdfaVersion(%v) = %q, want %q", c.level, got, c.ver)
		}
	}
}

// TestCanonicalPrefixSingleQuote is the C33 guard: a single-quoted xmlns
// declaration binding a canonical extension-schema namespace to a wrong prefix
// is flagged, not evaded.
func TestCanonicalPrefixSingleQuote(t *testing.T) {
	// pick any canonical namespace/prefix
	var uri, want string
	for u, w := range canonicalXMPPrefixes {
		uri, want = u, w
		break
	}
	if uri == "" {
		t.Skip("no canonical prefixes configured")
	}

	// Bind the namespace to a deliberately wrong prefix using single quotes.
	xmp := "<x xmlns:WRONG='" + uri + "'></x>"
	errs := checkXMPExtensionContainer(xmp, nil, "6.6.2", PDFA2b)
	flagged := false
	for _, e := range errs {
		if strings.Contains(e.Message, uri) && strings.Contains(e.Message, want) {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("single-quoted xmlns binding %s to a wrong prefix was not flagged", uri)
	}
}
