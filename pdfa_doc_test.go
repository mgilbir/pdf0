package pdf0

import (
	"testing"

	"github.com/mgilbir/pdf0/pdfa"
)

// Building a PDF/A skeleton for a test, with its error checked.
//
// NewPDFADocument returns an error now. It is always nil today, and the point
// of it is that it will not have to be added the day it is not — but a test
// that ignored it would be the reason someone reached for a panic the last
// time. These check it and stop, which is what a test wants: the failure is the
// fixture, not the assertion, and a nil document would fail the next line in a
// way that says nothing about why.
//
// testing.TB rather than *testing.T so benchmarks and fuzz targets use the
// same two lines.

func mustPDFADoc(tb testing.TB, level pdfa.Level) *Document {
	tb.Helper()
	doc, err := NewPDFADocument(level)
	if err != nil {
		tb.Fatalf("building a %s skeleton: %v", level, err)
	}
	return doc
}

func mustPDFADocWithInfo(tb testing.TB, level pdfa.Level, title, author string) *Document {
	tb.Helper()
	doc, err := NewPDFADocumentWithInfo(level, title, author)
	if err != nil {
		tb.Fatalf("building a %s skeleton with info: %v", level, err)
	}
	return doc
}

func mustSRGBProfile(tb testing.TB) []byte {
	tb.Helper()
	p, err := pdfa.DefaultSRGBProfile()
	if err != nil {
		tb.Fatalf("the default sRGB profile: %v", err)
	}
	return p
}
