package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// The validators judge the document the trailer reaches — direct dictionaries
// included, orphans excluded (audit 2026-09-22 C83). Each test below builds a
// file twice over: the construct written where producers write it (inline,
// inside another object), which a scan of the object table never saw, and the
// same construct as an object nothing refers to, which a scan of the object
// table judged although it is not part of the document.

// reachableFixture is a one-page document whose page carries annots (an
// /Annots array body) and whose catalog carries catalogExtra, plus extra
// objects from 10 up.
func reachableFixture(t *testing.T, annots, catalogExtra string, extra map[int]string) *Document {
	t.Helper()
	objs := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R " + catalogExtra + " >>",
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /TrimBox [0 0 100 100] /Annots [" + annots + "] >>",
	}
	for n, b := range extra {
		objs[n] = b
	}
	return readAssembled(t, objs)
}

func messages[T interface{ Error() string }](vs []T) string {
	var b strings.Builder
	for _, v := range vs {
		b.WriteString(v.Error())
		b.WriteString("\n")
	}
	return b.String()
}

func TestPDFXJudgesTheDocumentItReaches(t *testing.T) {
	// The audit's scenario: a Link whose JavaScript action is written inline.
	inline := reachableFixture(t, "<< /Type /Annot /Subtype /Link /Rect [0 0 1 1] /A << /S /JavaScript /JS (app.alert(1)) >> >>", "", nil)
	if got := messages(ValidatePDFX(inline, pdfx.PDFX4)); !strings.Contains(got, "JavaScript actions are not permitted (object 3)") {
		t.Errorf("an inline JavaScript action passed PDF/X:\n%s", got)
	}
	// The same action as an object nothing refers to is not the document's.
	orphan := reachableFixture(t, "", "", map[int]string{10: "<< /Type /Action /S /JavaScript /JS (x) >>"})
	if got := messages(ValidatePDFX(orphan, pdfx.PDFX4)); strings.Contains(got, "JavaScript") {
		t.Errorf("an orphan JavaScript action was judged:\n%s", got)
	}
}

func TestPDFUAJudgesTheDocumentItReaches(t *testing.T) {
	direct := reachableFixture(t, "<< /Type /Annot /Subtype /TrapNet /Rect [0 0 1 1] >>", "", nil)
	if got := messages(ValidatePDFUA(direct)); !strings.Contains(got, "[PDF/UA-1 7.18.2] object 3: TrapNet annotations are not permitted") {
		t.Errorf("a TrapNet annotation written in the page's /Annots was not judged:\n%s", got)
	}
	orphan := reachableFixture(t, "", "", map[int]string{10: "<< /Type /Annot /Subtype /TrapNet /Rect [0 0 1 1] >>"})
	if got := messages(ValidatePDFUA(orphan)); strings.Contains(got, "TrapNet") {
		t.Errorf("an orphan TrapNet annotation was judged:\n%s", got)
	}

	// A file specification written directly in the EmbeddedFiles name tree —
	// the shape most producers write — is held to 7.11.
	names := "/Names << /EmbeddedFiles << /Names [(a.txt) << /Type /Filespec /F (a.txt) /EF << /F 10 0 R >> >>] >> >>"
	fs := reachableFixture(t, "", names, map[int]string{10: "stream:/Type /EmbeddedFile|hello"})
	if got := messages(ValidatePDFUA(fs)); !strings.Contains(got, "[PDF/UA-1 7.11] object 1: embedded-file specification must have non-empty /F and /UF keys") {
		t.Errorf("a direct embedded-file specification without /UF was not judged:\n%s", got)
	}
}

func TestPDFAJudgesTheDocumentItReaches(t *testing.T) {
	// PDF/A-2 forbids a Movie annotation; an orphan one is not the document's.
	orphan := reachableFixture(t, "", "", map[int]string{10: "<< /Type /Annot /Subtype /Movie /Rect [0 0 10 10] /F 4 >>"})
	if got := messages(ValidatePDFA(orphan, pdfa.PDFA2b)); strings.Contains(got, "/Movie") {
		t.Errorf("an orphan Movie annotation was judged:\n%s", got)
	}
	reached := reachableFixture(t, "10 0 R", "", map[int]string{10: "<< /Type /Annot /Subtype /Movie /Rect [0 0 10 10] /F 4 >>"})
	if got := messages(ValidatePDFA(reached, pdfa.PDFA2b)); !strings.Contains(got, "annotation subtype /Movie is not allowed") {
		t.Errorf("a Movie annotation on the page was not judged:\n%s", got)
	}

	// A direct embedded-file specification without /UF: invisible to the
	// object-table scan, and a violation of 6.8 at PDF/A-2.
	names := "/Names << /EmbeddedFiles << /Names [(a.pdf) << /Type /Filespec /F (a.pdf) /EF << /F 10 0 R >> >>] >> >>"
	fs := reachableFixture(t, "", names, map[int]string{10: "stream:/Type /EmbeddedFile|hello"})
	if got := messages(ValidatePDFA(fs, pdfa.PDFA2b)); !strings.Contains(got, "[PDF/A-2b 6.8] object 1: filespec must have /UF") {
		t.Errorf("a direct embedded-file specification without /UF was not judged:\n%s", got)
	}
}

// TestPDFAAppearanceExemptionNeedsNoSize pins 6.3.3 t01 (PDF/A-1 6.5.3): an
// annotation is exempt from needing an appearance only when its rectangle has
// neither width nor height. The corpus's PDF_A-2b 6-3-3-t01-fail-p is a
// FileAttachment 50 points tall and 0 wide with no /AP — a failing file that
// the "either" reading exempted.
func TestPDFAAppearanceExemptionNeedsNoSize(t *testing.T) {
	for _, c := range []struct {
		rect string
		want bool
	}{
		{"[50 600 50 650]", true}, // no width, some height: not exempt
		{"[50 600 90 600]", true}, // some width, no height: not exempt
		{"[50 600 50 600]", false},
	} {
		d := reachableFixture(t, "<< /Type /Annot /Subtype /Square /Rect "+c.rect+" /F 4 >>", "", nil)
		got := strings.Contains(messages(ValidatePDFA(d, pdfa.PDFA2b)), "annotation must have /AP")
		if got != c.want {
			t.Errorf("/Rect %s: appearance required = %v, want %v", c.rect, got, c.want)
		}
	}
}

// TestPDFAFileNameKeysAreForEmbeddedFiles: /F and /UF are required of the
// specification of an embedded file (veraPDF: containsEF == false || (F !=
// null && UF != null)), not of one naming an external file, which a GoToR or
// Launch action writes inline. /AFRelationship is asked of every
// specification at PDF/A-3 and of an embedded file's only at PDF/A-4.
func TestPDFAFileNameKeysAreForEmbeddedFiles(t *testing.T) {
	external := reachableFixture(t, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /F 4 /A << /S /GoToR /F << /Type /Filespec /F (other.pdf) >> /D [0 /Fit] >> >>", "", nil)
	for _, lvl := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA4} {
		got := messages(ValidatePDFA(external, lvl))
		if strings.Contains(got, "filespec must have /UF") || strings.Contains(got, "filespec must have /AFRelationship") {
			t.Errorf("%v: an external file's specification was held to the embedded-file rules:\n%s", lvl, got)
		}
	}
	if got := messages(ValidatePDFA(external, pdfa.PDFA3b)); !strings.Contains(got, "filespec must have /AFRelationship") {
		t.Errorf("PDF/A-3b asks every file specification for /AFRelationship:\n%s", got)
	}
}
