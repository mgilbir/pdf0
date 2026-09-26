package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
)

// importFaithful compares what a source page shows with what its extracted copy
// shows. It is the corpus oracle, so it knows the importer's documented
// transformations and nothing else: /StructParent(s) are removed (the structure
// tree is not carried), and a reference into the source's page tree may become
// anything (it is remapped or dropped). Every other key and value must be the
// same, resolved on both sides.
type importFaithful struct {
	src, dst *Document
	same     map[[2]int]bool
}

func (c *importFaithful) isPage(o object.Object) bool {
	d := c.src.ResolveDict(o)
	if d == nil {
		return false
	}
	typ, _ := c.src.Resolve(d.Get("Type")).(object.Name)
	return typ == "Page" || typ == "Pages" || typ == "Catalog"
}

func (c *importFaithful) equal(a, b object.Object, depth int) string {
	if depth > 48 {
		return ""
	}
	ar, aRef := a.(object.IndirectRef)
	br, bRef := b.(object.IndirectRef)
	if aRef && bRef {
		key := [2]int{ar.Number, br.Number}
		if done, ok := c.same[key]; ok && done {
			return ""
		}
		c.same[key] = true // assume equal while comparing: cycles
	}
	a, b = c.src.Resolve(a), c.dst.Resolve(b)
	switch av := a.(type) {
	case *object.Dictionary:
		bv, ok := b.(*object.Dictionary)
		if !ok {
			return fmt.Sprintf("dictionary became %T", b)
		}
		return c.dicts(av, bv, depth)
	case *object.Stream:
		bv, ok := b.(*object.Stream)
		if !ok {
			return fmt.Sprintf("stream became %T", b)
		}
		if !bytes.Equal(av.Data, bv.Data) {
			return "stream data differs"
		}
		return c.dicts(&av.Dict, &bv.Dict, depth)
	case object.Array:
		bv, ok := b.(object.Array)
		if !ok || len(av) != len(bv) {
			return fmt.Sprintf("array of %d became %v", len(av), b)
		}
		for i := range av {
			if c.isPage(av[i]) {
				continue
			}
			if msg := c.equal(av[i], bv[i], depth+1); msg != "" {
				return fmt.Sprintf("[%d]: %s", i, msg)
			}
		}
		return ""
	}
	if !object.Equal(a, b) {
		return fmt.Sprintf("%v became %v", a, b)
	}
	return ""
}

func (c *importFaithful) dicts(a, b *object.Dictionary, depth int) string {
	for k, v := range a.All() {
		if k == "StructParent" || k == "StructParents" || c.isPage(v) {
			continue
		}
		// Annotations and actions are page-bound and follow the destination
		// policy, which the targeted tests pin; the corpus checks what renders.
		if k == "Annots" || k == "A" || k == "AA" || k == "Dest" || k == "Next" {
			continue
		}
		// Write recomputes a stream's /Length from its data, which is compared.
		if k == "Length" {
			continue
		}
		if msg := c.equal(v, b.Get(k), depth+1); msg != "" {
			return k.String() + " " + msg
		}
	}
	return ""
}

// TestCorpusExtractEveryPage runs the importer over every PDF of the veraPDF
// corpus: each page on its own through ExtractPages, Write and Read, and the
// whole document through AppendPages into an empty one. Each extract must be
// one page carrying its own /MediaBox, and everything the source page renders
// from — its content, its resources (every name, resolved), its media, crop
// and rotation — must be what the source page had, attribute inheritance
// included. The source is the oracle.
//
// Every page of every document is checked; the per-page singles are capped for
// documents over 200 pages, for the reason given where it is done.
func TestCorpusExtractEveryPage(t *testing.T) {
	start := time.Now()
	root := testfiles.VeraPDFCorpus.Path(t)
	files := testfiles.VeraPDFCorpus.PDFs(t, "")
	var docs, pages, refused int
	var failures []string
	fail := func(rel, format string, args ...any) {
		failures = append(failures, rel+": "+fmt.Sprintf(format, args...))
	}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue // parse coverage is TestCorpusParsesEntirely's job
		}
		docs++
		n := src.PageCount()
		unsafeSource := src.Locked() || len(src.brokenObjStms) > 0 || len(src.decryptFailures) > 0
		if unsafeSource {
			// The importer must refuse, never copy what it cannot read.
			if _, _, err := src.ExtractPages([]int{0}); err == nil && n > 0 {
				fail(rel, "a Locked or damaged source was extracted without error")
			}
			refused++
			continue
		}
		srcPages := src.PageList()
		// check extracts the given pages in one call and holds every copy to
		// the source page it came from.
		check := func(what string, indices []int) {
			out, _, err := src.ExtractPages(indices)
			if err != nil {
				fail(rel, "%s: %v", what, err)
				return
			}
			var buf bytes.Buffer
			if err := out.Write(&buf); err != nil {
				fail(rel, "%s: write: %v", what, err)
				return
			}
			re, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				fail(rel, "%s: re-read: %v", what, err)
				return
			}
			got := re.PageList()
			if len(got) != len(indices) || rootCount(t, re) != len(indices) {
				fail(rel, "%s: the extract has %d pages, /Count %d; want %d", what, len(got), rootCount(t, re), len(indices))
				return
			}
			cmp := &importFaithful{src: src, dst: re, same: map[[2]int]bool{}}
			for j, i := range indices {
				pages++
				pg, sp := got[j], srcPages[i]
				if _, ok := re.Resolve(pg.Get("MediaBox")).(object.Array); !ok {
					fail(rel, "%s: page %d has no /MediaBox of its own", what, i)
				}
				for _, key := range []object.Name{"Resources", "MediaBox", "CropBox", "Rotate"} {
					want := src.view().InheritedPageAttr(sp, key)
					if want == nil {
						continue
					}
					if msg := cmp.equal(want, pg.Get(key), 0); msg != "" {
						fail(rel, "%s: page %d: %s: %s", what, i, key, msg)
					}
				}
				if msg := cmp.equal(sp.Get("Contents"), pg.Get("Contents"), 0); msg != "" {
					fail(rel, "%s: page %d: /Contents: %s", what, i, msg)
				}
			}
		}
		// Every page on its own. ExtractPages walks the source's page tree once
		// per call, so doing this for every page of a document is quadratic in
		// its page count: the one 10000-page file in the corpus (Isartor
		// 6.1.12 t01, an implementation-limits test) makes the whole test take 89s,
		// against under 4s with the cap below. Above 200 pages,
		// the first and last 100 are extracted singly, and every page is still
		// extracted and checked, by the all-pages call below.
		for i := 0; i < n; i++ {
			if n > 200 && i >= 100 && i < n-100 {
				continue
			}
			check(fmt.Sprintf("page %d alone", i), []int{i})
		}
		if n > 1 {
			all := make([]int, n)
			for i := range all {
				all[i] = i
			}
			check("all pages", all)
		}
		dst := NewDocument()
		if _, err := dst.AppendPages(src); err != nil {
			fail(rel, "AppendPages: %v", err)
			continue
		}
		var buf bytes.Buffer
		if err := dst.Write(&buf); err != nil {
			fail(rel, "AppendPages: write: %v", err)
			continue
		}
		re, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			fail(rel, "AppendPages: re-read: %v", err)
			continue
		}
		if re.PageCount() != n || rootCount(t, re) != n {
			fail(rel, "AppendPages: %d pages, /Count %d; the source has %d", re.PageCount(), rootCount(t, re), n)
		}
	}
	t.Logf("page import over the corpus: %d documents, %d page copies checked, %d sources refused (Locked or damaged), %v",
		docs, pages, refused, time.Since(start).Round(time.Millisecond))
	if docs == 0 || pages == 0 {
		t.Fatal("no corpus page was exercised")
	}
	if len(failures) > 0 {
		if len(failures) > 50 {
			failures = append(failures[:50], fmt.Sprintf("… and %d more", len(failures)-50))
		}
		t.Errorf("%d failure(s):\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
}
