package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
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
	doc, changed, err := d.withArchivalTimestamp(certs)
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
func (d *Document) withArchivalTimestamp(certs []*x509.Certificate) (*Document, []int, error) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return nil, nil, errors.New("timestamp: document has no catalog")
	}
	page := d.firstPage(catalog)
	if page == nil {
		return nil, nil, errors.New("timestamp: document has no page")
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
	field.Set("T", String{Value: []byte("Timestamp1")})
	field.Set("V", IndirectRef{Number: tsNum})
	field.Set("Rect", Array{Integer(0), Integer(0), Integer(0), Integer(0)})
	field.Set("F", Integer(132))
	field.Set("P", d.pageRef(catalog))
	clone.Objects[fieldNum] = &IndirectObject{Number: fieldNum, Value: field}

	changed := []int{dssNum, tsNum, fieldNum}
	for _, r := range certRefs {
		changed = append(changed, r.(IndirectRef).Number)
	}

	// Attach the field to the page annotations.
	pageClone := page.Clone()
	annots, _ := d.Resolve(pageClone.Get("Annots")).(Array)
	pageClone.Set("Annots", append(append(Array{}, annots...), IndirectRef{Number: fieldNum}))
	pageNum := d.dictObjNum(page)
	clone.Objects[pageNum] = &IndirectObject{Number: pageNum, Value: pageClone}
	changed = append(changed, pageNum)

	// Add /DSS to the catalog, appending the field to an existing AcroForm if any.
	catClone := catalog.Clone()
	catClone.Set("DSS", IndirectRef{Number: dssNum})
	if af := d.ResolveDict(catalog.Get("AcroForm")); af != nil {
		afClone := af.Clone()
		fields, _ := d.Resolve(afClone.Get("Fields")).(Array)
		afClone.Set("Fields", append(append(Array{}, fields...), IndirectRef{Number: fieldNum}))
		afNum := d.dictObjNum(af)
		clone.Objects[afNum] = &IndirectObject{Number: afNum, Value: afClone}
		changed = append(changed, afNum)
	} else {
		afNum := alloc()
		af2 := &Dictionary{}
		af2.Set("Fields", Array{IndirectRef{Number: fieldNum}})
		af2.Set("SigFlags", Integer(3))
		clone.Objects[afNum] = &IndirectObject{Number: afNum, Value: af2}
		catClone.Set("AcroForm", IndirectRef{Number: afNum})
		changed = append(changed, afNum)
	}
	catNum := d.dictObjNum(catalog)
	clone.Objects[catNum] = &IndirectObject{Number: catNum, Value: catClone}
	changed = append(changed, catNum)

	return clone, changed, nil
}

// patchDocTimestamp fills the document time-stamp's /ByteRange and /Contents: it
// builds an RFC 3161 token over the byte-range bytes. It targets the time-stamp
// placeholder (the one still carrying the /ByteRange placeholder), leaving an
// earlier, already-filled signature untouched.
func patchDocTimestamp(data []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer) ([]byte, error) {
	pi := bytes.Index(data, []byte(byteRangePlaceholder))
	if pi < 0 {
		return nil, errors.New("timestamp: /ByteRange placeholder not found")
	}
	ci := bytes.Index(data[pi:], []byte("/Contents"))
	if ci < 0 {
		return nil, errors.New("timestamp: /Contents not found after placeholder")
	}
	ci += pi
	lt := bytes.IndexByte(data[ci:], '<')
	if lt < 0 {
		return nil, errors.New("timestamp: /Contents value not found")
	}
	contentsStart := ci + lt
	gt := bytes.IndexByte(data[contentsStart:], '>')
	if gt < 0 {
		return nil, errors.New("timestamp: /Contents not terminated")
	}
	contentsEnd := contentsStart + gt + 1

	len1 := contentsStart
	start2, len2 := contentsEnd, len(data)-contentsEnd
	real := fmt.Sprintf("0 %010d %010d %010d", len1, start2, len2)
	if len(real) != len(byteRangePlaceholder) {
		return nil, errors.New("timestamp: /ByteRange width mismatch")
	}
	copy(data[pi:pi+len(real)], real)

	signed := append(append([]byte(nil), data[:len1]...), data[start2:start2+len2]...)
	token, err := buildTimestampToken(signed, tsaCert, tsaKey, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	hexTok := hex.EncodeToString(token)
	room := contentsEnd - 1 - (contentsStart + 1)
	if len(hexTok) > room {
		return nil, fmt.Errorf("timestamp: token (%d hex) exceeds reserved space (%d)", len(hexTok), room)
	}
	region := data[contentsStart+1 : contentsEnd-1]
	for i := range region {
		region[i] = '0'
	}
	copy(region, hexTok)
	return data, nil
}
