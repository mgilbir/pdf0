package pdf0

import (
	"bytes"
	"encoding/ascii85"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/pdfa"
)

// deviceRGBContent paints with DeviceRGB, which PDF/A-2b reports without an
// output intent (6.2.4.3): a finding that exists only if the content is read.
var deviceRGBContent = []byte("1 0 0 rg 0 0 10 10 re f")

func ascii85Of(b []byte) []byte {
	enc := make([]byte, ascii85.MaxEncodedLen(len(b)))
	return append(enc[:ascii85.Encode(enc, b)], '~', '>')
}

// runLengthOf encodes b as literal runs, then the end-of-data byte.
func runLengthOf(b []byte) []byte {
	var out []byte
	for len(b) > 0 {
		n := min(len(b), 128)
		out = append(out, byte(n-1))
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return append(out, 128)
}

// findingKeys is a report as a sorted list of "rule: message" lines.
func findingKeys(vs []pdfa.Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Rule+": "+v.Message)
	}
	sort.Strings(out)
	return out
}

// TestContentUnderEveryImplementedFilterIsChecked is audit 2026-09-22 C46 and
// C151. The same content, unfiltered, reports a DeviceRGB violation. Encoded
// with a filter pdf0 did not implement (ASCII85Decode — legal in every PDF/A
// part and what Ghostscript writes in front of Flate — or RunLengthDecode), with
// Flate whose Adler-32 checksum is missing, or with a predictor whose
// /DecodeParms is indirect, the content decoded to nothing and the finding
// vanished with no trip at all. Every one must now report exactly what the
// unfiltered stream reports.
func TestContentUnderEveryImplementedFilterIsChecked(t *testing.T) {
	baseline := findingKeys(ValidatePDFA(readRaw(t, minimalContentPDF(t, deviceRGBContent, false)), pdfa.PDFA2b))
	if len(baseline) == 0 || !strings.Contains(strings.Join(baseline, "\n"), "RGB") {
		t.Fatalf("the unfiltered control reports no DeviceRGB finding: %v", baseline)
	}
	flate := zlibBytes(deviceRGBContent)
	// One PNG row under the Sub filter (type 1): each byte stored as its
	// difference from the one before, which is not content until the
	// predictor is reversed — skipping it, as an unresolved /DecodeParms did,
	// leaves bytes that draw nothing.
	row := []byte{1}
	for i, c := range deviceRGBContent {
		prev := byte(0)
		if i > 0 {
			prev = deviceRGBContent[i-1]
		}
		row = append(row, c-prev)
	}
	predicted := zlibBytes(row)
	cases := map[string][]rawObj{
		"ASCII85Decode":                    onePage(rawObj{dict: "<</Filter/ASCII85Decode>>", stream: ascii85Of(deviceRGBContent)}, ""),
		"RunLengthDecode":                  onePage(rawObj{dict: "<</Filter/RunLengthDecode>>", stream: runLengthOf(deviceRGBContent)}, ""),
		"[ASCII85Decode FlateDecode]":      onePage(rawObj{dict: "<</Filter[/ASCII85Decode/FlateDecode]>>", stream: ascii85Of(flate)}, ""),
		"FlateDecode without its Adler-32": onePage(rawObj{dict: "<</Filter/FlateDecode>>", stream: flate[:len(flate)-4]}, ""),
		"FlateDecode with an indirect /DecodeParms": onePage(
			rawObj{dict: "<</Filter/FlateDecode/DecodeParms 5 0 R>>", stream: predicted}, "",
			rawObj{dict: "<</Predictor 12/Columns " + strconv.Itoa(len(deviceRGBContent)) + ">>"}),
		"indirect /Filter": onePage(rawObj{dict: "<</Filter 5 0 R>>", stream: flate}, "",
			rawObj{dict: "/FlateDecode"}),
	}
	for name, objs := range cases {
		got := findingKeys(ValidatePDFA(readRaw(t, buildRawPDF(objs)), pdfa.PDFA2b))
		if strings.Join(got, "\n") != strings.Join(baseline, "\n") {
			t.Errorf("%s: findings differ from the unfiltered content's\n got: %v\nwant: %v", name, got, baseline)
		}
	}
}

// TestContentOverTheDecodeLimitIsReported: 110 MB of content is over the
// 100 MB per-stream decode limit, and the decode failed into the same silent
// nil as a corrupt stream — no content rule ran and nothing said so (audit
// 2026-09-22 C46). The producer now records the trip.
func TestContentOverTheDecodeLimitIsReported(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 1536 << 20, Timeout: 2 * time.Minute}, func(t *testing.T) {
		unit := []byte("1 0 0 rg 0 0 1 1 re f\n")
		content := bytes.Repeat(unit, (110<<20)/len(unit))
		file := minimalContentPDF(t, content, true)
		content = nil
		vs := ValidatePDFA(readRaw(t, file), pdfa.PDFA2b)
		var limit bool
		for _, v := range vs {
			if v.Rule == "limit" && strings.Contains(v.Message, core.GuardDecodedStream) && IsCheckerFinding(v) {
				limit = true
			}
		}
		if !limit {
			t.Fatalf("no %s finding for a 110 MB content stream: %v", core.GuardDecodedStream, findingKeys(vs))
		}
	})
}

// TestCappedXRefStreamIsReported is audit 2026-09-22 C47's third case: a
// cross-reference stream over the decode limit made Read rebuild the table by
// scanning, silently. The rebuild is kept — it is what makes the file readable
// — and the reason for it is now reported.
func TestCappedXRefStreamIsReported(t *testing.T) {
	head, offs := numberedPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	xoff := len(head)
	var ent []byte
	ent = append(ent, 0, 0, 0, 0, 255)
	for i := 1; i <= 2; i++ {
		ent = append(ent, 1)
		ent = append(ent, be(offs[i], 3)...)
		ent = append(ent, 0)
	}
	ent = append(ent, 1)
	ent = append(ent, be(xoff, 3)...)
	ent = append(ent, 0)
	enc := zlibBytes(ent)
	var b bytes.Buffer
	b.Write(head)
	fmt.Fprintf(&b, "3 0 obj\n<< /Type /XRef /Size 4 /W [1 3 1] /Root 1 0 R /Filter /FlateDecode /Length %d >>\nstream\n", len(enc))
	b.Write(enc)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xoff)
	file := b.Bytes()

	if vs := ValidatePDFA(readRaw(t, file), pdfa.PDFA2b); hasFinding(vs, "limit", "") {
		t.Fatalf("control: a limit finding under the default limits: %v", findingKeys(vs))
	}
	doc := readRaw(t, file, WithMaxDecodedStreamBytes(8))
	if !doc.Source().Rebuilt() {
		t.Fatal("the table was not rebuilt; the fixture does not exercise the cap")
	}
	if vs := ValidatePDFA(doc, pdfa.PDFA2b); !hasFinding(vs, "limit", "cross-reference stream") {
		t.Errorf("no finding says why the table was rebuilt: %v", findingKeys(vs))
	}
}
