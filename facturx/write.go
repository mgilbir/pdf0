package facturx

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// This file produces Factur-X invoices and Order-X orders: it embeds the Cross
// Industry XML as an associated file and writes the XMP metadata that
// identifies it, including the PDF/A extension schema that declares the fx:
// namespace, as PDF/A requires for a custom metadata schema.
//
// It edits the document; it does not rebuild any of it. The attachment is
// inserted into the EmbeddedFiles name tree beside whatever is already there,
// the /AF array gains one entry, and the metadata packet is edited through the
// XMP model, so every other property it holds — the PDF/A conformance letter
// among them — survives (audit C44). This used to set a fresh name tree
// holding only the invoice and a fresh metadata packet with conformance B.

const facturxFileName = "factur-x.xml"
const orderXFileName = "order-x.xml"

// ErrNotXML is returned when the document to embed is empty or is not
// well-formed XML. A container is validated by its embedded XML; embedding
// something that cannot be read as XML makes a file that says it is an invoice
// and cannot be checked as one.
var ErrNotXML = errors.New("facturx: the document to embed is empty or not well-formed XML")

// Embed embeds the CII invoice XML into doc as the associated file
// factur-x.xml and writes the Factur-X metadata for the given profile. doc must
// be a PDF/A-3 document (for example from NewPDFADocument(PDFA3b)), or one that
// declares no PDF/A part at all, which then declares 3b; the result is a
// Factur-X container that ValidateFacturX accepts after a round trip. title,
// when non-empty, is recorded as the document title in the XMP.
//
// An invoice the document already carries — an attachment named like one
// (factur-x.xml, zugferd-invoice.xml, xrechnung.xml, in any case), in /AF or
// the EmbeddedFiles name tree — is replaced, not added to: a container carries
// exactly one invoice, and embedding again is how an invoice is updated. That
// makes Embed idempotent: embedding the same invoice twice leaves the document
// as embedding it once did. Every other attachment is kept.
//
// It returns an error, and leaves doc unchanged, when invoiceXML is empty or
// not well-formed XML (ErrNotXML), the profile is unknown, the title cannot be
// written as XMP text, the document declares a PDF/A part other than 3, or its
// existing metadata or name tree cannot be edited without guessing.
func Embed(doc core.View, invoiceXML []byte, profile formalis.Profile, title string) error {
	if _, ok := formalis.ProfileFor(string(profile)); !ok {
		return fmt.Errorf("unknown Factur-X profile %q", profile)
	}
	return embed(doc, invoiceFamily, invoiceXML, facturxFileName, "INVOICE", string(profile), "Factur-X XML invoice", title)
}

// EmbedOrder is Embed for Order-X: it embeds the Cross Industry Order XML as
// order-x.xml and writes the Order-X metadata, in the Order-X namespace.
// docType is ORDER, ORDER_CHANGE or ORDER_RESPONSE.
func EmbedOrder(doc core.View, orderXML []byte, profile OrderXProfile, docType, title string) error {
	if _, ok := orderXProfileFor(string(profile)); !ok {
		return fmt.Errorf("unknown Order-X profile %q", profile)
	}
	if !orderXDocumentTypes[docType] {
		return fmt.Errorf("unknown Order-X document type %q (ORDER, ORDER_CHANGE or ORDER_RESPONSE)", docType)
	}
	return embed(doc, orderFamily, orderXML, orderXFileName, docType, string(profile), "Order-X XML order", title)
}

func embed(doc core.View, f family, data []byte, fileName, docType, level, desc, title string) error {
	if err := checkXML(data); err != nil {
		return err
	}
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	if cat == nil {
		return fmt.Errorf("document has no catalog")
	}

	// The metadata first: it is the part most likely to refuse, and nothing
	// has been changed yet.
	metaRef := cat.Get("Metadata")
	metaStream, _ := doc.Resolve(metaRef).(*object.Stream)
	packet, err := core.EditableXMP(doc, metaStream)
	if err != nil {
		return err
	}
	if err := writeContainerMetadata(packet, f, docType, fileName, level, title); err != nil {
		return err
	}
	packetBytes, err := packet.Bytes()
	if err != nil {
		return err
	}

	// The previous document of this family, if any, goes: from /AF and from
	// the name tree, and its objects when nothing else refers to them. They
	// are found before anything is written.
	isOurs := func(fs *object.Dictionary) bool {
		ours, _ := f.matchName(fileSpecName(doc, fs))
		return ours
	}
	oursEntry := func(key []byte, v object.Object) bool {
		if ours, _ := f.matchName(core.DecodePDFTextString(key)); ours {
			return true
		}
		fs := doc.ResolveDict(v)
		return fs != nil && isOurs(fs)
	}
	var dropped []int
	names := doc.ResolveDict(cat.Get("Names"))
	var efRoot *object.Dictionary
	if names != nil {
		efRoot = doc.ResolveDict(names.Get("EmbeddedFiles"))
	}
	if efRoot != nil {
		entries, complete := doc.NameTreeEntries(efRoot)
		if !complete {
			return fmt.Errorf("the EmbeddedFiles name tree is cyclic or too deep to insert into")
		}
		for _, e := range entries {
			if oursEntry(e.Key, e.Value) {
				dropped = append(dropped, object.RefNum(e.Value))
			}
		}
	}
	af, _ := doc.Resolve(cat.Get("AF")).(object.Array)
	keptAF := make(object.Array, 0, len(af)+1)
	inAF := map[int]bool{}
	for _, e := range af {
		if fs := doc.ResolveDict(e); fs != nil && isOurs(fs) {
			dropped = append(dropped, object.RefNum(e))
			continue
		}
		// The same file specification listed twice is one association.
		if n := object.RefNum(e); n != 0 {
			if inAF[n] {
				continue
			}
			inAF[n] = true
		}
		keptAF = append(keptAF, e)
	}

	// New objects are numbered by the document's allocator: one past the
	// highest key in Objects can be a number the source file's object streams
	// use (audit 2026-09-22 C3).
	if doc.Alloc == nil {
		return fmt.Errorf("the document view has no object allocator")
	}
	newObj := func(v object.Object) int {
		n := doc.Alloc()
		doc.Objects[n] = &object.IndirectObject{Number: n, Value: v}
		return n
	}

	modDate := "D:" + time.Now().UTC().Format("20060102150405") + "+00'00'"
	ef := object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("EmbeddedFile")},
		object.Entry{Key: "Subtype", Value: object.Name("text/xml")},
		object.Entry{Key: "Params", Value: object.NewDictionary(
			object.Entry{Key: "ModDate", Value: object.String{Value: []byte(modDate)}},
			object.Entry{Key: "Size", Value: object.Integer(len(data))},
		)},
	), append([]byte(nil), data...))
	efNum := newObj(ef)
	fs := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Filespec")},
		object.Entry{Key: "F", Value: object.String{Value: []byte(fileName)}},
		object.Entry{Key: "UF", Value: object.String{Value: EncodeUTF16BE(fileName)}},
		object.Entry{Key: "AFRelationship", Value: object.Name("Data")},
		object.Entry{Key: "Desc", Value: object.String{Value: []byte(desc)}},
		object.Entry{Key: "EF", Value: object.NewDictionary(
			object.Entry{Key: "F", Value: object.IndirectRef{Number: efNum}},
			object.Entry{Key: "UF", Value: object.IndirectRef{Number: efNum}},
		)},
	)
	fsNum := newObj(fs)
	fsRef := object.IndirectRef{Number: fsNum}

	// The insert is the one step left that can refuse (a tree whose shape it
	// cannot place a key in, such as a child without /Limits). It runs before
	// anything already in the document has been touched, and a refusal takes
	// back the two objects just added.
	if efRoot != nil {
		if err := doc.NameTreeInsert(efRoot, []byte(fileName), fsRef); err != nil {
			delete(doc.Objects, efNum)
			delete(doc.Objects, fsNum)
			return fmt.Errorf("the EmbeddedFiles name tree cannot be inserted into: %w", err)
		}
		doc.NameTreeRemove(efRoot, func(key []byte, v object.Object) bool {
			return object.RefNum(v) != fsNum && oursEntry(key, v)
		})
	} else {
		if names == nil {
			names = &object.Dictionary{}
			cat.Set("Names", object.IndirectRef{Number: newObj(names)})
		}
		efRoot = object.NewDictionary(object.Entry{Key: "Names", Value: object.Array{object.String{Value: []byte(fileName)}, fsRef}})
		names.Set("EmbeddedFiles", object.IndirectRef{Number: newObj(efRoot)})
	}

	cat.Set("AF", append(keptAF, fsRef))

	md := core.MetadataStream(packetBytes)
	if n := object.RefNum(metaRef); n != 0 && doc.Objects[n] != nil {
		doc.Objects[n].Value = md
	} else {
		cat.Set("Metadata", object.IndirectRef{Number: newObj(md)})
	}

	dropUnreferenced(doc, dropped)
	return nil
}

// dropUnreferenced deletes the replaced file specifications, and their embedded
// file streams, when nothing in the document refers to them any more. Left in
// the object table they would be written out as unreachable embedded files —
// which a validator that scans every object would still see.
func dropUnreferenced(doc core.View, filespecs []int) {
	candidates := map[int]bool{}
	for _, n := range filespecs {
		if n == 0 || doc.Objects[n] == nil {
			continue
		}
		candidates[n] = true
		if fs := doc.ResolveDict(object.IndirectRef{Number: n}); fs != nil {
			if efd := doc.ResolveDict(fs.Get("EF")); efd != nil {
				for v := range efd.Values() {
					if m := object.RefNum(v); m != 0 {
						candidates[m] = true
					}
				}
			}
		}
	}
	if len(candidates) == 0 {
		return
	}
	// A reference from a candidate to another candidate does not keep either
	// alive; any other reference does.
	referenced := map[int]bool{}
	for num, iobj := range doc.Objects {
		if candidates[num] {
			continue
		}
		collectRefs(iobj.Value, referenced)
	}
	for n := range candidates {
		if !referenced[n] {
			delete(doc.Objects, n)
		}
	}
}

// collectRefs records every object number o refers to, walking arrays and
// dictionaries without recursion so a deep structure cannot exhaust the stack.
func collectRefs(o object.Object, into map[int]bool) {
	stack := []object.Object{o}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v := cur.(type) {
		case object.IndirectRef:
			into[v.Number] = true
		case object.Array:
			stack = append(stack, v...)
		case *object.Dictionary:
			for e := range v.Values() {
				stack = append(stack, e)
			}
		case *object.Stream:
			for e := range v.Dict.Values() {
				stack = append(stack, e)
			}
		}
	}
}

// checkXML reports whether data is a non-empty, well-formed XML document.
func checkXML(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("%w: it is empty", ErrNotXML)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	depth, roots := 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotXML, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) > 0 {
				return fmt.Errorf("%w: text outside the root element", ErrNotXML)
			}
		}
	}
	if roots != 1 {
		return fmt.Errorf("%w: %d root elements", ErrNotXML, roots)
	}
	return nil
}

// EncodeUTF16BE encodes s as a PDF text string: a UTF-16BE byte-order mark
// followed by big-endian code units (used for Unicode file-spec /UF names).
// Characters outside the Basic Multilingual Plane are written as surrogate
// pairs.
func EncodeUTF16BE(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			hi, lo := 0xD800+(r>>10), 0xDC00+(r&0x3FF)
			out = append(out, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
			continue
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return out
}

// XMPPacket builds a fresh XMP metadata packet for a container: PDF/A-3b
// identification, an optional document title, the PDF/A extension schema that
// declares the fx namespace, and the four fx properties. docType INVOICE
// writes the Factur-X namespace; ORDER, ORDER_CHANGE and ORDER_RESPONSE write
// the Order-X one, with order-x.xml as the file name.
//
// Embed does not use it: Embed edits the document's own packet. This is for a
// caller assembling a container by hand.
func XMPPacket(profile formalis.Profile, docType, title string) ([]byte, error) {
	f, fileName := invoiceFamily, facturxFileName
	if strings.HasPrefix(docType, "ORDER") {
		f, fileName = orderFamily, orderXFileName
	}
	p := xmp.New()
	if err := writeContainerMetadata(p, f, docType, fileName, string(profile), title); err != nil {
		return nil, err
	}
	return p.Bytes()
}
