package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// sigContentsBytes is the reserved size of the /Contents placeholder (the CMS
// signature is hex-encoded into it). Ample for an RSA-2048 or ECDSA signature
// plus the certificate chain.
const sigContentsBytes = 8192

const byteRangePlaceholder = "0 9999999999 9999999999 9999999999"

// WriteSigned writes the document with an appended digital signature over its
// whole content: it adds a signature field, serializes with placeholders,
// computes the /ByteRange, signs the covered bytes with key (certificate cert
// embedded, adbe.pkcs7.detached, SHA-256), and fills /Contents. The in-memory
// document is not modified.
//
// The document must not be encrypted (sign a plaintext document, or encrypt a
// signed one afterwards).
func (d *Document) WriteSigned(w io.Writer, cert *x509.Certificate, key crypto.Signer) error {
	if d.Encrypted || d.security != nil {
		return errors.New("cannot sign an encrypted document")
	}
	signedDoc, err := d.withSignatureField()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := signedDoc.Write(&buf); err != nil {
		return err
	}
	data := buf.Bytes()

	// Locate the /Contents placeholder: the hex string after "/Contents".
	ci := bytes.Index(data, []byte("/Contents"))
	if ci < 0 {
		return errors.New("signing: /Contents not found in output")
	}
	lt := bytes.IndexByte(data[ci:], '<')
	if lt < 0 {
		return errors.New("signing: /Contents value not found")
	}
	contentsStart := ci + lt                         // index of '<'
	gt := bytes.IndexByte(data[contentsStart:], '>') // relative
	if gt < 0 {
		return errors.New("signing: /Contents not terminated")
	}
	contentsEnd := contentsStart + gt + 1 // index just after '>'

	// The signed range is everything except the /Contents value (the <...>).
	start1, len1 := 0, contentsStart
	start2, len2 := contentsEnd, len(data)-contentsEnd

	// Patch the /ByteRange placeholder in place (same length so nothing shifts).
	// The leading 0 (start1) is literal; the three fields are len1, start2, len2.
	real := fmt.Sprintf("0 %010d %010d %010d", len1, start2, len2)
	if len(real) != len(byteRangePlaceholder) {
		return errors.New("signing: /ByteRange width mismatch")
	}
	pi := bytes.Index(data, []byte(byteRangePlaceholder))
	if pi < 0 || pi > contentsStart {
		return errors.New("signing: /ByteRange placeholder not found")
	}
	copy(data[pi:pi+len(real)], real)

	// Digest and sign the covered bytes (now that /ByteRange is final).
	signed := append(append([]byte(nil), data[start1:start1+len1]...), data[start2:start2+len2]...)
	cms, err := buildSignedData(cert, key, signed)
	if err != nil {
		return err
	}
	hexSig := hex.EncodeToString(cms)
	room := contentsEnd - 1 - (contentsStart + 1) // hex chars between < and >
	if len(hexSig) > room {
		return fmt.Errorf("signing: signature (%d hex) exceeds reserved space (%d)", len(hexSig), room)
	}
	// Fill the placeholder: signature hex followed by '0' padding.
	region := data[contentsStart+1 : contentsEnd-1]
	for i := range region {
		region[i] = '0'
	}
	copy(region, hexSig)

	_, err = w.Write(data)
	return err
}

// withSignatureField returns a copy of the document with a signature field, its
// AcroForm entry, and a placeholder /Sig dictionary added.
func (d *Document) withSignatureField() (*Document, error) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return nil, errors.New("signing: document has no catalog")
	}
	page := d.firstPage(catalog)
	if page == nil {
		return nil, errors.New("signing: document has no page to attach the signature to")
	}

	clone := &Document{
		Version:        d.Version,
		Objects:        make(map[int]*IndirectObject, len(d.Objects)+3),
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
	sigNum, fieldNum, formNum := maxObj+1, maxObj+2, maxObj+3

	// Placeholder signature dictionary. /ByteRange before /Contents so the array
	// sits in the first signed segment.
	sig := &Dictionary{}
	sig.Set("Type", Name("Sig"))
	sig.Set("Filter", Name("Adobe.PPKLite"))
	sig.Set("SubFilter", Name("adbe.pkcs7.detached"))
	sig.Set("ByteRange", Array{Integer(0), Integer(9999999999), Integer(9999999999), Integer(9999999999)})
	sig.Set("Contents", String{Value: make([]byte, sigContentsBytes), IsHex: true})

	// Signature field / widget annotation.
	field := &Dictionary{}
	field.Set("Type", Name("Annot"))
	field.Set("Subtype", Name("Widget"))
	field.Set("FT", Name("Sig"))
	field.Set("T", String{Value: []byte("Signature1")})
	field.Set("V", IndirectRef{Number: sigNum})
	field.Set("Rect", Array{Integer(0), Integer(0), Integer(0), Integer(0)})
	field.Set("F", Integer(132)) // Print | Locked
	field.Set("P", d.pageRef(catalog))

	acroForm := &Dictionary{}
	acroForm.Set("Fields", Array{IndirectRef{Number: fieldNum}})
	acroForm.Set("SigFlags", Integer(3))

	clone.Objects[sigNum] = &IndirectObject{Number: sigNum, Value: sig}
	clone.Objects[fieldNum] = &IndirectObject{Number: fieldNum, Value: field}
	clone.Objects[formNum] = &IndirectObject{Number: formNum, Value: acroForm}

	// Attach the field to the page (/Annots) and the form to the catalog, cloning
	// both so the caller's document is untouched.
	pageClone := page.Clone()
	annots, _ := d.Resolve(pageClone.Get("Annots")).(Array)
	pageClone.Set("Annots", append(append(Array{}, annots...), IndirectRef{Number: fieldNum}))
	clone.Objects[d.dictObjNum(page)] = &IndirectObject{Number: d.dictObjNum(page), Value: pageClone}

	catClone := catalog.Clone()
	catClone.Set("AcroForm", IndirectRef{Number: formNum})
	clone.Objects[d.dictObjNum(catalog)] = &IndirectObject{Number: d.dictObjNum(catalog), Value: catClone}

	return clone, nil
}

func (d *Document) firstPage(catalog *Dictionary) *Dictionary {
	pages := d.ResolveDict(catalog.Get("Pages"))
	if pages == nil {
		return nil
	}
	kids, _ := d.Resolve(pages.Get("Kids")).(Array)
	for _, kid := range kids {
		if pg := d.ResolveDict(kid); pg != nil {
			if t, _ := pg.Get("Type").(Name); t == "Page" {
				return pg
			}
		}
	}
	return nil
}

func (d *Document) pageRef(catalog *Dictionary) Object {
	pages := d.ResolveDict(catalog.Get("Pages"))
	if pages == nil {
		return Null{}
	}
	kids, _ := d.Resolve(pages.Get("Kids")).(Array)
	if len(kids) > 0 {
		return kids[0]
	}
	return Null{}
}

// dictObjNum finds the object number whose value is the given dictionary.
func (d *Document) dictObjNum(target *Dictionary) int {
	for num, iobj := range d.Objects {
		if dp, ok := iobj.Value.(*Dictionary); ok && dp == target {
			return num
		}
	}
	return -1
}
