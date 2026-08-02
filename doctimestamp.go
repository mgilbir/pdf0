package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"github.com/mgilbir/pdf0/sign"
	"io"
	"time"
)

// This file adds a document time-stamp and a Document Security Store (DSS) as an
// incremental update, upgrading a PAdES B-T signature to B-LTA. The DSS carries
// the long-term validation material (B-LT); the document time-stamp is an
// RFC 3161 token over the whole file that archives it (B-LTA). The original bytes
// — including any existing signature — are preserved verbatim, so the earlier
// signature stays valid.

// WriteArchivalTimestamp adds a DSS (holding certs as validation material) and a
// document time-stamp over the whole file, as an incremental update. original
// must be the bytes the document was read from. The document should already carry
// a B-T signature for the result to reach B-LTA.
func (d *Document) WriteArchivalTimestamp(w io.Writer, original []byte, certs []*x509.Certificate, tsaCert *x509.Certificate, tsaKey crypto.Signer) error {
	doc, changed, err := withArchivalTimestamp(d, certs)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := doc.WriteIncremental(&buf, original, changed); err != nil {
		return err
	}
	out, err := patchDocTimestamp(buf.Bytes(), tsaCert, tsaKey)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// withArchivalTimestamp returns a clone with a DSS and a document time-stamp field
// added, and the list of changed object numbers for the incremental update.
func withArchivalTimestamp(d *Document, certs []*x509.Certificate) (*Document, []int, error) {
	catalog, page, catNum, pageNum, err := signingTarget(d, "timestamp")
	if err != nil {
		return nil, nil, err
	}

	clone := &Document{
		Version:        d.Version,
		Objects:        make(map[int]*IndirectObject, len(d.Objects)+8),
		Trailer:        *d.Trailer.Clone(),
		usedXRefStream: d.usedXRefStream,
	}
	maxObj := 0
	for num, iobj := range d.Objects {
		clone.Objects[num] = iobj
		if num > maxObj {
			maxObj = num
		}
	}
	next := maxObj
	alloc := func() int { next++; return next }

	// DSS with the validation certificates, each stored as a stream.
	var certRefs Array
	for _, c := range certs {
		n := alloc()
		clone.Objects[n] = &IndirectObject{Number: n, Value: &Stream{Dict: Dictionary{}, Data: c.Raw}}
		certRefs = append(certRefs, IndirectRef{Number: n})
	}
	dssNum := alloc()
	dss := &Dictionary{}
	if len(certRefs) > 0 {
		dss.Set("Certs", certRefs)
	}
	clone.Objects[dssNum] = &IndirectObject{Number: dssNum, Value: dss}

	// Document time-stamp signature dictionary and field.
	tsNum, fieldNum := alloc(), alloc()
	ts := &Dictionary{}
	ts.Set("Type", Name("DocTimeStamp"))
	ts.Set("Filter", Name("Adobe.PPKLite"))
	ts.Set("SubFilter", Name("ETSI.RFC3161"))
	ts.Set("ByteRange", Array{Integer(0), Integer(9999999999), Integer(9999999999), Integer(9999999999)})
	ts.Set("Contents", String{Value: make([]byte, sigContentsBytes), IsHex: true})
	clone.Objects[tsNum] = &IndirectObject{Number: tsNum, Value: ts}

	field := &Dictionary{}
	field.Set("Type", Name("Annot"))
	field.Set("Subtype", Name("Widget"))
	field.Set("FT", Name("Sig"))
	field.Set("T", String{Value: []byte(freeFieldName(d, catalog, "Timestamp"))})
	field.Set("V", IndirectRef{Number: tsNum})
	field.Set("Rect", Array{Integer(0), Integer(0), Integer(0), Integer(0)})
	field.Set("F", Integer(132))
	field.Set("P", IndirectRef{Number: pageNum})
	clone.Objects[fieldNum] = &IndirectObject{Number: fieldNum, Value: field}

	changed := []int{dssNum, tsNum, fieldNum}
	for _, r := range certRefs {
		changed = append(changed, r.(IndirectRef).Number)
	}

	// Attach the field to the page annotations.
	pageClone := page.Clone()
	annots, _ := d.Resolve(pageClone.Get("Annots")).(Array)
	pageClone.Set("Annots", append(append(Array{}, annots...), IndirectRef{Number: fieldNum}))
	clone.Objects[pageNum] = &IndirectObject{Number: pageNum, Value: pageClone}
	changed = append(changed, pageNum)

	// Add /DSS to the catalog, appending the field to an existing AcroForm if any.
	catClone := catalog.Clone()
	catClone.Set("DSS", IndirectRef{Number: dssNum})

	// The interactive form, handled exactly as in withSignatureField: an existing
	// one is extended rather than replaced, so no earlier signature's field is
	// orphaned and no ordinary form field is dropped, and the signature bits are
	// OR-ed into /SigFlags (Table 225) so the producer's other bits survive.
	existingForm := d.ResolveDict(catalog.Get("AcroForm"))
	acroForm := &Dictionary{}
	if existingForm != nil {
		acroForm = existingForm.Clone()
	}
	fields, _ := d.Resolve(acroForm.Get("Fields")).(Array)
	acroForm.Set("Fields", append(append(Array{}, fields...), IndirectRef{Number: fieldNum}))
	sigFlags, _ := d.Resolve(acroForm.Get("SigFlags")).(Integer)
	acroForm.Set("SigFlags", sigFlags|3)

	// Update the existing form object where there is one so the incremental
	// update supersedes it; otherwise allocate. A form stored as a direct
	// dictionary in the catalog is legal (ISO 32000-2 Table 29 does not require
	// /AcroForm to be indirect) but has no object of its own to update —
	// dictObjNum reports -1 — so it is promoted to a new indirect object and the
	// catalog, which is rewritten here anyway, is pointed at it. Writing it under
	// the -1 instead would emit an object with a non-positive number (§7.3.10)
	// that the catalog does not reference.
	formNum := -1
	if existingForm != nil {
		formNum = d.view().DictObjNum(existingForm)
	}
	if formNum < 0 {
		formNum = alloc()
		catClone.Set("AcroForm", IndirectRef{Number: formNum})
	}
	clone.Objects[formNum] = &IndirectObject{Number: formNum, Value: acroForm}
	changed = append(changed, formNum)

	clone.Objects[catNum] = &IndirectObject{Number: catNum, Value: catClone}
	changed = append(changed, catNum)

	return clone, changed, nil
}

// patchDocTimestamp fills the document time-stamp's /ByteRange and /Contents: it
// builds an RFC 3161 token over the byte-range bytes. It targets the time-stamp
// placeholder (the one still carrying the /ByteRange placeholder), leaving an
// earlier, already-filled signature untouched.
func patchDocTimestamp(data []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer) ([]byte, error) {
	slots, err := findSigSlots(data, "timestamp")
	if err != nil {
		return nil, err
	}
	signed, err := fillByteRange(data, slots, "timestamp")
	if err != nil {
		return nil, err
	}
	token, err := sign.BuildTimestampToken(signed, tsaCert, tsaKey, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := fillContents(data, slots, hex.EncodeToString(token), "timestamp"); err != nil {
		return nil, err
	}
	return data, nil
}
