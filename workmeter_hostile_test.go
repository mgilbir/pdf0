package pdf0

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// The audit 2026-09-22 T1 repros, each a regression test (C10, C14, C17, C18,
// C19, C37-C42, C50-C53, C79, C84).
//
// Every one multiplied a unit of work that was bounded — one /W range, one
// RoleMap chain, one PostScript evaluation, one bfrange — by something the file
// controls, and ran for minutes, ran out of memory or overflowed the stack. Each
// test runs the repro in a capped child (internal/hostile) and asserts two
// things:
//
//   - a bounded outcome: the correct result, or the "limit" finding, well
//     inside the child's memory and time caps;
//   - that a deadline is honoured: the Context variant with a short deadline
//     returns within a small multiple of it, whatever work the file asks for.
//
// The inputs are built in code; the sizes are the audit's, or the smallest
// that took seconds before the fix.

// hostileWorkLimits is the child's cap for these tests: well above what a
// bounded run of any of them needs, and far below what they took unbounded.
var hostileWorkLimits = hostile.Limits{MaxRSS: 768 << 20, Timeout: 90 * time.Second}

// deadline is the context deadline the tests give a run, and deadlineSlack
// how long after it the run may return. A run that honours cancellation
// returns within one poll of the meter or one decoded megabyte of it.
const (
	deadline      = 250 * time.Millisecond
	deadlineSlack = 2 * time.Second
)

// honoursDeadline runs op under a context with a short deadline and fails if
// it returns much later than the deadline.
func honoursDeadline(t *testing.T, name string, op func(ctx context.Context)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start := time.Now()
	op(ctx)
	if el := time.Since(start); el > deadline+deadlineSlack*time.Duration(hostile.RaceScale) {
		t.Errorf("%s: returned %v after a %v deadline", name, el.Round(time.Millisecond), deadline)
	}
}

// anyViolation is what the tests read off any validator's result.
type anyViolation interface {
	Error() string
	RuleID() string
}

func findingMessages[T anyViolation](v []T) []string {
	out := make([]string, len(v))
	for i, e := range v {
		out[i] = e.RuleID() + " " + e.Error()
	}
	return out
}

// noCheckerFindings fails when a run that should have completed reports that
// it did not, or that a check panicked.
func noCheckerFindings(t *testing.T, what string, msgs []string) {
	t.Helper()
	for _, m := range msgs {
		if strings.HasPrefix(m, "limit ") || strings.HasPrefix(m, "internal ") {
			t.Errorf("%s: %s", what, m)
		}
	}
}

// countContaining counts the messages containing s.
func countContaining(msgs []string, s string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m, s) {
			n++
		}
	}
	return n
}

// timed runs op and fails if it takes longer than bound: the structural fixes
// make these runs fast, and a regression that brings back the quadratic is a
// timeout long before it is an out-of-memory.
func timed(t *testing.T, what string, bound time.Duration, op func()) {
	t.Helper()
	start := time.Now()
	op()
	if el := time.Since(start); el > bound*time.Duration(hostile.RaceScale) {
		t.Errorf("%s took %v, over %v", what, el.Round(time.Millisecond), bound)
	}
}

func structRaw(root string, extra ...string) []byte {
	objs := []rawObj{
		{dict: "<< /Type /Catalog /Pages 2 0 R /StructTreeRoot 4 0 R /MarkInfo << /Marked true >> /Lang (en) >>"},
		{dict: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{dict: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1 1] >>"},
		{dict: root},
	}
	for _, e := range extra {
		objs = append(objs, rawObj{dict: e})
	}
	return buildRawPDF(objs)
}

// C17: a DPM array that contains itself overflowed the stack, and a DAG of
// arrays each naming the next twice cost 2^depth.
func TestHostileDPMCycleAndDAG(t *testing.T) {
	dparts := func(dpm ...string) []byte {
		objs := []rawObj{
			{dict: "<< /Type /Catalog /Pages 2 0 R /DPartRoot 4 0 R >>"},
			{dict: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
			{dict: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /DPart 5 0 R >>"},
			{dict: "<< /Type /DPartRoot /DPartRootNode 5 0 R >>"},
			{dict: "<< /Type /DPart /Parent 4 0 R /Start 3 0 R /DPM << /A 6 0 R >> >>"},
		}
		for _, d := range dpm {
			objs = append(objs, rawObj{dict: d})
		}
		return buildRawPDF(objs)
	}
	t.Run("cycle", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			doc := readRaw(t, dparts("[6 0 R]"))
			noCheckerFindings(t, "ValidateDParts", findingMessages(ValidateDParts(doc)))
			noCheckerFindings(t, "ValidatePDFVT", findingMessages(ValidatePDFVT(doc)))
		})
	})
	t.Run("dag", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			var dag []string
			for i := 0; i < 60; i++ {
				dag = append(dag, fmt.Sprintf("[%d 0 R %d 0 R]", 7+i, 7+i))
			}
			// The leaf is a name, which a DPM may not hold: the finding
			// proves the walk reached the bottom.
			dag = append(dag, "[/NotAllowed]")
			doc := readRaw(t, dparts(dag...))
			var msgs []string
			timed(t, "a 60-deep DPM DAG", 5*time.Second, func() { msgs = findingMessages(ValidateDParts(doc)) })
			noCheckerFindings(t, "ValidateDParts", msgs)
			if countContaining(msgs, "DPM value of type object.Name") != 1 {
				t.Errorf("the name at the bottom of the DAG was reported %d times, want once: %v", countContaining(msgs, "object.Name"), msgs)
			}
			honoursDeadline(t, "ValidateDPartsContext", func(ctx context.Context) { ValidateDPartsContext(ctx, doc) })
		})
	})
}

// C18: N elements each naming one shared /K array of all N took quadratic
// memory: out of memory at N = 10,000.
func TestHostileSharedKArray(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const n = 10000
		var arr strings.Builder
		var elems []string
		for i := 0; i < n; i++ {
			fmt.Fprintf(&arr, "%d 0 R ", 6+i)
		}
		elems = append(elems, "["+arr.String()+"]")
		for i := 0; i < n; i++ {
			elems = append(elems, "<< /Type /StructElem /S /P /P 4 0 R /K 5 0 R >>")
		}
		doc := readRaw(t, structRaw("<< /Type /StructTreeRoot /K 5 0 R >>", elems...))
		var ua, a []string
		timed(t, "PDF/UA over a shared /K array", 20*time.Second, func() { ua = findingMessages(ValidatePDFUA(doc)) })
		timed(t, "PDF/A-2a over a shared /K array", 20*time.Second, func() { a = findingMessages(ValidatePDFA(doc, pdfa.PDFA2a)) })
		noCheckerFindings(t, "PDF/UA", ua)
		noCheckerFindings(t, "PDF/A-2a", a)
		honoursDeadline(t, "ValidatePDFUAContext", func(ctx context.Context) { ValidatePDFUAContext(ctx, doc) })
		honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA2a) })
	})
}

// C41: a 100,000-long RoleMap chain and 2,000 elements tagged with its first
// type walked the chain once per element: 92 seconds.
func TestHostileRoleMapChain(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const k, m = 100000, 2000
		var rm, kids strings.Builder
		for i := 0; i < k-1; i++ {
			fmt.Fprintf(&rm, "/T%d /T%d ", i, i+1)
		}
		var elems []string
		for i := 0; i < m; i++ {
			fmt.Fprintf(&kids, "%d 0 R ", 5+i)
			elems = append(elems, "<< /Type /StructElem /S /T0 /P 4 0 R >>")
		}
		doc := readRaw(t, structRaw(fmt.Sprintf("<< /Type /StructTreeRoot /RoleMap << %s >> /K [%s] >>", rm.String(), kids.String()), elems...))
		var msgs []string
		timed(t, "PDF/UA over a long role-map chain", 10*time.Second, func() { msgs = findingMessages(ValidatePDFUA(doc)) })
		noCheckerFindings(t, "PDF/UA", msgs)
		honoursDeadline(t, "ValidatePDFUAContext", func(ctx context.Context) { ValidatePDFUAContext(ctx, doc) })
	})
}

// C42: 40,000 leaves each spanning all 40,000 pages filled the coverage page by
// page: 38 seconds.
func TestHostileDPartLeavesTimesPages(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const n = 40000
		firstPage, firstLeaf := 5, 5+n
		var kids, leaves strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&kids, "%d 0 R ", firstPage+i)
			fmt.Fprintf(&leaves, "%d 0 R ", firstLeaf+i)
		}
		objs := []rawObj{
			{dict: "<< /Type /Catalog /Pages 2 0 R /DPartRoot 3 0 R >>"},
			{dict: fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), n)},
			{dict: "<< /Type /DPartRoot /DPartRootNode 4 0 R >>"},
			{dict: fmt.Sprintf("<< /Type /DPart /Parent 3 0 R /DParts [[%s]] >>", leaves.String())},
		}
		for i := 0; i < n; i++ {
			objs = append(objs, rawObj{dict: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1 1] >>"})
		}
		for i := 0; i < n; i++ {
			objs = append(objs, rawObj{dict: fmt.Sprintf("<< /Type /DPart /Parent 4 0 R /Start %d 0 R /End %d 0 R >>", firstPage, firstPage+n-1)})
		}
		doc := readRaw(t, buildRawPDF(objs))
		var msgs []string
		timed(t, "ValidateDParts over overlapping leaves", 10*time.Second, func() { msgs = findingMessages(ValidateDParts(doc)) })
		noCheckerFindings(t, "ValidateDParts", msgs)
		// Every page is covered by every leaf, and every leaf but the first
		// restarts at page 1: the findings the page-by-page fill made.
		if got := countContaining(msgs, "page is included in more than one DPart leaf range"); got != n {
			t.Errorf("%d pages reported in more than one leaf, want %d", got, n)
		}
		if got := countContaining(msgs, "not contiguous"); got != n-1 {
			t.Errorf("%d leaves reported not contiguous, want %d", got, n-1)
		}
		honoursDeadline(t, "ValidateDPartsContext", func(ctx context.Context) { ValidateDPartsContext(ctx, doc) })
	})
}

// C84: walkers over file structure recursed once per level. A 100,000-element
// structure chain overflowed a 64 MB stack.
func TestHostileDeepStructure(t *testing.T) {
	t.Run("structure chain", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			const n = 100000
			var elems []string
			for i := 0; i < n; i++ {
				k := ""
				if i < n-1 {
					k = fmt.Sprintf("/K %d 0 R", 6+i)
				}
				elems = append(elems, fmt.Sprintf("<< /S /P /P %d 0 R %s >>", 4+i, k))
			}
			doc := readRaw(t, structRaw("<< /Type /StructTreeRoot /K 5 0 R >>", elems...))
			// The structure walks are iterative, so a deep chain is read
			// whole: no stack overflow, and nothing left unchecked.
			noCheckerFindings(t, "PDF/UA", findingMessages(ValidatePDFUA(doc)))
			noCheckerFindings(t, "PDF/A-2a", findingMessages(ValidatePDFA(doc, pdfa.PDFA2a)))
		})
	})
	t.Run("page tree", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			// A page tree 5,000 /Pages nodes deep: recursive by nature, so the
			// walk is held to the uniform depth guard, and the run says so.
			const n = 5000
			objs := []rawObj{{dict: "<< /Type /Catalog /Pages 2 0 R >>"}}
			for i := 0; i < n; i++ {
				objs = append(objs, rawObj{dict: fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", 3+i)})
			}
			objs = append(objs, rawObj{dict: "<< /Type /Page /MediaBox [0 0 1 1] >>"})
			doc := readRaw(t, buildRawPDF(objs))
			msgs := findingMessages(ValidatePDFA(doc, pdfa.PDFA2b))
			if countContaining(msgs, "("+core.GuardWalkDepth+")") != 1 {
				t.Errorf("a 5,000-deep page tree: no %s limit finding in %v", core.GuardWalkDepth, msgs)
			}
			if _, err := doc.ExtractText(); !errors.Is(err, core.ErrWorkLimit) {
				t.Errorf("ExtractText over a 5,000-deep page tree: err = %v, want the walk-depth limit", err)
			}
		})
	})
}

// C19: a DAG of Type 3 fonts whose resources name the next font twice cost
// 2^depth in the PDF/X colour scan.
func TestHostileType3FontDAG(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const depth = 40
		objs := []rawObj{
			{dict: "<< /Type /Catalog /Pages 2 0 R >>"},
			{dict: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
			{dict: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Resources << /Font << /F0 4 0 R >> >> >>"},
		}
		for i := 0; i < depth; i++ {
			res := ""
			if i < depth-1 {
				res = fmt.Sprintf("/Resources << /Font << /A %d 0 R /B %d 0 R >> >>", 5+i, 5+i)
			}
			objs = append(objs, rawObj{dict: "<< /Type /Font /Subtype /Type3 /FontBBox [0 0 1 1] /FontMatrix [1 0 0 1 0 0] /CharProcs << >> /Encoding << /Differences [] >> /FirstChar 0 /LastChar 0 /Widths [0] " + res + " >>"})
		}
		doc := readRaw(t, buildRawPDF(objs))
		var msgs []string
		timed(t, "PDF/X-4 over a Type 3 font DAG", 5*time.Second, func() { msgs = findingMessages(ValidatePDFX(doc, pdfx.PDFX4)) })
		noCheckerFindings(t, "PDF/X-4", msgs)
		honoursDeadline(t, "ValidatePDFXContext", func(ctx context.Context) { ValidatePDFXContext(ctx, doc, pdfx.PDFX4) })
	})
}

// cidFontDoc is a one-page document showing text in the bundled composite
// face (a CIDFontType2 with an embedded program), with the descendant font's
// /W replaced by w. extra objects are added under the numbers returned.
func cidFontDoc(t *testing.T, w func(add func(object.Object) object.IndirectRef) object.Array) *Document {
	t.Helper()
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	var b content.Builder
	codes, _ := face.Encode("Hi")
	b.BeginText().SetFont("F1", 12).MoveText(10, 10).ShowText(codes).EndText()
	doc := NewDocument()
	if _, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b, Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back := readRaw(t, buf.Bytes())
	set := 0
	for _, iobj := range back.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		if st, _ := d.Get("Subtype").(object.Name); st == "CIDFontType2" {
			d.Set("W", w(back.Add))
			set++
		}
	}
	if set != 1 {
		t.Fatalf("found %d CIDFontType2 fonts, want 1", set)
	}
	return back
}

// C10: /W entries were expanded into a map. 2,000 ranges of 65,536 CIDs each
// ran out of memory, and a large widths array named by reference from every
// entry was expanded once per reference.
func TestHostileCIDWidths(t *testing.T) {
	t.Run("overlapping ranges", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			doc := cidFontDoc(t, func(func(object.Object) object.IndirectRef) object.Array {
				var w object.Array
				for i := 0; i < 2000; i++ {
					w = append(w, object.Integer(i*65536), object.Integer(i*65536+65535), object.Integer(500))
				}
				return w
			})
			var msgs []string
			timed(t, "PDF/A-2b over 2,000 wide /W ranges", 10*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA2b)) })
			noCheckerFindings(t, "PDF/A-2b", msgs)
			honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA2b) })
		})
	})
	t.Run("shared sub-array", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			doc := cidFontDoc(t, func(add func(object.Object) object.IndirectRef) object.Array {
				big := make(object.Array, 1000000)
				for i := range big {
					big[i] = object.Integer(500)
				}
				ref := add(big)
				var w object.Array
				for i := 0; i < 2000; i++ {
					w = append(w, object.Integer(i*1000000), ref)
				}
				return w
			})
			var msgs []string
			timed(t, "PDF/A-2b over a shared /W sub-array", 10*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA2b)) })
			noCheckerFindings(t, "PDF/A-2b", msgs)
		})
	})
}

// C38: every "trailer" substring was parsed to the end of the file; a comment
// after each made it quadratic.
func TestHostileTrailerSubstrings(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		objs := rawPage("", "<<>>")
		objs = append(objs, rawObj{dict: "<<>>", stream: []byte("/Linearized " + strings.Repeat("trailer%", 160000))})
		raw := buildRawPDF(objs)
		doc := readRaw(t, raw)
		var msgs []string
		timed(t, "PDF/A-1b over 160,000 \"trailer\" substrings", 5*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA1b)) })
		noCheckerFindings(t, "PDF/A-1b", msgs)
		honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA1b) })
	})
}

// C39: one 16 MB appearance stream named by a hundred annotations was
// tokenised a hundred times.
func TestHostileSharedAppearanceStream(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const nAnn = 100
		body := []byte(strings.Repeat("0 0 m ", 16<<20/6))
		objs := rawPage("", "<<>>")
		var annots strings.Builder
		for i := 0; i < nAnn; i++ {
			fmt.Fprintf(&annots, "%d 0 R ", 7+i)
		}
		objs[2].dict = "<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]/Contents 4 0 R/Resources<<>>/Annots[" + annots.String() + "]>>"
		objs = append(objs,
			rawObj{dict: "<</Type/XObject/Subtype/Form/BBox[0 0 1 1]/Filter/FlateDecode>>", stream: zlibBytes(body)},
			rawObj{dict: "<</N 5 0 R>>"},
		)
		for i := 0; i < nAnn; i++ {
			objs = append(objs, rawObj{dict: "<</Type/Annot/Subtype/Square/Rect[0 0 1 1]/F 4/AP 6 0 R>>"})
		}
		raw := buildRawPDF(objs)
		doc := readRaw(t, raw)
		var msgs []string
		timed(t, "PDF/A-2b over a shared appearance stream", 5*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA2b)) })
		noCheckerFindings(t, "PDF/A-2b", msgs)
		honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA2b) })
	})
}

// C40: XMP character data interrupted by comments was accumulated by string
// concatenation.
func TestHostileXMPTextAccumulation(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		xmp := `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"><pdfaid:part>2</pdfaid:part><pdfaid:conformance>B</pdfaid:conformance><dc:format>` + strings.Repeat("a<!---->", 520000) + `</dc:format></rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
		objs := rawPage("", "<<>>")
		objs[0].dict = "<</Type/Catalog/Pages 2 0 R/Metadata 5 0 R>>"
		objs = append(objs, rawObj{dict: "<</Type/Metadata/Subtype/XML>>", stream: []byte(xmp)})
		raw := buildRawPDF(objs)
		doc := readRaw(t, raw)
		var msgs []string
		timed(t, "PDF/A-2b over 520,000 interrupted XMP text runs", 10*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA2b)) })
		noCheckerFindings(t, "PDF/A-2b", msgs)
		honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA2b) })
	})
}

// C37: every referrer of an action chain re-walked it, recursively: N = M =
// 4,000 took sixteen seconds, and a 20,000-long chain overflowed a 16 MB stack.
func TestHostileActionChains(t *testing.T) {
	chain := func(n, m int, last string) []rawObj {
		start := 5
		objs := rawPage("", "<<>>")
		objs[0].dict = fmt.Sprintf("<</Type/Catalog/Pages 2 0 R/OpenAction %d 0 R/Outlines %d 0 R>>", start, start+m+n)
		for i := 0; i < m; i++ {
			switch {
			case i+1 < m:
				objs = append(objs, rawObj{dict: fmt.Sprintf("<</S/GoTo/D[3 0 R/Fit]/Next %d 0 R>>", start+i+1)})
			default:
				objs = append(objs, rawObj{dict: "<</S/" + last + ">>"})
			}
		}
		first := start + m
		for j := 0; j < n; j++ {
			links := fmt.Sprintf("/Parent %d 0 R", start+m+n)
			if j > 0 {
				links += fmt.Sprintf("/Prev %d 0 R", first+j-1)
			}
			if j+1 < n {
				links += fmt.Sprintf("/Next %d 0 R", first+j+1)
			}
			objs = append(objs, rawObj{dict: fmt.Sprintf("<</Title(x)/A %d 0 R%s>>", start, links)})
		}
		objs = append(objs, rawObj{dict: fmt.Sprintf("<</Type/Outlines/First %d 0 R/Last %d 0 R/Count %d>>", first, first+n-1, n)})
		return objs
	}
	t.Run("many referrers", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			const n, m = 4000, 4000
			raw := buildRawPDF(chain(n, m, "Launch"))
			doc := readRaw(t, raw)
			var msgs []string
			timed(t, "PDF/A-2b over 4,000 referrers of a 4,000-long chain", 3*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA2b)) })
			noCheckerFindings(t, "PDF/A-2b", msgs)
			// The /Launch at the end of the chain is judged once per chain it
			// is reached through: for the /OpenAction, and for the first
			// outline item whose /A reaches it (the others share its chain).
			if got := countContaining(msgs, "forbidden action type /Launch"); got != 2 {
				t.Errorf("the /Launch at the end of the chain was reported %d times, want 2 (the /OpenAction and the first holder)", got)
			}
			honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA2b) })
		})
	})
	t.Run("long chain", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: hostileWorkLimits.MaxRSS, Timeout: hostileWorkLimits.Timeout, MaxStack: 16 << 20}, func(t *testing.T) {
			raw := buildRawPDF(chain(1, 20000, "JavaScript"))
			doc := readRaw(t, raw)
			msgs := findingMessages(ValidatePDFA(doc, pdfa.PDFA2b))
			noCheckerFindings(t, "PDF/A-2b", msgs)
			if countContaining(msgs, "forbidden action type /JavaScript") == 0 {
				t.Error("the /JavaScript 20,000 actions down the chain was not reported")
			}
		})
	})
}

// separationImage draws one image, object 5, whose colour space is a
// Separation with the given tint transform, object 6.
func separationImage(w, h int, samples []byte, fnDict string, fnStream []byte, colorspace string) []byte {
	objs := rawPage("q 100 0 0 100 0 0 cm /Im0 Do Q", "<</XObject<</Im0 5 0 R>>>>")
	objs = append(objs,
		rawObj{dict: fmt.Sprintf("<</Type/XObject/Subtype/Image/Width %d/Height %d/BitsPerComponent 8/ColorSpace%s/Filter/FlateDecode>>", w, h, colorspace), stream: zlibBytes(samples)},
		rawObj{dict: fnDict, stream: fnStream},
	)
	return buildRawPDF(objs)
}

// C50: a Type 0 function iterated all 2^m corners of the sample cell: a 40-
// colorant DeviceN image did not finish.
func TestHostileType0Corners(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const n = 40
		raw := separationImage(1, 1, make([]byte, n),
			"<</FunctionType 0/Domain["+strings.Repeat("0 1 ", n)+"]/Range[0 1]/Size["+strings.Repeat("1 ", n)+"]/BitsPerSample 8>>", []byte{1},
			"[/DeviceN["+strings.Repeat("/A ", n)+"]/DeviceGray 6 0 R]")
		doc := readRaw(t, raw)
		var ims []images.ExtractedImage
		timed(t, "extracting a 40-colorant DeviceN image", 5*time.Second, func() { ims = doc.ExtractImages() })
		if len(ims) != 1 || !ims[0].Decoded {
			t.Errorf("images = %+v, want one decoded", ims)
		}
	})
}

// C51: a type-4 tint transform ran once per pixel, each within its own step
// budget: 1,600 pixels of a 590,000-step program took twelve seconds.
func TestHostilePostScriptPerPixel(t *testing.T) {
	prog := func(k int) []byte {
		pk := "{ }"
		for i := 0; i < k; i++ {
			pk = "{ " + pk + " dup true exch if true exch if }"
		}
		return []byte("{ pop " + pk + " true exch if 0.5 }")
	}
	image := func(k, w int) []byte {
		samples := make([]byte, w*w)
		for i := range samples {
			samples[i] = byte(i)
		}
		return separationImage(w, w, samples, "<</FunctionType 4/Domain[0 1]/Range[0 1]>>", prog(k), "[/Separation/Spot/DeviceGray 6 0 R]")
	}
	t.Run("memoised per input", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			doc := readRaw(t, image(16, 64))
			var ims []images.ExtractedImage
			timed(t, "a 64x64 image through a 590,000-step tint transform", 10*time.Second, func() { ims = doc.ExtractImages() })
			if len(ims) != 1 || !ims[0].Decoded {
				t.Errorf("images = %+v, want one decoded", ims)
			}
		})
	})
	t.Run("metered", func(t *testing.T) {
		hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
			// 2^24 steps an input, 256 inputs: past any budget.
			doc := readRaw(t, image(24, 16))
			var ims []images.ExtractedImage
			var err error
			timed(t, "a tint transform of 2^24 steps an input", 60*time.Second, func() {
				all, e := doc.ExtractImagesContext(context.Background())
				ims, err = all, e
			})
			if !errors.Is(err, core.ErrWorkLimit) || len(ims) != 1 || ims[0].Decoded || !strings.Contains(ims[0].Note, "("+core.GuardWork+")") {
				t.Errorf("err = %v, images = %+v; want the image undecoded with a note naming %s, and the error", err, ims, core.GuardWork)
			}
			honoursDeadline(t, "ExtractImagesContext", func(ctx context.Context) { _, _ = doc.ExtractImagesContext(ctx) })
		})
	})
}

// C52: an embedded CMap was searched range by range for every code: 65,000
// ranges and a million codes took eighty seconds.
func TestHostileCMapLookup(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const nr, nc = 65000, 1000000
		var cm strings.Builder
		cm.WriteString("/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n1 begincodespacerange <0000> <FFFF> endcodespacerange\n")
		for i := 0; i < nr; i += 100 {
			fmt.Fprintf(&cm, "%d begincidrange\n", min(100, nr-i))
			for j := i; j < min(i+100, nr); j++ {
				fmt.Fprintf(&cm, "<%04X> <%04X> 1\n", j, j)
			}
			cm.WriteString("endcidrange\n")
		}
		cm.WriteString("endcmap\n")
		objs := rawPage("BT /F1 12 Tf <"+strings.Repeat("FFFF", nc)+"> Tj ET", "<</Font<</F1 5 0 R>>>>")
		objs = append(objs,
			rawObj{dict: "<</Type/Font/Subtype/Type0/BaseFont/X/Encoding 6 0 R/DescendantFonts[7 0 R]>>"},
			rawObj{dict: "<</Type/CMap/CMapName/X/CIDSystemInfo<</Registry(Adobe)/Ordering(Identity)/Supplement 0>>/Filter/FlateDecode>>", stream: zlibBytes([]byte(cm.String()))},
			rawObj{dict: "<</Type/Font/Subtype/CIDFontType2/BaseFont/X/CIDSystemInfo<</Registry(Adobe)/Ordering(Identity)/Supplement 0>>/FontDescriptor 8 0 R>>"},
			rawObj{dict: "<</Type/FontDescriptor/FontName/X/Flags 4/CIDSet 9 0 R>>"},
			rawObj{dict: "<<>>", stream: []byte{0xff}},
		)
		doc := readRaw(t, buildRawPDF(objs))
		var msgs []string
		timed(t, "PDF/A-1b over 65,000 CMap ranges and a million codes", 20*time.Second, func() { msgs = findingMessages(ValidatePDFA(doc, pdfa.PDFA1b)) })
		noCheckerFindings(t, "PDF/A-1b", msgs)
		honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA1b) })
	})
}

// C53: a ToUnicode CMap's bfranges were expanded into a map: 2,000 copies of
// <0000> <FFFF> in 914 bytes were 131 million writes.
func TestHostileToUnicodeRanges(t *testing.T) {
	doc := func(copies int) *Document {
		cm := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n1 begincodespacerange <0000> <FFFF> endcodespacerange\n" +
			fmt.Sprintf("%d beginbfrange\n", copies) + strings.Repeat("<0000><FFFF><0041>\n", copies) + "endbfrange\nendcmap\n"
		objs := rawPage("BT /F1 12 Tf (A) Tj ET", "<</Font<</F1 5 0 R>>>>")
		objs = append(objs,
			rawObj{dict: "<</Type/Font/Subtype/Type1/BaseFont/Helvetica/ToUnicode 6 0 R>>"},
			rawObj{dict: "<</Filter/FlateDecode>>", stream: zlibBytes([]byte(cm))},
		)
		return readRaw(t, buildRawPDF(objs))
	}
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		want, err := doc(1).ExtractText()
		if err != nil {
			t.Fatal(err)
		}
		d := doc(2000)
		var got string
		timed(t, "ExtractText over 2,000 whole-space bfranges", 5*time.Second, func() { got, err = d.ExtractText() })
		if err != nil || got != want {
			t.Errorf("ExtractText = %q, %v; want %q, as one copy of the range gives", got, err, want)
		}
		noCheckerFindings(t, "PDF/UA", findingMessages(ValidatePDFUA(d)))
		honoursDeadline(t, "ExtractTextContext", func(ctx context.Context) { _, _ = d.ExtractTextContext(ctx) })
	})
}

// C79: text extraction had no run, so two hundred pages sharing one 50 MB
// content stream decoded and read it two hundred times.
func TestHostileSharedPageContent(t *testing.T) {
	hostile.Run(t, hostileWorkLimits, func(t *testing.T) {
		const np = 200
		c := []byte("BT /F1 12 Tf (x) Tj ET\n" + strings.Repeat("0 0 1 1 re f\n", 50<<20/13))
		kids := ""
		objs := []rawObj{{dict: "<</Type/Catalog/Pages 2 0 R>>"}, {}, {dict: "<</Filter/FlateDecode>>", stream: zlibBytes(c)}, {dict: "<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>>"}}
		for i := 0; i < np; i++ {
			kids += fmt.Sprintf("%d 0 R ", len(objs)+1)
			objs = append(objs, rawObj{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]/Contents 3 0 R/Resources<</Font<</F1 4 0 R>>>>>>"})
		}
		objs[1] = rawObj{dict: fmt.Sprintf("<</Type/Pages/Kids[%s]/Count %d>>", kids, np)}
		doc := readRaw(t, buildRawPDF(objs))
		var text string
		var err error
		timed(t, "ExtractText over 200 pages sharing a 50 MB stream", 15*time.Second, func() { text, err = doc.ExtractText() })
		if err != nil || strings.Count(text, "x") != np {
			t.Errorf("ExtractText: %d pages' text, err %v; want every page's", strings.Count(text, "x"), err)
		}
		honoursDeadline(t, "ExtractTextContext", func(ctx context.Context) { _, _ = doc.ExtractTextContext(ctx) })
		honoursDeadline(t, "ValidatePDFAContext", func(ctx context.Context) { ValidatePDFAContext(ctx, doc, pdfa.PDFA2b) })
	})
}
