package pdf0

import (
	"crypto/rand"
	"fmt"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// This file implements preflight repair: the write side of PDF/A conformance,
// as opposed to the validators that only report. It removes the document-level
// constructs ISO 19005 forbids outright — encryption (6.1.3), and the
// additional-action trigger events the target level forbids on the catalog,
// pages, annotations and form fields (6.6) — and synthesizes the required file
// identifier (6.1.3, ISO 32000-1 14.4).
//
// The scope is deliberately narrow. Every fix here is information-free: it
// deletes something forbidden or supplies a value the standard lets the
// producer choose, so it can never turn a conforming document into a
// non-conforming one. Failures that need information the file does not carry —
// a missing font program, device colour with no output intent — are left to
// the caller and to ValidatePDFA. Do not add a fix that has to guess.

// RepairAction records one fix applied by Repair.
type RepairAction struct {
	Description string
}

// maxFieldDepth bounds the walk of the AcroForm field tree. A form nested this
// deeply is a mistake or an attack; the walk also visits each field once, so a
// field tree with a cycle ends.
const maxFieldDepth = 64

// Repair applies a set of safe, well-defined fixes that remove common PDF/A
// conformance failures at level, and reports what it changed. It mutates the
// document in place; a subsequent Write emits the repaired file. Repair never
// touches page content or fonts — it only removes forbidden document-level
// constructs — so it cannot make a conformant document non-conformant.
//
// What it removes depends on level, and is what the validator reports at that
// level (pdfa.TriggerEventForbidden): before PDF/A-4 every additional-actions
// (/AA) dictionary on the catalog, a page, an annotation or a form field; at
// PDF/A-4 only the trigger events 6.6.3 forbids, keeping the interaction events
// it permits. Annotations are found through each page's /Annots, direct or
// indirect, and fields through the AcroForm field tree.
//
// It is not a substitute for validation: run ValidatePDFA afterwards to see what
// remains (missing embedded fonts, device colour without an output intent, and
// the like need information Repair does not have).
//
// It returns an error, and changes nothing, for a nil or Locked document — its
// content is still ciphertext, and a file identifier added to it would change
// the key that content is encrypted under — and for a level pdfa does not
// define.
func (d *Document) Repair(level pdfa.Level) ([]RepairAction, error) {
	if d == nil {
		return nil, errNilDocument
	}
	if _, ok := pdfa.TriggerEventForbidden(level, ""); !ok {
		return nil, fmt.Errorf("pdf0: %v is not a PDF/A level Repair knows", level)
	}
	if d.Locked() {
		return nil, fmt.Errorf("pdf0: cannot repair a document whose content is still encrypted: %w", d.LockReason())
	}
	var actions []RepairAction
	add := func(desc string) { actions = append(actions, RepairAction{Description: desc}) }

	// Encryption is forbidden in PDF/A. The document was decrypted on Read
	// (it is not Locked), so the encryption can be dropped.
	if d.security != nil {
		d.RemoveEncryption()
		add("removed document encryption (/Encrypt)")
	}

	pruneAA := func(owner *object.Dictionary, what string) {
		if owner == nil {
			return
		}
		aa := d.ResolveDict(owner.Get("AA"))
		if aa == nil {
			if owner.Has("AA") {
				owner.Delete("AA") // not a dictionary: nothing a reader could act on
				add("removed " + what + " additional-actions (/AA)")
			}
			return
		}
		var cut []object.Name
		for event := range aa.Keys() {
			if forbidden, _ := pdfa.TriggerEventForbidden(level, event); forbidden {
				cut = append(cut, event)
			}
		}
		// Every event forbidden, or none there at all: the /AA goes. An empty
		// one says nothing, and before PDF/A-4 its presence alone is what is
		// forbidden.
		if len(cut) == aa.Len() {
			owner.Delete("AA")
			add("removed " + what + " additional-actions (/AA)")
			return
		}
		// Edited in place: an /AA shared by several owners is pruned once for
		// all of them, and each is reported.
		for _, event := range cut {
			aa.Delete(event)
			add(fmt.Sprintf("removed the %v trigger event from %s additional-actions (/AA)", event, what))
		}
	}

	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	pruneAA(catalog, "catalog")

	seen := map[*object.Dictionary]bool{}
	for _, page := range d.PageList() {
		pruneAA(page, "page")
		annots, _ := d.Resolve(page.Get("Annots")).(object.Array)
		for _, a := range annots {
			if ad := d.ResolveDict(a); ad != nil && !seen[ad] {
				seen[ad] = true
				pruneAA(ad, "annotation")
			}
		}
	}
	if catalog != nil {
		if form := d.ResolveDict(catalog.Get("AcroForm")); form != nil {
			fields, _ := d.Resolve(form.Get("Fields")).(object.Array)
			d.walkFields(fields, 0, seen, func(f *object.Dictionary) { pruneAA(f, "form field") })
		}
	}

	// A file identifier is required; synthesize one if missing.
	if d.Trailer.Get("ID") == nil {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return actions, fmt.Errorf("pdf0: generating a file identifier: %w", err)
		}
		trailer := d.Trailer.Clone()
		trailer.Set("ID", object.Array{object.String{Value: id}, object.String{Value: append([]byte(nil), id...)}})
		d.Trailer = *trailer // dictcopy: installs the edited clone; nothing else holds it
		add("added a missing file identifier (/ID)")
	}

	return actions, nil
}

// walkFields calls visit on every field dictionary of an AcroForm field tree,
// through /Kids, whether each is direct or indirect. Each field is walked once,
// so a tree that shares or cycles back to a node ends; one already visited as
// an annotation (a widget merged with its field) is not visited again.
func (d *Document) walkFields(kids object.Array, depth int, seen map[*object.Dictionary]bool, visit func(*object.Dictionary)) {
	walked := map[*object.Dictionary]bool{}
	var walk func(kids object.Array, depth int)
	walk = func(kids object.Array, depth int) {
		if depth > maxFieldDepth {
			return
		}
		for _, k := range kids {
			f := d.ResolveDict(k)
			if f == nil || walked[f] {
				continue
			}
			walked[f] = true
			if !seen[f] {
				seen[f] = true
				visit(f)
			}
			if sub, ok := d.Resolve(f.Get("Kids")).(object.Array); ok {
				walk(sub, depth+1)
			}
		}
	}
	walk(kids, depth)
}
