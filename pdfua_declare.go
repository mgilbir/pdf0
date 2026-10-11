package pdf0

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// Declaring a document accessible: the document-level parts of PDF/UA
// (ISO 14289).
//
// A structure tree is most of PDF/UA, and SetStructureTree writes it. The rest
// is said once, for the whole document, and a reader needs all of it: the
// language to read the text in (the catalog /Lang, which SetDocumentInfo writes
// from DocumentInfo.Language), a title to announce in place of the file name
// (dc:title, shown because /ViewerPreferences /DisplayDocTitle says so), and
// the claim itself, the pdfuaid identification in the XMP metadata, which is
// what tells a checker which standard to hold the file to.
//
// The claim is the last of these to make, and DeclarePDFUA refuses to make it
// over a document that cannot carry it: one with no title to display, a
// version the part is not defined for, and for PDF/UA-2 no language. Those are
// the facts the caller supplies; everything the declaration can derive from
// them it writes.

// pdfua2Revision is the pdfuaid:rev PDF/UA-2 declares: the year of the edition
// of ISO 14289-2 the file follows, as pdfaid:rev is for PDF/A-4.
const pdfua2Revision = "2024"

// DeclarePDFUA declares that the document conforms to PDF/UA part 1
// (ISO 14289-1) or part 2 (ISO 14289-2), writing what that declaration
// consists of:
//
//   - pdfuaid:part in the XMP metadata, and for part 2 pdfuaid:rev 2024;
//   - an extension schema describing the pdfuaid namespace, which PDF/A
//     requires of a metadata namespace it does not predefine. It is written
//     whether or not the document claims PDF/A today: the claim can come later
//     (EmbedFacturX makes a document PDF/A-3), and outside PDF/A the
//     description is inert;
//   - /ViewerPreferences /DisplayDocTitle true, so a reader shows the title
//     rather than the file name;
//   - for part 2, the structure tree's top-level Document element placed in
//     the PDF 2.0 structure namespace, as ISO 14289-2 8.2.5.2 requires. A tree
//     set later with SetStructureTree is written that way too, so the tree and
//     the declaration may come in either order.
//
// The document must already say what the declaration cannot invent, and is
// refused otherwise: a title (SetDocumentInfo with DocumentInfo.Title), because
// /DisplayDocTitle with no title to display shows nothing; for part 2 a
// language (DocumentInfo.Language), which ISO 14289-2 8.4.4 requires in the
// catalog; and the PDF version the part is defined for — 1.x for part 1 (set
// Document.Version to "1.7" before adding content; NewDocument starts at 2.0),
// 2.0 for part 2. So describe the document first, then declare it:
//
//	err := doc.SetDocumentInfo(pdf0.DocumentInfo{Title: "Annual report", Language: "en-GB"})
//	if err != nil {
//	    return err
//	}
//	return doc.DeclarePDFUA(1)
//
// Declaring again is harmless, and declaring the other part replaces the first
// declaration. SetDocumentInfo afterwards keeps it, since it edits the
// metadata rather than replacing it.
//
// The declaration is not a check. It writes the claim; ValidatePDFUA and
// ValidatePDFUA2 say whether the document lives up to it — the structure tree,
// the fonts, the alternate text are the caller's — and Save checks only a
// PDF/A claim. A nil document and a Locked one are refused, as for
// SetDocumentInfo, and so is metadata that cannot be edited. A refused
// declaration changes nothing.
func (d *Document) DeclarePDFUA(part int) error {
	if d == nil {
		return errNilDocument
	}
	if d.Locked() {
		return errLockedTarget("declaring PDF/UA")
	}
	if part != 1 && part != 2 {
		return fmt.Errorf("pdf0: there is no PDF/UA part %d; ISO 14289 has parts 1 and 2", part)
	}
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return fmt.Errorf("pdf0: the document has no catalog to declare PDF/UA in")
	}
	declared := d.declaredVersion()
	if major, _, ok := core.ParsePDFVersion(declared); !ok || major != part {
		if part == 1 {
			return fmt.Errorf("pdf0: PDF/UA-1 is defined for PDF 1.x and the document declares PDF %q; "+
				"set Document.Version to \"1.7\" before adding content, or declare part 2", declared)
		}
		return fmt.Errorf("pdf0: PDF/UA-2 is defined for PDF 2.0 and the document declares PDF %q", declared)
	}

	packet, err := d.editableMetadata(catalog)
	if err != nil {
		return err
	}
	if !hasDocumentTitle(packet) {
		return fmt.Errorf("pdf0: PDF/UA requires a title for /DisplayDocTitle to display, and the document's " +
			"metadata has none (dc:title); describe it with SetDocumentInfo, giving a Title, before declaring it")
	}
	if part == 2 {
		if s, _ := d.Resolve(catalog.Get("Lang")).(object.String); len(s.Value) == 0 {
			return fmt.Errorf("pdf0: PDF/UA-2 requires the document's language in the catalog /Lang " +
				"(ISO 14289-2 8.4.4); give SetDocumentInfo a Language before declaring it")
		}
	}
	if err := writePDFUAIdentification(packet, part); err != nil {
		return fmt.Errorf("pdf0: document metadata: %w", err)
	}
	data, err := packet.Bytes()
	if err != nil {
		return err
	}

	// Everything that can fail has; from here the document changes.
	d.setMetadataStream(catalog, data)
	prefs := d.ResolveDict(catalog.Get("ViewerPreferences"))
	if prefs == nil {
		prefs = &object.Dictionary{}
		catalog.Set("ViewerPreferences", prefs)
	}
	prefs.Set("DisplayDocTitle", object.Boolean(true))
	if part == 2 {
		d.placeDocumentInPDF20Namespace(catalog)
	}
	return nil
}

// hasDocumentTitle reports whether the packet carries a non-empty dc:title.
func hasDocumentTitle(p *xmp.Packet) bool {
	title, ok := p.Get(xmp.NSDC, "title")
	if !ok {
		return false
	}
	text, _ := title.Value.AltText("x-default")
	return strings.TrimSpace(text) != ""
}

// writePDFUAIdentification sets the pdfuaid properties for part, removing the
// revision when the part has none, and describes the namespace for PDF/A.
func writePDFUAIdentification(p *xmp.Packet, part int) error {
	// The prefix must be pdfuaid (ISO 14289-1 clause 5), which SetText uses
	// unless the packet already binds it to another namespace — and then the
	// validator reports it, as it should.
	if err := p.SetText(xmp.NSPDFUAID, "pdfuaid", "part", fmt.Sprint(part)); err != nil {
		return err
	}
	if part == 2 {
		if err := p.SetText(xmp.NSPDFUAID, "pdfuaid", "rev", pdfua2Revision); err != nil {
			return err
		}
	} else {
		p.Remove(xmp.NSPDFUAID, "rev")
	}
	return p.SetExtensionSchema(pdfuaExtensionSchema)
}

// pdfuaExtensionSchema describes the PDF/UA identification schema as PDF/A
// requires (ISO 19005-2 6.6.2.3.1). The wording of the first three entries is
// ISO 14289-1's own example; rev is ISO 14289-2's addition. All four are
// declared whichever part is claimed, so the description does not change when
// the declaration does.
var pdfuaExtensionSchema = xmp.ExtensionSchema{
	Schema:       "PDF/UA Universal Accessibility Schema",
	NamespaceURI: xmp.NSPDFUAID,
	Prefix:       "pdfuaid",
	Properties: []xmp.ExtensionProperty{
		{Name: "part", ValueType: "Integer", Category: "internal", Description: "Indicates, which part of ISO 14289 standard is followed"},
		{Name: "amd", ValueType: "Text", Category: "internal", Description: "Optional PDF/UA amendment identifier"},
		{Name: "corr", ValueType: "Text", Category: "internal", Description: "Optional PDF/UA corrigenda identifier"},
		{Name: "rev", ValueType: "Integer", Category: "internal", Description: "The year of the edition of the part of ISO 14289 followed"},
	},
}

// declaresPDFUA2 reports whether the document's metadata claims PDF/UA-2.
func (d *Document) declaresPDFUA2() bool {
	packet, status := d.view().DocumentXMPPacket()
	if status != core.XMPParsed {
		return false
	}
	part, _ := packet.Text(xmp.NSPDFUAID, "part")
	return part == "2"
}

// placeDocumentInPDF20Namespace puts each top-level Document element of the
// structure tree that names no namespace into the PDF 2.0 standard structure
// namespace, declaring that namespace on the tree root when it is not there.
//
// ISO 14289-2 8.2.5.2 requires the tree root's one child to be a Document in
// the PDF 2.0 namespace. Document means the same in both standard
// namespaces, so this changes what a PDF/UA-2 checker reads and nothing a
// reader presents; an element already in some namespace is left there. Below
// the top level nothing is moved: no rule asks it, and every other element is
// in the namespace SetStructureTree chose for its type.
func (d *Document) placeDocumentInPDF20Namespace(catalog *object.Dictionary) {
	root := d.ResolveDict(catalog.Get("StructTreeRoot"))
	if root == nil {
		return
	}
	var top []*object.Dictionary
	kids := d.Resolve(root.Get("K"))
	if arr, ok := kids.(object.Array); ok {
		for _, k := range arr {
			top = append(top, d.ResolveDict(k))
		}
	} else {
		top = append(top, d.ResolveDict(kids))
	}
	var ns object.Object
	for _, e := range top {
		if e == nil || e.Get("NS") != nil {
			continue
		}
		if s, _ := d.Resolve(e.Get("S")).(object.Name); s != "Document" {
			continue
		}
		if ns == nil {
			ns = d.pdf20Namespace(root)
		}
		e.Set("NS", ns)
	}
}

// pdf20Namespace returns the reference to the tree root's PDF 2.0 namespace
// dictionary, adding one to its /Namespaces when there is none.
func (d *Document) pdf20Namespace(root *object.Dictionary) object.Object {
	list := root.Get("Namespaces")
	spaces, _ := d.Resolve(list).(object.Array)
	for _, entry := range spaces {
		ref, isRef := entry.(object.IndirectRef)
		if !isRef {
			continue // /NS must be an indirect reference (ISO 32000-2 Table 355)
		}
		if nsDict := d.ResolveDict(ref); nsDict != nil {
			if s, _ := d.Resolve(nsDict.Get("NS")).(object.String); string(s.Value) == core.NSPDF20 {
				return ref
			}
		}
	}
	ref := d.Add(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Namespace")},
		object.Entry{Key: "NS", Value: object.String{Value: []byte(core.NSPDF20)}},
	))
	grown := append(slices.Clone(spaces), ref)
	// The array is replaced where it lives: in its own object when /Namespaces
	// is a reference to one, so nothing that shares it sees a stale copy.
	if n := object.RefNum(list); n != 0 && d.Objects[n] != nil {
		if _, ok := d.Objects[n].Value.(object.Array); ok {
			d.Objects[n].Value = grown
			return ref
		}
	}
	root.Set("Namespaces", grown)
	return ref
}
