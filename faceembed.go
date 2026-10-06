package pdf0

import (
	"fmt"
	"slices"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
)

// One embedded font per face per document.
//
// Page.Faces embeds a face once the page's drawing is final, subsetted to the
// glyphs the face has set. Doing that afresh on every AddPage wrote a new font
// per page, each a subset of every glyph set so far: a hundred pages of one
// face were a hundred font programs, page N's holding the glyphs of pages 1..N
// — quadratic, and 134 MB for a hundred pages of Japanese text (audit
// 2026-09-22 C94).
//
// Instead the document remembers each face it has embedded: the font
// dictionary pages name, every object the embedding wrote, and the face's
// EmbedRevision at the time. A later page that names the face gets the same
// font dictionary. When the face has set glyphs since — which is what
// EmbedRevision says — the embedding is rewritten in place: the new subset,
// covering every use so far, is written under the old object numbers, so every
// page that named the font names the larger one. The document is complete
// after every call, which is why this is done at AddPage and not deferred to
// Write: a validator, ExtractText or a caller walking Objects between calls
// sees a font that covers every page it is named on.

// faceEmbedding is one face as this document has embedded it.
type faceEmbedding struct {
	// ref is the font dictionary every page naming the face refers to, and
	// vref the vertical font (fonts.Face.Vertical) every page naming its
	// vertical form refers to. Each is the zero reference until a page names
	// that form, and never changes once written.
	ref, vref object.IndirectRef
	// subs are a bitmap face's other Type 3 fonts, by fonts.SubfontName
	// number less one. A page names each one its drawing selected, and each
	// keeps its number once written, as ref does.
	subs []object.IndirectRef
	// nums are the object numbers the embedding wrote, in the order it wrote
	// them; a rewrite reuses them in the same order.
	nums []int
	// revision is the face's EmbedRevision when the embedding was written.
	revision int
	// forms are the fonts the embedding wrote: every form of the face a page
	// has named.
	forms fonts.Forms
}

// embedFaces returns, for each named face, the font dictionary the document
// has for it, embedding the face or rewriting its embedding first when the
// face has set glyphs the embedded subset lacks.
//
// It is called once the content stream is final: a face is subsetted to the
// glyphs it was asked to set, so embedding it any earlier produces a font that
// contains nothing the page uses. All the faces are staged and committed
// together, so a face that cannot be embedded changes nothing — not the other
// faces, and not a face's earlier embedding.
func (d *Document) embedFaces(faces map[object.Name]*fonts.Face) (map[object.Name]object.IndirectRef, error) {
	if len(faces) == 0 {
		return nil, nil
	}
	type rewrite struct {
		face  *fonts.Face
		entry faceEmbedding
		old   []int // the numbers the previous embedding held
	}
	stage := d.stageAdds()
	// A document that claims PDF/A-1 may not use transparency (ISO 19005-1
	// 6.4), and a bitmap face's anti-aliased and colour glyphs are soft
	// masks unless they are written without (fonts.Forms.Opaque). The claim
	// is read as each page is added, so a font embedded before it was made
	// is rewritten with the next page that names it.
	opaque := d.existingPDFAIdentification().part == "1"
	refs := make(map[object.Name]object.IndirectRef, len(faces))
	var rewrites []rewrite
	// The two forms of one face are one embedding, keyed by the face they
	// belong to, which writes every form any page has named: the forms named
	// before, and the ones this page names.
	named := map[*fonts.Face]fonts.Forms{}
	for _, f := range faces {
		forms := named[f.Horizontal()]
		if f.IsVertical() {
			forms.Vertical = true
		} else {
			forms.Horizontal = true
		}
		named[f.Horizontal()] = forms
	}
	// formOf is the font dictionary a name is written with: the vertical one
	// for a vertical form.
	formOf := func(face *fonts.Face, e fonts.Embedded) object.IndirectRef {
		if face.IsVertical() {
			return e.Vertical
		}
		return e.Horizontal
	}
	pending := map[*fonts.Face]fonts.Embedded{} // faces already settled in this call
	for _, name := range sortedNames(faces) {
		face := faces[name].Horizontal()
		if e, ok := pending[face]; ok {
			refs[name] = formOf(faces[name], e)
			subfontRefs(refs, name, e)
			continue
		}
		prev, have := d.faces[face]
		if have {
			// An embedding whose font dictionaries are no longer in the
			// document — a caller edited Objects directly — is written afresh.
			for _, ref := range append([]object.IndirectRef{prev.ref, prev.vref}, prev.subs...) {
				if _, ok := d.Objects[ref.Number]; ref.Number != 0 && !ok {
					have = false
				}
			}
		}
		want := named[face]
		want.Opaque = opaque
		if have {
			want.Horizontal = want.Horizontal || prev.forms.Horizontal
			want.Vertical = want.Vertical || prev.forms.Vertical
		}
		rev := face.EmbedRevision()
		if have && prev.revision == rev && prev.forms == want {
			e := fonts.Embedded{Horizontal: prev.ref, Vertical: prev.vref, Subfonts: prev.subs}
			refs[name] = formOf(faces[name], e)
			subfontRefs(refs, name, e)
			pending[face] = e
			continue
		}
		first := len(stage.objs)
		stage.reuse = nil
		if have {
			stage.reuse = append([]int(nil), prev.nums...)
		}
		e, err := embedFace(face, stage, want)
		stage.reuse = nil
		if err != nil {
			stage.abort()
			return nil, fmt.Errorf("embedding the face named %s: %w", name, err)
		}
		written := stage.objs[first:]
		// A rewrite that wrote a different number of objects than the
		// embedding before it — a form added, which is written in its place
		// among the others — does not land each font dictionary on the number
		// the pages already name. Each such number is swapped back
		// throughout what was written.
		keep := func(got *object.IndirectRef, was object.IndirectRef) {
			if was.Number == 0 || *got == was {
				return
			}
			a, b := got.Number, was.Number
			swapRefs(written, a, b)
			moved := []*object.IndirectRef{&e.Horizontal, &e.Vertical}
			for i := range e.Subfonts {
				moved = append(moved, &e.Subfonts[i])
			}
			for _, r := range moved {
				switch r.Number {
				case a:
					r.Number = b
				case b:
					r.Number = a
				}
			}
		}
		if have {
			keep(&e.Horizontal, prev.ref)
			keep(&e.Vertical, prev.vref)
			// A bitmap face's sub-fonts only ever grow in number, and the
			// ones a page already names keep their numbers.
			for i := range min(len(prev.subs), len(e.Subfonts)) {
				keep(&e.Subfonts[i], prev.subs[i])
			}
		}
		nums := make([]int, len(written))
		for i, o := range written {
			nums[i] = o.Number
		}
		entry := faceEmbedding{ref: e.Horizontal, vref: e.Vertical, subs: e.Subfonts, nums: nums, revision: rev, forms: want}
		var old []int
		if have {
			old = prev.nums
		}
		rewrites = append(rewrites, rewrite{face: face, entry: entry, old: old})
		refs[name] = formOf(faces[name], e)
		subfontRefs(refs, name, e)
		pending[face] = e
	}
	stage.commit()
	if d.faces == nil {
		d.faces = map[*fonts.Face]*faceEmbedding{}
	}
	for _, r := range rewrites {
		kept := map[int]bool{}
		for _, n := range r.entry.nums {
			kept[n] = true
		}
		for _, n := range r.old {
			if !kept[n] {
				delete(d.Objects, n) // held by the previous subset only
			}
		}
		entry := r.entry
		d.faces[r.face] = &entry
	}
	return refs, nil
}

// subfontRefs names a bitmap face's other Type 3 fonts after the name the face
// has, as its drawing selected them (fonts.SubfontName).
func subfontRefs(refs map[object.Name]object.IndirectRef, name object.Name, e fonts.Embedded) {
	for k, ref := range e.Subfonts {
		refs[fonts.SubfontName(name, k+1)] = ref
	}
}

// embedFace is Face.EmbedForms, as embedFaces calls it. It is a variable only
// so a test can make a rewrite write a different number of objects than the
// embedding it replaces anywhere among them, which a face does only by gaining
// a form and embedFaces handles in general.
var embedFace = func(f *fonts.Face, a fonts.Allocator, forms fonts.Forms) (fonts.Embedded, error) {
	return f.EmbedForms(a, forms)
}

// swapRefs exchanges object numbers a and b in every reference the objects
// hold, and in the objects' own numbers. The objects are freshly written by
// one embedding, so nothing outside them refers to either number yet except
// the pages that name the font dictionary — which is the number being kept.
func swapRefs(objs []*object.IndirectObject, a, b int) {
	swap := func(n int) int {
		switch n {
		case a:
			return b
		case b:
			return a
		}
		return n
	}
	var walk func(o object.Object) object.Object
	walk = func(o object.Object) object.Object {
		switch v := o.(type) {
		case object.IndirectRef:
			v.Number = swap(v.Number)
			return v
		case *object.Dictionary:
			for _, k := range slices.Collect(v.Keys()) {
				v.Set(k, walk(v.Get(k)))
			}
		case *object.Stream:
			walk(&v.Dict)
		case object.Array:
			for i := range v {
				v[i] = walk(v[i])
			}
		}
		return o
	}
	for _, obj := range objs {
		obj.Number = swap(obj.Number)
		obj.Value = walk(obj.Value)
	}
}
