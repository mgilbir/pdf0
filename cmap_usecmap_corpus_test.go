package pdf0

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestCorpusTWGA025UseCMapIsReadNotSkipped pins a conforming corpus file
// whose embedded CMap builds on another: "/KSCms-UHC-H usecmap", with /UseCMap
// carrying KSCms-UHC-H itself, embedded. Its one shown code, <01>, is mapped
// only by that base's notdefrange.
//
// Every earlier reading got this wrong one way or the other. The substring
// reader refused the CMap and skipped the font's glyph checks without a word;
// recording that skip would have put a checker finding on a conforming file;
// reading the CMap without its base would have reported <01> as outside it.
// Read with its base, every code has a CID, nothing is skipped, and the file
// gains no finding of any kind — in particular no checker finding.
func TestCorpusTWGA025UseCMapIsReadNotSkipped(t *testing.T) {
	path := testfiles.VeraPDFCorpus.File(t, "TWG test files/TWG test suite A025-pdfa2-pass-a.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		for _, v := range ValidatePDFA(doc, level) {
			if IsCheckerFinding(v) {
				t.Errorf("%v: checker finding %s: %s", level, v.RuleID(), v.Error())
			}
			if strings.Contains(v.Error(), "glyph") || strings.Contains(v.Error(), "CIDSet") {
				t.Errorf("%v: %s: %s", level, v.RuleID(), v.Error())
			}
		}
	}
	for _, v := range ValidatePDFUA(doc) {
		if IsCheckerFinding(v) {
			t.Errorf("PDF/UA-1: checker finding %s: %s", v.RuleID(), v.Error())
		}
	}
}
