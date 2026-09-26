package pdf0

import (
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
	"io"
	"time"
)

// This file adds a document time-stamp and a Document Security Store (DSS) as an
// incremental update, upgrading a PAdES B-T signature to B-LT and B-LTA. The DSS
// carries the long-term validation material (B-LT); the document time-stamp is
// an RFC 3161 token over the whole file that archives it (B-LTA). The original
// bytes — including any existing signature — are preserved verbatim, so the
// earlier signature stays valid, and every object the update adds or edits is
// one a verifier's allowed-changes analysis accepts as archival (see
// sign.Result.ChangesAllowed).

// ValidationData is the long-term validation material a Document Security
// Store holds (ISO 32000-2 12.8.4.3): the certificates a verifier needs to
// build the signer's and the time-stamp authorities' chains, and the CRLs and
// OCSP responses (DER) that say they were not revoked. Fetching them — from the
// CA's responders, the certificates' AIA and CRL distribution points — is the
// caller's: pdf0 does no network I/O.
type ValidationData struct {
	Certs []*x509.Certificate
	CRLs  [][]byte
	OCSPs [][]byte
}

// WriteArchivalTimestamp writes an incremental update of the file the document
// was read from (see WriteIncremental) that adds data to the document's DSS and
// a document time-stamp over the whole file, issued in-process by the
// time-stamp authority whose certificate and key are given. The document should
// already carry a B-T signature for the result to reach B-LTA.
//
// An existing DSS is extended, never replaced: what it already holds — its
// certificates, CRLs, OCSP responses and /VRI entries — stays reachable, and
// material it already holds (byte for byte) is not added twice. Renewing a
// B-LTA document's archival time-stamp, which ETSI EN 319 142-1 expects to be
// done before the last one's authority certificate expires, therefore keeps
// every earlier piece of revocation evidence (audit 2026-09-22 C60).
func (d *Document) WriteArchivalTimestamp(w io.Writer, data ValidationData, tsaCert *x509.Certificate, tsaKey crypto.Signer) error {
	if err := d.signable("timestamp"); err != nil {
		return err
	}
	doc, changed, err := withArchivalTimestamp(d, data)
	if err != nil {
		return err
	}
	out, err := doc.incrementalBytes(changed)
	if err != nil {
		return err
	}
	out, err = patchDocTimestamp(out, tsaCert, tsaKey)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// withArchivalTimestamp returns a clone with the DSS extended and a document
// time-stamp field added, and the list of changed object numbers for the
// incremental update.
func withArchivalTimestamp(d *Document, data ValidationData) (*Document, []int, error) {
	catalog, page, catNum, pageNum, err := signingTarget(d, "timestamp")
	if err != nil {
		return nil, nil, err
	}
	for i, c := range data.Certs {
		if c == nil {
			return nil, nil, fmt.Errorf("timestamp: certificate %d is nil", i)
		}
	}

	clone := d.updateClone(len(data.Certs) + len(data.CRLs) + len(data.OCSPs) + 5)
	alloc := clone.allocObjNum
	var changed []int
	catClone := catalog.Clone()
	catChanged := false

	// The DSS: the existing one extended, or a new one. Each list keeps the
	// references it has and gains a stream for each new piece of material; an
	// indirect list is copied into the dictionary, which the update rewrites
	// anyway, rather than edited where it lies.
	dss := &object.Dictionary{}
	dssNum := -1
	if existing := d.ResolveDict(catalog.Get("DSS")); existing != nil {
		dss = existing.Clone()
		if n := object.RefNum(catalog.Get("DSS")); n > 0 {
			dssNum = n
		}
	}
	lists := []struct {
		key   object.Name
		items [][]byte
	}{
		{"Certs", derOf(data.Certs)},
		{"CRLs", data.CRLs},
		{"OCSPs", data.OCSPs},
	}
	for _, list := range lists {
		have, _ := d.Resolve(dss.Get(list.key)).(object.Array)
		merged := append(object.Array{}, have...)
		held := map[string]bool{}
		for _, ref := range have {
			if st, ok := d.Resolve(ref).(*object.Stream); ok {
				// reason: an item that did not decode matches nothing, so the new
				// one is added beside it; nothing is lost either way.
				data, _ := d.view().Content(st) // reason: see above
				held[string(data)] = true
			}
		}
		for _, item := range list.items {
			if len(item) == 0 || held[string(item)] {
				continue
			}
			held[string(item)] = true
			n := alloc()
			clone.Objects[n] = &object.IndirectObject{Number: n, Value: object.NewStream(&object.Dictionary{}, item)}
			merged = append(merged, object.IndirectRef{Number: n})
			changed = append(changed, n)
		}
		if len(merged) > len(have) {
			dss.Set(list.key, merged)
		}
	}
	if dssNum < 0 {
		// No DSS, or one stored directly in the catalog: the DSS becomes an
		// object of its own and the catalog points at it.
		dssNum = alloc()
		catClone.Set("DSS", object.IndirectRef{Number: dssNum})
		catChanged = true
	}
	clone.Objects[dssNum] = &object.IndirectObject{Number: dssNum, Value: dss}
	changed = append(changed, dssNum)

	// Document time-stamp signature dictionary and field.
	tsNum, fieldNum := alloc(), alloc()
	ts := &object.Dictionary{}
	ts.Set("Type", object.Name("DocTimeStamp"))
	ts.Set("Filter", object.Name("Adobe.PPKLite"))
	ts.Set("SubFilter", object.Name("ETSI.RFC3161"))
	ts.Set("ByteRange", object.Array{object.Integer(0), object.Integer(9999999999), object.Integer(9999999999), object.Integer(9999999999)})
	ts.Set("Contents", object.String{Value: make([]byte, sigContentsBytes), IsHex: true})
	clone.Objects[tsNum] = &object.IndirectObject{Number: tsNum, Value: ts}

	field := &object.Dictionary{}
	field.Set("Type", object.Name("Annot"))
	field.Set("Subtype", object.Name("Widget"))
	field.Set("FT", object.Name("Sig"))
	field.Set("T", object.String{Value: []byte(freeFieldName(d, catalog, "Timestamp"))})
	field.Set("V", object.IndirectRef{Number: tsNum})
	field.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)})
	field.Set("F", object.Integer(132))
	field.Set("P", object.IndirectRef{Number: pageNum})
	clone.Objects[fieldNum] = &object.IndirectObject{Number: fieldNum, Value: field}
	changed = append(changed, tsNum, fieldNum)

	// Attach the field to the page annotations.
	pageClone := page.Clone()
	annots, _ := d.Resolve(pageClone.Get("Annots")).(object.Array)
	pageClone.Set("Annots", append(append(object.Array{}, annots...), object.IndirectRef{Number: fieldNum}))
	clone.Objects[pageNum] = &object.IndirectObject{Number: pageNum, Value: pageClone}
	changed = append(changed, pageNum)

	// The interactive form, handled exactly as in withSignatureField: an existing
	// one is extended rather than replaced, so no earlier signature's field is
	// orphaned and no ordinary form field is dropped, and the signature bits are
	// OR-ed into /SigFlags (Table 225) so the producer's other bits survive.
	existingForm := d.ResolveDict(catalog.Get("AcroForm"))
	acroForm := &object.Dictionary{}
	if existingForm != nil {
		acroForm = existingForm.Clone()
	}
	fields, _ := d.Resolve(acroForm.Get("Fields")).(object.Array)
	acroForm.Set("Fields", append(append(object.Array{}, fields...), object.IndirectRef{Number: fieldNum}))
	sigFlags, _ := d.Resolve(acroForm.Get("SigFlags")).(object.Integer)
	acroForm.Set("SigFlags", sigFlags|3)

	// Update the existing form object where there is one so the incremental
	// update supersedes it; otherwise allocate. A form stored as a direct
	// dictionary in the catalog is legal (ISO 32000-2 Table 29 does not require
	// /AcroForm to be indirect) but has no object of its own to update —
	// dictObjNum reports -1 — so it is promoted to a new indirect object and the
	// catalog is pointed at it. Writing it under the -1 instead would emit an
	// object with a non-positive number (§7.3.10) that the catalog does not
	// reference.
	formNum := -1
	if existingForm != nil {
		formNum = d.view().DictObjNum(existingForm)
	}
	if formNum < 0 {
		formNum = alloc()
		catClone.Set("AcroForm", object.IndirectRef{Number: formNum})
		catChanged = true
	}
	clone.Objects[formNum] = &object.IndirectObject{Number: formNum, Value: acroForm}
	changed = append(changed, formNum)

	// The catalog is rewritten only when it changed. Repeating it unchanged is
	// harmless, but it is one more object a verifier has to compare.
	if catChanged {
		clone.Objects[catNum] = &object.IndirectObject{Number: catNum, Value: catClone}
		changed = append(changed, catNum)
	}
	return clone, changed, nil
}

// derOf returns the certificates' DER encodings.
func derOf(certs []*x509.Certificate) [][]byte {
	out := make([][]byte, len(certs))
	for i, c := range certs {
		out[i] = c.Raw
	}
	return out
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
