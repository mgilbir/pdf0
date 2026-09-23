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
	// ref is the font dictionary every page naming the face refers to. It
	// never changes once written.
	ref object.IndirectRef
	// nums are the object numbers the embedding wrote, in the order it wrote
	// them; a rewrite reuses them in the same order.
	nums []int
	// revision is the face's EmbedRevision when the embedding was written.
	revision int
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
	refs := make(map[object.Name]object.IndirectRef, len(faces))
	var rewrites []rewrite
	pending := map[*fonts.Face]object.IndirectRef{} // faces named twice in one call
	for _, name := range sortedNames(faces) {
		face := faces[name]
		if ref, ok := pending[face]; ok {
			refs[name] = ref
			continue
		}
		prev, have := d.faces[face]
		if have {
			// An embedding whose font dictionary is no longer in the document
			// — a caller edited Objects directly — is written afresh.
			if _, ok := d.Objects[prev.ref.Number]; !ok {
				have = false
			}
		}
		rev := face.EmbedRevision()
		if have && prev.revision == rev {
			refs[name] = prev.ref
			pending[face] = prev.ref
			continue
		}
		first := len(stage.objs)
		stage.reuse = nil
		if have {
			stage.reuse = append([]int(nil), prev.nums...)
		}
		top, err := embedFace(face, stage)
		stage.reuse = nil
		if err != nil {
			stage.abort()
			return nil, fmt.Errorf("embedding the face named %s: %w", name, err)
		}
		written := stage.objs[first:]
		if have && top != prev.ref {
			// The rewrite wrote a different number of objects than the first
			// embedding, so the font dictionary did not land on the number the
			// pages name. Swap the two numbers throughout what was written.
			swapRefs(written, top.Number, prev.ref.Number)
			top = prev.ref
		}
		nums := make([]int, len(written))
		for i, o := range written {
			nums[i] = o.Number
		}
		entry := faceEmbedding{ref: top, nums: nums, revision: rev}
		var old []int
		if have {
			old = prev.nums
		}
		rewrites = append(rewrites, rewrite{face: face, entry: entry, old: old})
		refs[name] = top
		pending[face] = top
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

// embedFace is Face.Embed, as embedFaces calls it. It is a variable only so a
// test can make a rewrite write a different number of objects than the
// embedding it replaces, which no face does today and embedFaces handles.
var embedFace = func(f *fonts.Face, a fonts.Allocator) (object.IndirectRef, error) { return f.Embed(a) }

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
