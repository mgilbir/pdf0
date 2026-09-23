package pdf0

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Tests for the byte-level PDF/A rules reading the document's source record
// (audit 2026-09-22 C69, C70, C71): what they judge is the file the document
// was read from, located where Read located it.

// withoutNoSourceFile drops the one checker finding a document built in memory
// is expected to carry — its byte-level rules did not run — so a test about
// its other findings can say "none".
func withoutNoSourceFile(vs []pdfa.Violation) []pdfa.Violation {
	var out []pdfa.Violation
	for _, v := range vs {
		if IsCheckerFinding(v) && strings.Contains(v.Message, core.GuardNoSourceFile) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func messagesOf(vs []pdfa.Violation) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Error()
	}
	return out
}

func findingsContaining(vs []pdfa.Violation, sub string) []pdfa.Violation {
	var out []pdfa.Violation
	for _, v := range vs {
		if strings.Contains(v.Message, sub) {
			out = append(out, v)
		}
	}
	return out
}

// TestAnInMemoryDocumentSaysItsByteRulesDidNotRun: a document built in memory
// has no file, so the byte-level rules cannot run; the result says so with one
// checker finding rather than passing in silence (audit 2026-09-22 C69). The
// same document written and read back has a file, and no findings.
func TestAnInMemoryDocumentSaysItsByteRulesDidNotRun(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		doc := mustPDFADoc(t, level)
		vs := ValidatePDFA(doc, level)
		if len(vs) != 1 || !IsCheckerFinding(vs[0]) || !strings.Contains(vs[0].Message, core.GuardNoSourceFile) {
			t.Errorf("%s in memory: want one %s checker finding, got %q", level, core.GuardNoSourceFile, messagesOf(vs))
		}
		if vs := ValidatePDFA(writeRead(t, doc), level); len(vs) != 0 {
			t.Errorf("%s written and read back: want no findings, got %q", level, messagesOf(vs))
		}
	}
}

// TestByteRulesJudgeTheFileTheDocumentWasReadFrom is C69's scenario. A caller
// reads a clean file, edits it and validates it. The byte rules used to take
// the bytes as a parameter and measure them against the offsets Read had
// recorded for the old file: given the rewritten file they reported a
// hexadecimal-string violation found in neither file, and given a shorter one
// an out-of-range slice turned every byte rule into one "internal" finding.
// Now they read the file the document was read from, and only it: its
// declared stream lengths too, so editing a stream's /Length in the graph
// changes nothing they say.
func TestByteRulesJudgeTheFileTheDocumentWasReadFrom(t *testing.T) {
	d := writeRead(t, mustPDFADoc(t, pdfa.PDFA2b))
	if vs := ValidatePDFA(d, pdfa.PDFA2b); len(vs) != 0 {
		t.Fatalf("the clean file has findings: %q", messagesOf(vs))
	}
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	cat.Set("Lang", object.String{Value: []byte("en-US-with-a-long-tag-to-shift-every-offset")})
	if vs := ValidatePDFA(d, pdfa.PDFA2b); len(vs) != 0 {
		t.Errorf("the edited document: want the file's verdict (clean), got %q", messagesOf(vs))
	}
	// The metadata stream's /Length in the graph no longer matches its data.
	// The model rule says so, once; the byte rule measures the file, whose
	// /Length is right, and says nothing.
	const lengthMsg = "the value of the Length key does not match the actual number of bytes in the stream"
	md := d.Resolve(cat.Get("Metadata")).(*object.Stream)
	md.Dict.Set("Length", object.Integer(3))
	if got := findingsContaining(ValidatePDFA(d, pdfa.PDFA2b), lengthMsg); len(got) != 1 {
		t.Errorf("an edited /Length: want the model rule's one finding, got %q", messagesOf(got))
	}
}

// streamFile is a PDF/A-2b-shaped file (a catalog and an empty page tree)
// whose object 3 is a stream written with the stream keyword directly after
// the dictionary's ">>" (legal: ">" is a delimiter), declaring length, with
// data "HELLO"; object 4, after it, is a stream with 11 bytes of data.
func streamFile(length int) []byte {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.obj(3, fmt.Sprintf("<< /Length %d >>stream\nHELLO\nendstream", length))
	b.obj(4, "<< /Length 11 >>\nstream\nHELLO WORLD\nendstream")
	b.xrefTable(b.inUse(1, 2, 3, 4), true, "/Root 1 0 R /ID [<0102> <0102>]")
	return b.bytes()
}

// TestStreamKeywordAfterADelimiter is C70: "/Length 5 >>stream" is legal, and
// the length check searched for a white-space-preceded "stream" from the
// object's offset, stepped over it, and measured the next object's stream
// instead. The length is measured from the keyword Read's parser took.
func TestStreamKeywordAfterADelimiter(t *testing.T) {
	const lengthMsg = "the value of the Length key does not match the actual number of bytes in the stream"
	d := readBytes(t, streamFile(5))
	if got := findingsContaining(ValidatePDFA(d, pdfa.PDFA2b), lengthMsg); len(got) != 0 {
		t.Errorf("a correct /Length after \">>stream\" was reported: %q", messagesOf(got))
	}
	if got := findingsContaining(ValidatePDFA(d, pdfa.PDFA2b), "stream keyword"); len(got) != 0 {
		t.Errorf("the \">>stream\" keyword's layout was reported: %q", messagesOf(got))
	}
	// A wrong /Length is reported, on object 3 and nowhere else: by the model
	// rule (the parser recovered the data by searching for endstream) and by
	// the byte rule, which measures the file.
	d = readBytes(t, streamFile(8))
	got := findingsContaining(ValidatePDFA(d, pdfa.PDFA2b), lengthMsg)
	if len(got) != 2 {
		t.Errorf("a wrong /Length after \">>stream\": want the model and byte findings, got %q", messagesOf(got))
	}
	for _, v := range got {
		if v.Object != 3 {
			t.Errorf("a wrong /Length on object 3 reported against object %d", v.Object)
		}
	}
}

// xrefWordFile is a file whose catalog carries the word "xref" in a string
// and whose page content mentions it in a comment. Its one table is
// well-formed; with bad, the table's xref keyword is followed by a space.
func xrefWordFile(t *testing.T, bad bool) []byte {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /Note (see the xref table) >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R >>")
	content := "% the xref table\n"
	b.obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	b.xrefTable(b.inUse(1, 2, 3, 4), true, "/Root 1 0 R /ID [<0102> <0102>]")
	data := b.bytes()
	if bad {
		// A space after the keyword; the keyword's own offset is unchanged,
		// and so is every entry, which points before it.
		i := bytes.LastIndex(data, []byte("\nxref\n"))
		if i < 0 {
			t.Fatal("no table in the built file")
		}
		data = append(data[:i+5:i+5], append([]byte(" "), data[i+5:]...)...)
	}
	return data
}

// TestTheWordXrefIsNotATable is C71: every delimited "xref" in the file was
// taken for a cross-reference table, so a string or a comment saying "the xref
// table" was reported under 6.1.4 at every level. Only the tables Read located
// are checked — and a malformed one still is.
func TestTheWordXrefIsNotATable(t *testing.T) {
	const eolMsg = "the xref keyword is not followed by a single EOL marker"
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA4} {
		d := readBytes(t, xrefWordFile(t, false))
		if got := findingsContaining(ValidatePDFA(d, level), "xref keyword"); len(got) != 0 {
			t.Errorf("%s: the word \"xref\" in a string and a comment was reported: %q", level, messagesOf(got))
		}
		d = readBytes(t, xrefWordFile(t, true))
		if got := findingsContaining(ValidatePDFA(d, level), eolMsg); len(got) != 1 {
			t.Errorf("%s: a table whose keyword is followed by a space: want one finding, got %q", level, messagesOf(got))
		}
	}
}

// linearizedFile is two revisions whose trailers carry different /ID first
// elements. With linearized, the first object in the file is a linearization
// dictionary; otherwise the word "/Linearized" appears only inside a string.
func linearizedFile(linearized bool) []byte {
	b := newFileBuilder()
	if linearized {
		b.obj(5, "<< /Linearized 1 >>")
	} else {
		b.obj(5, "<< /Note (/Linearized 1) >>")
	}
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.xrefTable(b.inUse(1, 2, 5), true, "/Root 1 0 R /ID [<1111> <1111>]")
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /Changed true >>")
	b.xrefTable(b.inUse(1), false, "/Root 1 0 R /ID [<2222> <2222>]")
	return b.bytes()
}

// TestLinearizedMeansTheFirstObject is C71's sibling: the linearized-trailer
// rule (ISO 19005-1 6.1.3) called a file linearized when "/Linearized"
// occurred anywhere in its bytes, and took every "trailer" in them for a
// trailer. An incrementally updated file whose revisions legitimately carry
// different /IDs was reported for a word in a string. A file is linearized
// when its first object is a linearization dictionary, and its trailers are
// those of the sections Read parsed.
func TestLinearizedMeansTheFirstObject(t *testing.T) {
	const msg = "linearized file: the file identifier /ID"
	if got := findingsContaining(ValidatePDFA(readBytes(t, linearizedFile(false)), pdfa.PDFA1b), msg); len(got) != 0 {
		t.Errorf("an updated file with \"/Linearized\" in a string was called linearized: %q", messagesOf(got))
	}
	if got := findingsContaining(ValidatePDFA(readBytes(t, linearizedFile(true)), pdfa.PDFA1b), msg); len(got) != 1 {
		t.Errorf("a linearized file whose trailers' /IDs differ: want one finding, got %q", messagesOf(got))
	}
}

// TestPDFA1FilesHaveNoCrossReferenceStreams: ISO 19005-1 6.1.4 (veraPDF
// 6.1.4-t03). PDF 1.4 has no cross-reference streams; the later parts allow
// them.
func TestPDFA1FilesHaveNoCrossReferenceStreams(t *testing.T) {
	const msg = "cross-reference stream"
	d := readBytes(t, xrefStreamHighFile())
	if got := findingsContaining(ValidatePDFA(d, pdfa.PDFA1b), msg); len(got) != 1 {
		t.Errorf("a PDF/A-1 file with a cross-reference stream: want one finding, got %q", messagesOf(got))
	}
	if got := findingsContaining(ValidatePDFA(d, pdfa.PDFA2b), msg); len(got) != 0 {
		t.Errorf("PDF/A-2 allows cross-reference streams: %q", messagesOf(got))
	}
	if got := findingsContaining(ValidatePDFA(readBytes(t, xrefWordFile(t, false)), pdfa.PDFA1b), msg); len(got) != 0 {
		t.Errorf("a file with only a table was reported: %q", messagesOf(got))
	}
}
