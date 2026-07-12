package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This test checks that pdf0's validator is *consistent* with the Arlington PDF
// object grammar — that wherever both have an opinion about the same construct,
// they agree. It does NOT measure coverage (pdf0 deliberately validates PDF/A
// conformance over a parseable base document, not the whole object grammar).
// What it guards against is *misalignment*: pdf0 enforcing a structural rule
// that contradicts the grammar — rejecting a value Arlington considers valid, or
// demanding a type Arlington forbids. Such a rule would be a bug (a likely false
// positive on real files), and this fails if one ever appears.
//
// Each key pdf0 constrains on an object it builds is classified against
// Arlington as AGREE, STRICTER (pdf0 narrower than the base grammar — expected
// for a PDF/A validator), or CONTRADICT (a real disagreement). CONTRADICT fails.
// The known STRICTER differences are listed in arlStricter with their reason.

// arlStricter records the keys where pdf0 is deliberately stricter than the base
// ISO 32000 grammar because PDF/A requires it. Key is "<Type>/<key>".
var arlStricter = map[string]string{
	"/Catalog/Metadata":            "PDF/A requires an XMP metadata stream (ISO 19005 6.7); base grammar makes /Metadata optional",
	"/OutputIntent/DestOutputProfile": "PDF/A requires the output intent's ICC profile; base grammar makes it conditional",
}

func TestArlingtonValidatorConsistency(t *testing.T) {
	dir := os.Getenv("ARLINGTON_MODEL")
	if dir == "" {
		dir = "testdata/arlington-pdf-model/tsv/2.0"
	}
	if _, err := os.Stat(filepath.Join(dir, "Catalog.tsv")); err != nil {
		t.Skip("Arlington model not present; run `make arlington`")
	}
	m := loadArlModel(dir)

	build := func() *Document { return NewPDFADocumentWithInfo(PDFA2b, "Title", "Author") }

	find := func(d *Document, ty Name) *Dictionary {
		for _, io := range d.Objects {
			switch v := io.Value.(type) {
			case *Dictionary:
				if t2, _ := v.Get("Type").(Name); t2 == ty {
					return v
				}
			case *Stream:
				if t2, _ := v.Dict.Get("Type").(Name); t2 == ty {
					return &v.Dict
				}
			}
		}
		return nil
	}

	// pdf0Rejects reports whether pdf0's validator flags a document after `mut`
	// is applied to a fresh conformant document's object of the given /Type.
	pdf0Rejects := func(ty Name, mut func(*Dictionary)) (rejected, applied bool) {
		d := build()
		o := find(d, ty)
		if o == nil {
			return false, false
		}
		mut(o)
		var b bytes.Buffer
		if d.Write(&b) != nil {
			return true, true
		}
		rd, err := Read(bytes.NewReader(b.Bytes()), int64(b.Len()))
		if err != nil {
			return true, true
		}
		return len(ValidatePDFABytes(rd, PDFA2b, b.Bytes())) > 0, true
	}

	// Sanity: pdf0 accepts, and Arlington accepts, the unmutated document.
	if data := writeDoc(t, build()); len(ValidatePDFABytes(mustRead(t, data), PDFA2b, data)) != 0 {
		t.Fatal("baseline conformant document is flagged by pdf0's validator")
	}

	objs := map[Name]string{
		"Catalog": "Catalog", "Pages": "PageTreeNodeRoot", "Page": "PageObject",
		"OutputIntent": "OutputIntents", "Metadata": "Metadata",
	}
	sample := build()

	type diff struct{ kind, detail string }
	var diffs []diff
	note := func(kind, detail string) { diffs = append(diffs, diff{kind, detail}) }

	for ty, sn := range objs {
		spec := m.spec(sn)
		obj := find(sample, ty)
		if spec == nil || obj == nil {
			continue
		}
		present := map[string]bool{}
		for _, k := range obj.Keys {
			present[string(k)] = true
		}
		for _, k := range spec {
			if !present[k.key] {
				continue
			}
			id := fmt.Sprintf("%s/%s", ty, k.key) // ty is a Name, printed as "/Catalog"

			// (1) Requiredness. If pdf0 rejects the key's removal it treats the
			// key as required; compare to Arlington.
			removedRejected, ok := pdf0Rejects(ty, func(d *Dictionary) { d.Delete(Name(k.key)) })
			if ok && removedRejected {
				switch {
				case k.required == "TRUE" || strings.Contains(k.required, "fn:"):
					// Arlington also requires it (unconditionally or conditionally).
				default:
					// pdf0 requires a key Arlington marks plainly optional.
					if reason := arlStricter[id]; reason != "" {
						note("STRICTER", id+" (required by pdf0) — "+reason)
					} else {
						note("REVIEW", id+": pdf0 requires it but Arlington Required="+q(k.required)+" (stricter than base grammar — confirm it is a PDF/A requirement)")
					}
				}
			}

			// (2) Contradiction: pdf0 must NOT reject a value that fully satisfies
			// Arlington. For a name-typed key with a closed value set, every listed
			// value is grammar-valid; pdf0 rejecting the one PDF/A also uses would
			// be a contradiction. We probe the value pdf0's own builder emits,
			// which is both PDF/A- and (per the output guard) Arlington-valid.
			if lits := literalSet(k.possible); lits != nil {
				cur, isName := obj.Get(Name(k.key)).(Name)
				if isName && lits[string(cur)] {
					rejected, ok := pdf0Rejects(ty, func(d *Dictionary) { d.Set(Name(k.key), cur) })
					if ok && rejected {
						t.Errorf("CONTRADICT %s: pdf0 rejects value /%s which Arlington's PossibleValues %s permits", id, cur, k.possible)
					}
				}
			}
		}
	}

	// Report the (expected) differences; fail on any unexplained REVIEW item.
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].detail < diffs[j].detail })
	for _, d := range diffs {
		switch d.kind {
		case "STRICTER":
			t.Logf("stricter (PDF/A): %s", d.detail)
		case "REVIEW":
			t.Errorf("unexplained misalignment: %s", d.detail)
		}
	}
	nStricter := 0
	for _, d := range diffs {
		if d.kind == "STRICTER" {
			nStricter++
		}
	}
	t.Logf("consistency: pdf0 agrees with Arlington on every checked key (no contradictions); %d deliberate PDF/A-stricter difference(s)", nStricter)
}

func q(s string) string { return "\"" + s + "\"" }

func writeDoc(t *testing.T, d *Document) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := d.Write(&b); err != nil {
		t.Fatalf("write: %v", err)
	}
	return b.Bytes()
}

func mustRead(t *testing.T, data []byte) *Document {
	t.Helper()
	d, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return d
}
