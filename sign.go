package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
	"io"
)

// This file produces signed PDFs (ISO 32000-2 §12.8). Signing is inherently a
// two-pass operation: a signature field is added with placeholder /ByteRange
// and a fixed-size hex /Contents, the document is serialized, and only then are
// the real offsets patched in and /Contents filled with the detached CMS built
// in signatures.go — the offsets cannot be known before the bytes exist.
//
// The patching is byte-exact by necessity. /ByteRange must leave exactly one
// gap and that gap must be exactly the /Contents hex string, or a verifier will
// find file bytes that no signature covers. Adding a signature to a document
// that already has one must go through the incremental path, which appends the
// new objects and reproduces the earlier bytes verbatim; a full rewrite would
// invalidate the existing signature.

// sigContentsBytes is the reserved size of the /Contents placeholder (the CMS
// signature is hex-encoded into it). Ample for an RSA-2048 or ECDSA signature
// plus the certificate chain.
const sigContentsBytes = 8192

const byteRangePlaceholder = "0 9999999999 9999999999 9999999999"

// SignOption configures WriteSigned and WriteSignedIncremental.
type SignOption func(*signConfig)

type signConfig struct {
	tsaCert            *x509.Certificate
	tsaKey             crypto.Signer
	invalidateExisting bool
}

// WithSignatureTimestamp embeds an RFC 3161 signature time-stamp over the
// signature value, issued in-process by the time-stamp authority whose
// certificate and key are given, making the signature PAdES B-T. The
// certificate must carry the id-kp-timeStamping extended key usage for a
// verifier to accept the token, and must chain to the verifier's time-stamp
// roots for it to be trusted.
func WithSignatureTimestamp(tsaCert *x509.Certificate, tsaKey crypto.Signer) SignOption {
	return func(c *signConfig) { c.tsaCert, c.tsaKey = tsaCert, tsaKey }
}

// InvalidatingExistingSignatures lets WriteSigned rewrite a document that
// already carries signatures. The rewrite moves every byte the existing
// signatures cover, so they no longer verify: their dictionaries are written
// out, but a verifier reports them invalid. Pass it only when that is what you
// intend; to add a signature and keep the existing ones valid, use
// WriteSignedIncremental. It has no effect on WriteSignedIncremental.
func InvalidatingExistingSignatures() SignOption {
	return func(c *signConfig) { c.invalidateExisting = true }
}

// ErrAlreadySigned is returned by WriteSigned for a document that carries
// signatures (or document time-stamps), which a full rewrite would invalidate.
var ErrAlreadySigned = errors.New("signing: the document already carries signatures, which rewriting it would invalidate; use WriteSignedIncremental to add a signature, or pass InvalidatingExistingSignatures")

func signOptions(opts []SignOption) (signConfig, error) {
	var c signConfig
	for _, o := range opts {
		o(&c)
	}
	if (c.tsaCert == nil) != (c.tsaKey == nil) {
		return c, errors.New("signing: a signature time-stamp needs both the authority's certificate and its key")
	}
	return c, nil
}

// hasSignatures reports whether any object of d is a signature or document
// time-stamp dictionary — the same test the encryption layer and the verifier
// use to find one (crypt.IsSignatureDict).
func (d *Document) hasSignatures() bool {
	for _, iobj := range d.Objects {
		if iobj == nil {
			continue
		}
		if dict, ok := iobj.Value.(*object.Dictionary); ok && crypt.IsSignatureDict(dict) {
			return true
		}
	}
	return false
}

// signable refuses a document a signing writer cannot work on: a nil one, and
// an encrypted one — with the page-tree and metadata writers' refusal when it
// is Locked (the content is still ciphertext), and in any case because a
// signature covers the file's bytes, which encrypting after signing would
// change: sign the plaintext document, then encrypt it.
func (d *Document) signable(op string) error {
	switch {
	case d == nil:
		return errNilDocument
	case d.Locked():
		return errLockedTarget(op)
	case d.Encrypted || d.security != nil:
		return fmt.Errorf("pdf0: %s: cannot sign an encrypted document", op)
	}
	return nil
}

// WriteSigned writes the document with an appended digital signature over its
// whole content: it adds a signature field, serializes with placeholders,
// computes the /ByteRange, signs the covered bytes with key (certificate cert
// embedded, ETSI.CAdES.detached, SHA-256), and fills /Contents. The in-memory
// document is not modified. With WithSignatureTimestamp the signature carries a
// signature time-stamp (PAdES B-T).
//
// It rewrites the whole file, so it refuses a document that already carries a
// signature, with ErrAlreadySigned: the rewrite would silently invalidate it.
// Add a signature to a signed document with WriteSignedIncremental, which
// appends to the file instead; pass InvalidatingExistingSignatures only to
// replace the file and discard the old signatures on purpose.
//
// The document must not be encrypted (sign a plaintext document, or encrypt a
// signed one afterwards).
func (d *Document) WriteSigned(w io.Writer, cert *x509.Certificate, key crypto.Signer, opts ...SignOption) error {
	cfg, err := signOptions(opts)
	if err != nil {
		return err
	}
	if err := d.signable("signing"); err != nil {
		return err
	}
	if !cfg.invalidateExisting && d.hasSignatures() {
		return ErrAlreadySigned
	}
	signedDoc, _, err := withSignatureField(d)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := signedDoc.Write(&buf); err != nil {
		return err
	}
	out, err := patchSignature(buf.Bytes(), cert, key, cfg.tsaCert, cfg.tsaKey)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// WriteSignedIncremental signs the document as an incremental update of the
// file it was read from: those bytes are preserved verbatim and only the
// signature objects are appended (see WriteIncremental). This is the way to add
// a signature without invalidating any signature already present. The
// document must have been read from a file; the new objects are numbered above
// every number that file uses. With WithSignatureTimestamp the signature
// carries a signature time-stamp (PAdES B-T).
//
// A verifier accepts the new signature as a permitted change after each
// earlier signature, unless a certification signature with DocMDP P 1 — or a
// FieldMDP lock on the field signed — forbids it (see
// sign.Result.ChangesAllowed).
func (d *Document) WriteSignedIncremental(w io.Writer, cert *x509.Certificate, key crypto.Signer, opts ...SignOption) error {
	cfg, err := signOptions(opts)
	if err != nil {
		return err
	}
	if err := d.signable("signing"); err != nil {
		return err
	}
	signedDoc, changed, err := withSignatureField(d)
	if err != nil {
		return err
	}
	data, err := signedDoc.incrementalBytes(changed)
	if err != nil {
		return err
	}
	out, err := patchSignature(data, cert, key, cfg.tsaCert, cfg.tsaKey)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// sigSlots are the byte offsets patchSignature and patchDocTimestamp fill in:
// the /ByteRange placeholder digits, and the /Contents hex string including its
// enclosing angle brackets.
type sigSlots struct {
	byteRange     int // start of the /ByteRange placeholder
	contentsStart int // the '<' opening /Contents
	contentsEnd   int // one past the '>' closing /Contents
}

// findSigSlots locates the placeholders of the one signature dictionary that is
// still unfilled, so the caller can patch it in place. what names the caller in
// error messages ("signing", "timestamp").
//
// It anchors on the /ByteRange placeholder and only then searches forward for
// /Contents. Anchoring the other way round — taking the first /Contents in the
// file — is wrong, because a page's content-stream reference (/Contents 5 0 R)
// precedes the signature dictionary in essentially every real document, so the
// search landed on the page and signing failed outright. The placeholder is a
// unique 34-byte literal, and it is unique in a stronger sense than "appears
// once": an already-filled signature carries real offsets there, so this finds
// only the dictionary being signed now and leaves an earlier signature alone.
// The scan runs backwards because in an incremental update the new signature is
// appended after the preserved original bytes.
func findSigSlots(data []byte, what string) (sigSlots, error) {
	var s sigSlots
	pi := bytes.LastIndex(data, []byte(byteRangePlaceholder))
	if pi < 0 {
		return s, fmt.Errorf("%s: /ByteRange placeholder not found", what)
	}
	ci := bytes.Index(data[pi:], []byte("/Contents"))
	if ci < 0 {
		return s, fmt.Errorf("%s: /Contents not found after the /ByteRange placeholder", what)
	}
	ci += pi
	lt := bytes.IndexByte(data[ci:], '<')
	if lt < 0 {
		return s, fmt.Errorf("%s: /Contents value not found", what)
	}
	contentsStart := ci + lt
	gt := bytes.IndexByte(data[contentsStart:], '>')
	if gt < 0 {
		return s, fmt.Errorf("%s: /Contents not terminated", what)
	}
	return sigSlots{byteRange: pi, contentsStart: contentsStart, contentsEnd: contentsStart + gt + 1}, nil
}

// fillByteRange patches the real /ByteRange over the placeholder and returns the
// bytes it covers: everything except the /Contents hex window. The two segments
// must leave exactly one gap, and that gap must be exactly /Contents, or a
// verifier finds file bytes no signature covers.
func fillByteRange(data []byte, s sigSlots, what string) ([]byte, error) {
	len1 := s.contentsStart
	start2, len2 := s.contentsEnd, len(data)-s.contentsEnd
	real := fmt.Sprintf("0 %010d %010d %010d", len1, start2, len2)
	if len(real) != len(byteRangePlaceholder) {
		return nil, fmt.Errorf("%s: /ByteRange width mismatch", what)
	}
	copy(data[s.byteRange:s.byteRange+len(real)], real)
	return append(append([]byte(nil), data[:len1]...), data[start2:start2+len2]...), nil
}

// fillContents writes hex into the reserved /Contents window, zero-padding the
// remainder.
func fillContents(data []byte, s sigSlots, hexValue, what string) error {
	room := s.contentsEnd - 1 - (s.contentsStart + 1)
	if len(hexValue) > room {
		return fmt.Errorf("%s: signature (%d hex) exceeds reserved space (%d)", what, len(hexValue), room)
	}
	region := data[s.contentsStart+1 : s.contentsEnd-1]
	for i := range region {
		region[i] = '0'
	}
	copy(region, hexValue)
	return nil
}

// patchSignature fills the /ByteRange and /Contents placeholders in serialized
// output: it locates the placeholders, patches /ByteRange in place, signs the
// covered bytes, and writes the CMS into /Contents. It works on both a full
// rewrite and an incremental update.
func patchSignature(data []byte, cert *x509.Certificate, key crypto.Signer, tsaCert *x509.Certificate, tsaKey crypto.Signer) ([]byte, error) {
	slots, err := findSigSlots(data, "signing")
	if err != nil {
		return nil, err
	}
	signed, err := fillByteRange(data, slots, "signing")
	if err != nil {
		return nil, err
	}
	cms, err := sign.BuildSignedDataFull(cert, key, signed, tsaCert, tsaKey)
	if err != nil {
		return nil, err
	}
	if err := fillContents(data, slots, hex.EncodeToString(cms), "signing"); err != nil {
		return nil, err
	}
	return data, nil
}

// withSignatureField returns a copy of the document with a signature field, its
// AcroForm entry, and a placeholder /Sig dictionary added.
func withSignatureField(d *Document) (*Document, []int, error) {
	catalog, page, catNum, pageNum, err := signingTarget(d, "signing")
	if err != nil {
		return nil, nil, err
	}

	clone := d.updateClone(4)
	sigNum, fieldNum := clone.allocObjNum(), clone.allocObjNum()

	// Placeholder signature dictionary. /ByteRange before /Contents so the array
	// sits in the first signed segment.
	sig := &object.Dictionary{}
	sig.Set("Type", object.Name("Sig"))
	sig.Set("Filter", object.Name("Adobe.PPKLite"))
	sig.Set("SubFilter", object.Name("ETSI.CAdES.detached"))
	sig.Set("ByteRange", object.Array{object.Integer(0), object.Integer(9999999999), object.Integer(9999999999), object.Integer(9999999999)})
	sig.Set("Contents", object.String{Value: make([]byte, sigContentsBytes), IsHex: true})

	// Signature field / widget annotation.
	field := &object.Dictionary{}
	field.Set("Type", object.Name("Annot"))
	field.Set("Subtype", object.Name("Widget"))
	field.Set("FT", object.Name("Sig"))
	field.Set("T", object.String{Value: []byte(freeFieldName(d, catalog, "Signature"))})
	field.Set("V", object.IndirectRef{Number: sigNum})
	field.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)})
	field.Set("F", object.Integer(132)) // Print | Locked
	field.Set("P", object.IndirectRef{Number: pageNum})

	clone.Objects[sigNum] = &object.IndirectObject{Number: sigNum, Value: sig}
	clone.Objects[fieldNum] = &object.IndirectObject{Number: fieldNum, Value: field}

	// Attach the field to the page (/Annots), cloning it so the caller's document
	// is untouched.
	pageClone := page.Clone()
	annots, _ := d.Resolve(pageClone.Get("Annots")).(object.Array)
	pageClone.Set("Annots", append(append(object.Array{}, annots...), object.IndirectRef{Number: fieldNum}))
	clone.Objects[pageNum] = &object.IndirectObject{Number: pageNum, Value: pageClone}

	changed := []int{sigNum, fieldNum, pageNum}

	// The interactive form. An existing one is extended, never replaced: the new
	// field is appended to whatever /Fields already lists, the signature bits are
	// OR-ed into /SigFlags, and every other key (/DA, /DR, /NeedAppearances, /Q,
	// …) is carried over by the clone. Replacing it would orphan an earlier
	// signature's field — a viewer enumerating the form would see one signature
	// where there are two — and drop every non-signature field from the document.
	existingForm := d.ResolveDict(catalog.Get("AcroForm"))
	acroForm := &object.Dictionary{}
	if existingForm != nil {
		acroForm = existingForm.Clone()
	}
	fields, _ := d.Resolve(acroForm.Get("Fields")).(object.Array)
	acroForm.Set("Fields", append(append(object.Array{}, fields...), object.IndirectRef{Number: fieldNum}))
	// /SigFlags is a bit field (ISO 32000-2 Table 225): bit 1 SignaturesExist,
	// bit 2 AppendOnly. Both are now true, but any other bit the producer set
	// must survive, so OR rather than assign.
	sigFlags, _ := d.Resolve(acroForm.Get("SigFlags")).(object.Integer)
	acroForm.Set("SigFlags", sigFlags|3)

	// Update the existing form object where there is one, so the incremental
	// update supersedes it instead of leaving it behind; otherwise allocate. A
	// form stored directly in the catalog (dictObjNum reports -1) has no object
	// of its own to update, so it is promoted to an indirect object.
	formNum := -1
	if existingForm != nil {
		formNum = d.view().DictObjNum(existingForm)
	}
	if formNum >= 0 {
		clone.Objects[formNum] = &object.IndirectObject{Number: formNum, Value: acroForm}
		changed = append(changed, formNum)
	} else {
		formNum = clone.allocObjNum()
		clone.Objects[formNum] = &object.IndirectObject{Number: formNum, Value: acroForm}
		catClone := catalog.Clone()
		catClone.Set("AcroForm", object.IndirectRef{Number: formNum})
		clone.Objects[catNum] = &object.IndirectObject{Number: catNum, Value: catClone}
		changed = append(changed, formNum, catNum)
	}
	return clone, changed, nil
}

// freeFieldName returns prefix followed by the lowest positive integer that no
// field in the document already uses. ISO 32000-2 §12.7.4.2 requires fully
// qualified field names to be unique, and the new field is added at the top
// level of the form, so its partial name is its qualified name.
//
// The prefix is the caller's: signing passes "Signature" and the document
// time-stamp "Timestamp", so the first of each in a document keeps the
// conventional "Signature1" / "Timestamp1" and a second becomes "Signature2" /
// "Timestamp2". Anything else would be a duplicate name — the time-stamp path
// used to write a literal "Timestamp1" every time, so two archival time-stamps
// produced two fields with one name and sign.Result.Field could not tell
// them apart. The counters are per prefix and the scan is over every name in
// use, so a time-stamp added to an already-signed document is unaffected by the
// signature's number and vice versa.
func freeFieldName(d *Document, catalog *object.Dictionary, prefix string) string {
	used := usedFieldNames(d, catalog)
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s%d", prefix, i)
		if !used[name] {
			return name
		}
	}
}

// usedFieldNames collects the qualified names already taken: every name in the
// interactive form's field tree, plus those of field-like dictionaries the tree
// does not reach. The second pass matters because a signature field can be
// orphaned from /Fields — producers do emit page-only widgets, and pdf0 itself
// did before the form was preserved — and reusing such a name would still be a
// duplicate. Over-collecting is harmless here: it only skips a number.
func usedFieldNames(d *Document, catalog *object.Dictionary) map[string]bool {
	used := map[string]bool{}
	if catalog != nil {
		if form := d.ResolveDict(catalog.Get("AcroForm")); form != nil {
			fields, _ := d.Resolve(form.Get("Fields")).(object.Array)
			seen := map[int]bool{}
			for _, f := range fields {
				collectUsedFieldNames(d, f, "", seen, used, 0)
			}
		}
	}
	for _, iobj := range d.Objects {
		if iobj == nil {
			continue
		}
		fd, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		if fd.Get("FT") == nil && fd.Get("V") == nil {
			continue // not a form field
		}
		if name := signQualifiedFieldName(d.view(), fd); name != "" {
			used[name] = true
		}
	}
	return used
}

// collectUsedFieldNames walks one branch of the field tree, recording the
// qualified name of every node. Depth-capped and cycle-guarded like the naming
// walk in signatures.go: the document may be untrusted.
func collectUsedFieldNames(d *Document, node object.Object, prefix string, seen map[int]bool, used map[string]bool, depth int) {
	if depth > sign.MaxFieldTreeDepth {
		return
	}
	if ref, ok := node.(object.IndirectRef); ok {
		if seen[ref.Number] {
			return // already visited: a cyclic or shared /Kids entry
		}
		seen[ref.Number] = true
	}
	fd := d.ResolveDict(node)
	if fd == nil {
		return
	}
	name := sign.JoinFieldName(prefix, signFieldPartialName(d.view(), fd))
	if name != "" {
		used[name] = true
	}
	kids, _ := d.Resolve(fd.Get("Kids")).(object.Array)
	for _, k := range kids {
		collectUsedFieldNames(d, k, name, seen, used, depth+1)
	}
}

// maxPageTreeDepth caps the page-tree descent below. The tree of an untrusted
// document may be absurdly deep; the cycle guard handles /Kids that point back
// up, this handles the rest.
const maxPageTreeDepth = 64

// firstPage returns the page a signature or document time-stamp widget is
// attached to: the document's first page in reading order, or nil when the page
// tree holds none.
//
// The walk descends into intermediate /Pages nodes. A node's /Kids may mix
// intermediate nodes and leaves in any order (ISO 32000-2 §7.7.3.2), and every
// page tree deeper than one level puts its first page inside an intermediate
// node, so the earlier version — which looked only at the root's /Kids and took
// the first entry typed /Page — found no page at all in such a document, so
// signing and time-stamping refused it outright. Descending agrees with
// PageList: the page signed is the one a reader shows first.
//
// Only the dictionary is returned, deliberately. The widget's /P must be an
// indirect reference to the page whose /Annots carries it (Table 166), and the
// caller takes that object number from signingObjNums, which derives it from
// this very dictionary — so the annotation and its /P cannot name different
// objects. A separate helper that re-walked the tree for the reference is what
// made them disagree: it returned the root's first /Kids entry whether or not
// that entry was a page, and pointed /P at an intermediate /Pages node.
func firstPage(d *Document, catalog *object.Dictionary) *object.Dictionary {
	return firstPageIn(d, catalog.Get("Pages"), map[int]bool{}, 0)
}

// firstPageIn returns the first leaf page of the subtree rooted at node.
func firstPageIn(d *Document, node object.Object, seen map[int]bool, depth int) *object.Dictionary {
	if depth > maxPageTreeDepth {
		return nil
	}
	if ref, ok := node.(object.IndirectRef); ok {
		if seen[ref.Number] {
			return nil // a cycle, or a node reachable by two paths
		}
		seen[ref.Number] = true
	}
	dict := d.ResolveDict(node)
	if dict == nil {
		return nil
	}
	// A leaf counts as a page only when it says so: an untyped leaf was never
	// accepted here and is not now. An untyped node holding /Kids is descended
	// into all the same, since only its children can be pages.
	if t, _ := d.view().ResolveName(dict.Get("Type")); t == "Page" {
		return dict
	}
	kids, _ := d.Resolve(dict.Get("Kids")).(object.Array)
	for _, kid := range kids {
		if pg := firstPageIn(d, kid, seen, depth+1); pg != nil {
			return pg
		}
	}
	return nil
}

// signingTarget resolves what a signature or document time-stamp field attaches
// to: the document catalog and the first page, with the object numbers under
// which both are rewritten. It refuses a document that has no catalog, no page,
// or in which either is a direct object. what names the caller ("signing",
// "timestamp") and prefixes every error.
//
// Signing and time-stamping need exactly this preamble and had grown their own
// copies of it, which drifted — the two reported a missing page with different
// wording, and only one of them checked for a direct object at first.
//
// Both dictionaries are rewritten when a field is added — the page gains the
// widget in its /Annots, the catalog gains an /AcroForm reference or a /DSS —
// and an object with no number of its own cannot be rewritten: an incremental
// update supersedes objects by number, so there would be nothing to supersede
// and the new value would be dropped on the floor (or, worse, written under the
// -1 that dictObjNum reports on a miss). The alternative to refusing is to
// promote the structure to a fresh indirect object, which is what happens to a
// direct /AcroForm below — but that is right there only because a direct
// /AcroForm is legal and only the catalog points at it. A direct catalog or page
// is malformed: ISO 32000-2 §7.5.5 requires the trailer /Root to be an indirect
// reference and §7.7.3.2 requires every page-tree /Kids entry to be one. Rather
// than silently repair a broken file — and change the identity of a structure
// other objects may already reference — signing reports it.
func signingTarget(d *Document, what string) (catalog, page *object.Dictionary, catNum, pageNum int, err error) {
	catalog = d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return nil, nil, 0, 0, fmt.Errorf("%s: document has no catalog", what)
	}
	page = firstPage(d, catalog)
	if page == nil {
		return nil, nil, 0, 0, fmt.Errorf("%s: document has no page to attach the field to", what)
	}
	catNum = d.view().DictObjNum(catalog)
	if catNum < 0 {
		return nil, nil, 0, 0, fmt.Errorf("%s: the document catalog is a direct object, so it cannot be updated; ISO 32000-2 §7.5.5 requires the trailer /Root to be an indirect reference", what)
	}
	pageNum = d.view().DictObjNum(page)
	if pageNum < 0 {
		return nil, nil, 0, 0, fmt.Errorf("%s: the first page is a direct object, so it cannot be updated; ISO 32000-2 §7.7.3.2 requires the page tree's /Kids entries to be indirect references", what)
	}
	return catalog, page, catNum, pageNum, nil
}
