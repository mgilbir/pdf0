package pdf0

import "crypto/rand"

// RepairAction records one fix applied by Repair.
type RepairAction struct {
	Description string
}

// Repair applies a set of safe, well-defined fixes that remove common PDF/A
// conformance failures, and reports what it changed. It mutates the document in
// place; a subsequent Write emits the repaired file. Repair never touches page
// content or fonts — it only removes forbidden document-level constructs — so it
// cannot make a conformant document non-conformant.
//
// It is not a substitute for validation: run ValidatePDFA afterwards to see what
// remains (missing embedded fonts, device colour without an output intent, and
// the like need information Repair does not have).
func (d *Document) Repair(level PDFALevel) []RepairAction {
	var actions []RepairAction
	add := func(desc string) { actions = append(actions, RepairAction{Description: desc}) }

	// Encryption is forbidden in PDF/A. If the document was decrypted on Read,
	// drop it; a still-encrypted document cannot be repaired.
	if d.security != nil {
		d.RemoveEncryption()
		add("removed document encryption (/Encrypt)")
	}

	if cat := d.ResolveDict(d.Trailer.Get("Root")); cat != nil {
		if cat.Get("AA") != nil {
			cat.Delete("AA")
			add("removed catalog additional-actions (/AA)")
		}
		// The document catalog must carry a metadata identifier; ensure /ID.
	}

	// Additional-actions dictionaries are forbidden on pages and annotations too.
	for _, pg := range collectPages(d, d.catalogPages()) {
		if pg.dict.Get("AA") != nil {
			pg.dict.Delete("AA")
			add("removed page additional-actions (/AA)")
		}
	}
	for _, iobj := range d.Objects {
		if a, ok := iobj.Value.(*Dictionary); ok && isAnnotation(a) && a.Get("AA") != nil {
			a.Delete("AA")
			add("removed annotation additional-actions (/AA)")
		}
	}

	// A file identifier is required; synthesize one if missing.
	if d.Trailer.Get("ID") == nil {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err == nil {
			trailer := d.Trailer.Clone()
			trailer.Set("ID", Array{String{Value: id}, String{Value: append([]byte(nil), id...)}})
			d.Trailer = *trailer
			add("added a missing file identifier (/ID)")
		}
	}

	return actions
}

// catalogPages returns the catalog's /Pages reference, or nil.
func (d *Document) catalogPages() Object {
	if cat := d.ResolveDict(d.Trailer.Get("Root")); cat != nil {
		return cat.Get("Pages")
	}
	return nil
}
