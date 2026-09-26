package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// TestFindingsAreInvariantUnderIndirection is the corpus-scale guard for the
// class behind audit 2026-09-22 C36 (and C18 before it): a validator that
// reads a value without resolving it answers differently for a file that
// writes the same value through an indirect reference. ISO 32000 permits that
// for almost every value, so the two files are the same document and must get
// the same findings.
//
// Every veraPDF corpus file is validated twice — as read, and after
// indirectify has rewritten every direct value it holds (dictionary entries,
// array elements, stream-dictionary entries) as an object of its own — by
// every validator: PDF/A at the file's own level, PDF/UA-1 and -2, PDF/X-4
// and -1a, PDF/VT, PDF/R and DPart. The two sets of findings are compared as
// rule and message, with every number masked, since the rewrite adds objects.
//
// A difference is a bug somewhere. The ones that remain are named in
// indirectionAllowlist with the finding that owns them; the test fails for a
// difference not listed and for a listed one that has gone, so the list can
// only shrink. Planting any one of the C36 bugs back makes it fail.
func TestFindingsAreInvariantUnderIndirection(t *testing.T) {
	corpusDir := corpusRoot(t)
	files := corpusTestFiles(t, "")
	type result struct{ diffs []string }
	results := make([]result, len(files))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < min(4, runtime.GOMAXPROCS(0)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i].diffs = indirectionDiffs(files[i], corpusDir)
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()

	got := map[string]bool{}
	for _, r := range results {
		for _, d := range r.diffs {
			got[d] = true
		}
	}
	var problems []string
	for d := range got {
		if _, ok := indirectionAllowlist[d]; !ok {
			problems = append(problems, "not invariant under indirection (unlisted): "+d)
		}
	}
	for d, owner := range indirectionAllowlist {
		if !got[d] {
			problems = append(problems, fmt.Sprintf("listed for %s, now invariant; remove it from indirectionAllowlist: %s", owner, d))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	t.Logf("indirection invariance: %d files, %d differences, all allowlisted: %v", len(files), len(got), len(problems) == 0)
}

var numberRun = regexp.MustCompile(`[0-9]+`)

// indirectionDiffs validates one file as read and rewritten, and returns each
// finding one side has more of than the other, as
// "file|validator|+ or -|finding with numbers masked" (+: only after the
// rewrite).
func indirectionDiffs(path, corpusDir string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{path + "|read|" + err.Error()}
	}
	rel, _ := filepath.Rel(corpusDir, path)
	suite := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	findings := func(rewrite bool) map[string]int {
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil
		}
		if rewrite {
			indirectify(doc)
		}
		out := map[string]int{}
		add := func(label string, errs []error) {
			for _, e := range errs {
				out[label+"|"+numberRun.ReplaceAllString(strings.ReplaceAll(e.Error(), "\n", " "), "N")]++
			}
		}
		add("pdfa", asErrors(ValidatePDFA(doc, indirectionLevel(suite))))
		add("ua1", asErrors(ValidatePDFUA(doc)))
		add("ua2", asErrors(ValidatePDFUA2(doc)))
		add("x4", asErrors(ValidatePDFX(doc, pdfx.PDFX4)))
		add("x1a", asErrors(ValidatePDFX(doc, pdfx.PDFX1a)))
		add("vt", asErrors(ValidatePDFVT(doc)))
		add("r", asErrors(ValidatePDFR(doc)))
		add("dpart", asErrors(ValidateDParts(doc)))
		return out
	}
	before, after := findings(false), findings(true)
	var out []string
	for k, n := range before {
		if after[k] < n {
			out = append(out, rel+"|"+strings.Replace(k, "|", "|-|", 1))
		}
	}
	for k, n := range after {
		if before[k] < n {
			out = append(out, rel+"|"+strings.Replace(k, "|", "|+|", 1))
		}
	}
	return out
}

func asErrors[T error](v []T) []error {
	out := make([]error, len(v))
	for i, e := range v {
		out[i] = e
	}
	return out
}

// indirectionLevel is the PDF/A level a corpus suite is validated at: its own,
// PDF/A-1b for Isartor, and PDF/A-4 for the suites that are not PDF/A ones.
func indirectionLevel(suite string) pdfa.Level {
	switch suite {
	case "Isartor test files":
		return pdfa.PDFA1b
	case "PDF_UA-1":
		return pdfa.PDFA2b
	}
	name := strings.ToLower(strings.TrimPrefix(suite, "PDF_A-"))
	for _, l := range pdfa.Levels() {
		if strings.TrimPrefix(strings.ToLower(l.String()), "pdf/a-") == name {
			return l
		}
	}
	return pdfa.PDFA4
}

// indirectStreamKeys are the stream-dictionary entries indirectify leaves
// alone: the ones the stream's own decoding reads, which ISO 32000 lets be
// indirect only for /Length, and which the decode chain declines to follow
// through a reference (audit 2026-09-22 C151) — a stream that no longer
// decodes is a different document, not the same one written differently.
var indirectStreamKeys = map[object.Name]bool{
	"Length": true, "Filter": true, "DecodeParms": true, "DL": true,
	"F": true, "FFilter": true, "FDecodeParms": true,
}

// indirectify rewrites every direct value in the document — each dictionary
// entry, array element and stream-dictionary entry, recursively — as an
// indirect object of its own, leaving the trailer and the stream-decoding
// entries alone.
func indirectify(doc *Document) {
	next := 1
	nums := make([]int, 0, len(doc.Objects))
	for n := range doc.Objects {
		nums = append(nums, n)
		if n >= next {
			next = n + 1
		}
	}
	sort.Ints(nums)
	var walk func(o object.Object, depth int)
	lift := func(o object.Object, depth int) object.Object {
		switch o.(type) {
		case nil, object.IndirectRef, object.Null:
			return o
		}
		walk(o, depth+1)
		n := next
		next++
		doc.Objects[n] = &object.IndirectObject{Number: n, Value: o}
		return object.IndirectRef{Number: n}
	}
	liftDict := func(d *object.Dictionary, skip map[object.Name]bool, depth int) {
		var keys []object.Name
		for k := range d.Keys() {
			keys = append(keys, k)
		}
		for _, k := range keys {
			if !skip[k] {
				d.Set(k, lift(d.Get(k), depth))
			}
		}
	}
	walk = func(o object.Object, depth int) {
		if depth > 40 {
			return
		}
		switch v := o.(type) {
		case *object.Dictionary:
			liftDict(v, nil, depth)
		case object.Array:
			for i := range v {
				v[i] = lift(v[i], depth)
			}
		case *object.Stream:
			liftDict(&v.Dict, indirectStreamKeys, depth)
		}
	}
	for _, n := range nums {
		walk(doc.Objects[n].Value, 0)
	}
}

// indirectionAllowlist: each difference the rewrite still makes, and the
// finding that owns it. Burn it down.
var indirectionAllowlist = map[string]string{
	// Separation tint-transform consistency compares two transforms with
	// object.Equal, which compares a nested reference by its number rather
	// than by what it names, so two identical functions whose sub-values are
	// separate objects read as different (audit 2026-09-22 C66, PR 14).
	"PDF_A-2b/6.2 Graphics/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-fail-d.pdf|pdfa|+|[PDF/A-Nb N.N.N.N] object N: Separation colorant /Blue has inconsistent tint transforms (objects N and N)":                    "C66",
	"PDF_A-2b/6.2 Graphics/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-fail-d.pdf|pdfa|+|[PDF/A-Nb N.N.N.N] object N: Separation colorant /Red has inconsistent tint transforms (objects N and N)":                     "C66",
	"PDF_A-2b/6.2 Graphics/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-pass-a.pdf|pdfa|+|[PDF/A-Nb N.N.N.N] object N: Separation colorant /Red has inconsistent tint transforms (objects N and N)":                     "C66",
	"PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-fail-b.pdf|pdfa|+|[PDF/A-N N.N.N.N] object N: Separation colorant /Red has inconsistent tint transforms (objects N and N)":   "C66",
	"PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-fail-d.pdf|pdfa|+|[PDF/A-N N.N.N.N] object N: Separation colorant /Blue has inconsistent tint transforms (objects N and N)":  "C66",
	"PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-fail-d.pdf|pdfa|+|[PDF/A-N N.N.N.N] object N: Separation colorant /Green has inconsistent tint transforms (objects N and N)": "C66",
	"PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-fail-d.pdf|pdfa|+|[PDF/A-N N.N.N.N] object N: Separation colorant /Red has inconsistent tint transforms (objects N and N)":   "C66",
	"PDF_A-4/6.2 Graphics/6.2.4 Colour spaces/6.2.4.4 Separation and DeviceN colour spaces/veraPDF test suite 6-2-4-4-t03-pass-a.pdf|pdfa|+|[PDF/A-N N.N.N.N] object N: Separation colorant /Red has inconsistent tint transforms (objects N and N)":   "C66",

	// PDF/A's forbidden-action scan walks doc.Objects and each dictionary's
	// /A, so an action dictionary written directly inside another (here a
	// Rendition action in an annotation's /AA) is never seen (the C83 class:
	// object scans instead of reachable walks, PR 16).
	"PDF_UA-1/7.18 Annotations/7.18.6 Media/7.18.6.2 Media clip data/7.18.6.2-t01-fail-a.pdf|pdfa|+|[PDF/A-Nb N.N.N] object N: forbidden action type /Rendition": "C83",
	"PDF_UA-1/7.18 Annotations/7.18.6 Media/7.18.6.2 Media clip data/7.18.6.2-t01-pass-a.pdf|pdfa|+|[PDF/A-Nb N.N.N] object N: forbidden action type /Rendition": "C83",
	"PDF_UA-1/7.18 Annotations/7.18.6 Media/7.18.6.2 Media clip data/7.18.6.2-t02-fail-a.pdf|pdfa|+|[PDF/A-Nb N.N.N] object N: forbidden action type /Rendition": "C83",
	"PDF_UA-1/7.18 Annotations/7.18.6 Media/7.18.6.2 Media clip data/7.18.6.2-t02-fail-b.pdf|pdfa|+|[PDF/A-Nb N.N.N] object N: forbidden action type /Rendition": "C83",
	"PDF_UA-1/7.18 Annotations/7.18.6 Media/7.18.6.2 Media clip data/7.18.6.2-t02-pass-a.pdf|pdfa|+|[PDF/A-Nb N.N.N] object N: forbidden action type /Rendition": "C83",

	// PDF/UA's annotation rules iterate doc.Objects, so an annotation written
	// directly in a page's /Annots is never seen (audit 2026-09-22 C83,
	// PR 16).
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-c.pdf|ua1|+|[PDF/UA-N N.N.N] object N: annotation is not tagged (no /StructParent linking it to the structure tree)":                                    "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-c.pdf|ua1|+|[PDF/UA-N N.N.N] object N: annotation of subtype /Circle has no alternate description (/Contents or /Alt)":                                  "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-c.pdf|ua2|+|[PDF/UA-N N.N.N] object N: annotation is not tagged (no /StructParent linking it to the structure tree)":                                    "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-c.pdf|ua2|+|[PDF/UA-N N.N.N] object N: annotation of subtype /Circle has no alternate description (/Contents or /Alt)":                                  "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.3 Colour spaces/6.2.3.3 Uncalibrated colour spaces/isartor-6-2-3-3-t02-fail-j.pdf|ua1|+|[PDF/UA-N N.N.N] object N: annotation is not tagged (no /StructParent linking it to the structure tree)":   "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.3 Colour spaces/6.2.3.3 Uncalibrated colour spaces/isartor-6-2-3-3-t02-fail-j.pdf|ua1|+|[PDF/UA-N N.N.N] object N: annotation of subtype /Circle has no alternate description (/Contents or /Alt)": "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.3 Colour spaces/6.2.3.3 Uncalibrated colour spaces/isartor-6-2-3-3-t02-fail-j.pdf|ua2|+|[PDF/UA-N N.N.N] object N: annotation is not tagged (no /StructParent linking it to the structure tree)":   "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.3 Colour spaces/6.2.3.3 Uncalibrated colour spaces/isartor-6-2-3-3-t02-fail-j.pdf|ua2|+|[PDF/UA-N N.N.N] object N: annotation of subtype /Circle has no alternate description (/Contents or /Alt)": "C83",

	// PDF/X's (and so PDF/VT's) ExtGState transfer-function rule iterates
	// doc.Objects, so a direct ExtGState dictionary inside /Resources is
	// never seen (audit 2026-09-22 C83, PR 16).
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t01-fail-a.pdf|vt|+|PDF/VT-N pdfx-N/forbidden: a transfer function (ExtGState /TR) is not permitted (object N)":  "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t01-fail-a.pdf|x1a|+|PDF/X forbidden: a transfer function (ExtGState /TR) is not permitted (object N)":           "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t01-fail-a.pdf|x4|+|PDF/X forbidden: a transfer function (ExtGState /TR) is not permitted (object N)":            "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t01-fail-b.pdf|vt|+|PDF/VT-N pdfx-N/forbidden: a transfer function (ExtGState /TR) is not permitted (object N)":  "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t01-fail-b.pdf|x1a|+|PDF/X forbidden: a transfer function (ExtGState /TR) is not permitted (object N)":           "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t01-fail-b.pdf|x4|+|PDF/X forbidden: a transfer function (ExtGState /TR) is not permitted (object N)":            "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t02-fail-a.pdf|vt|+|PDF/VT-N pdfx-N/forbidden: a transfer function (ExtGState /TRN) is not permitted (object N)": "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t02-fail-a.pdf|x1a|+|PDF/X forbidden: a transfer function (ExtGState /TRN) is not permitted (object N)":          "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t02-fail-a.pdf|x4|+|PDF/X forbidden: a transfer function (ExtGState /TRN) is not permitted (object N)":           "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t02-fail-b.pdf|vt|+|PDF/VT-N pdfx-N/forbidden: a transfer function (ExtGState /TRN) is not permitted (object N)": "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t02-fail-b.pdf|x1a|+|PDF/X forbidden: a transfer function (ExtGState /TRN) is not permitted (object N)":          "C83",
	"Isartor test files/PDFA-1b/6.2 Graphics/6.2.8 Extended graphics state/isartor-6-2-8-t02-fail-b.pdf|x4|+|PDF/X forbidden: a transfer function (ExtGState /TRN) is not permitted (object N)":           "C83",
}
