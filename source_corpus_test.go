package pdf0

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
	"github.com/mgilbir/pdf0/syntax"
)

// Corpus-wide tests for the source record: the checks that would have caught
// audit 2026-09-22 C3 and C4 the day they were introduced. TestCorpusParsesEntirely
// asserts only that every file reads without error, and a Read that silently
// drops 408 objects (C4) or an incremental writer that produces files no reader
// can use (C3) passes it.

// xrefWalk is an independent walk of a file's cross-reference chain, written
// from ISO 32000-2 7.5 without Read's pipeline: follow startxref and /Prev,
// merge a hybrid section's /XRefStm below its own table (7.5.8.4), and let a
// newer section's entry — free or in use — hide every older one (7.5.6). It
// shares only the section parsers with Read, which the parser tests cover; the
// chain, precedence and hybrid handling it re-derives are exactly what C3 and
// C4 got wrong.
type xrefWalk struct {
	inUse      map[int]XRefEntry // effective in-use entries, number 0 excluded
	containers map[int]bool      // numbers type-2 entries point into
	xrefObjs   map[int]bool      // cross-reference stream object numbers
}

func walkXRefIndependently(data []byte) (*xrefWalk, error) {
	i := bytes.LastIndex(data, []byte("startxref"))
	if i < 0 {
		return nil, fmt.Errorf("no startxref")
	}
	off, err := startXRefValue(data, i)
	if err != nil {
		return nil, err
	}
	w := &xrefWalk{inUse: map[int]XRefEntry{}, containers: map[int]bool{}, xrefObjs: map[int]bool{}}
	var newer []*XRefTable // sections already walked, newest first
	hidden := func(num int) bool {
		for _, t := range newer {
			if _, ok := t.Entries[num]; ok || t.IsFree(num) {
				return true
			}
		}
		return false
	}
	streamAt := func(off int64) (*XRefTable, *object.Dictionary, int, error) {
		if off < 0 || off >= int64(len(data)) {
			return nil, nil, 0, fmt.Errorf("offset %d outside file", off)
		}
		p := syntax.NewParser(data)
		p.Lexer().SetPosition(off)
		iobj, err := p.ParseIndirectObject()
		if err != nil {
			return nil, nil, 0, err
		}
		st, ok := iobj.Value.(*object.Stream)
		if !ok {
			return nil, nil, 0, fmt.Errorf("not a stream")
		}
		t, err := ParseXRefStream(st)
		return t, &st.Dict, iobj.Number, err
	}
	seen := map[int64]bool{}
	for {
		if off < 0 || off >= int64(len(data)) || seen[off] {
			break
		}
		seen[off] = true
		pos := off
		for pos < int64(len(data)) && syntax.IsWhitespace(data[pos]) {
			pos++
		}
		var table *XRefTable
		var trailer *object.Dictionary
		if bytes.HasPrefix(data[pos:], []byte("xref")) {
			table, err = ParseXRefTable(data, pos+4)
			if err != nil {
				return nil, err
			}
			ti := bytes.Index(data[pos:], []byte("trailer"))
			if ti < 0 {
				return nil, fmt.Errorf("no trailer")
			}
			p := syntax.NewParser(data)
			p.Lexer().SetPosition(pos + int64(ti) + 7)
			o, err := p.ParseObject()
			if err != nil {
				return nil, err
			}
			d, ok := o.(*object.Dictionary)
			if !ok {
				return nil, fmt.Errorf("trailer is not a dictionary")
			}
			trailer = d
			if xs, ok := trailer.Get("XRefStm").(object.Integer); ok {
				stm, _, num, err := streamAt(int64(xs))
				if err != nil {
					return nil, fmt.Errorf("/XRefStm: %w", err)
				}
				w.xrefObjs[num] = true
				// Below the table: its entries fill what the table does not
				// list in use.
				merged := &XRefTable{Entries: map[int]XRefEntry{}}
				for n, e := range stm.Entries {
					merged.Entries[n] = e
				}
				for n, e := range table.Entries {
					merged.Entries[n] = e
				}
				merged.Free = append(append([]XRefRange(nil), table.Free...), stm.Free...)
				merged.normalizeFree()
				table = merged
			}
		} else {
			var num int
			table, trailer, num, err = streamAt(off)
			if err != nil {
				return nil, err
			}
			w.xrefObjs[num] = true
		}
		for n, e := range table.Entries {
			if n == 0 || hidden(n) {
				continue
			}
			w.inUse[n] = e
		}
		newer = append(newer, table)
		prev, ok := trailer.Get("Prev").(object.Integer)
		if !ok {
			break
		}
		off = int64(prev)
	}
	for _, e := range w.inUse {
		if e.Compressed {
			w.containers[e.StreamObjNum] = true
		}
	}
	return w, nil
}

// TestCorpusReadKeepsEveryXRefObject: for every corpus file, the objects Read
// materialises are exactly the in-use cross-reference entries across the
// chain, less the ones Read drops on purpose, each for a reason recorded here:
//
//   - file structure: object streams and cross-reference streams, which Write
//     regenerates (normalizeStructure);
//   - objects inside an object stream Read recorded as broken (brokenObjStms),
//     which validation reports and Write refuses;
//   - files whose own cross-reference data Read could not use, and rebuilt by
//     scanning (Source.Rebuilt), where the chain is not what Read loaded;
//   - files whose chain this independent walk cannot follow (it has none of
//     Read's recovery), counted but not compared.
//
// Any other difference fails: an in-use entry Read did not materialise, or an
// object Read holds that no entry lists.
func TestCorpusReadKeepsEveryXRefObject(t *testing.T) {
	files := testfiles.VeraPDFCorpus.Files(t, "", testfiles.IsPDF)
	var compared, rebuilt, unwalkable, structural, inBroken, hybrids int
	var failures []string
	root := corpusRoot(t)
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			failures = append(failures, rel+": Read: "+err.Error())
			continue
		}
		if doc.Source().Rebuilt() {
			rebuilt++
			continue
		}
		w, err := walkXRefIndependently(data)
		if err != nil {
			unwalkable++
			continue
		}
		compared++
		for _, s := range doc.Source().Sections() {
			if s.Kind() == XRefHybridSection {
				hybrids++
			}
		}
		broken := map[int]bool{}
		for _, c := range doc.brokenObjStms {
			broken[c] = true
		}
		var lost []int
		for num, e := range w.inUse {
			if doc.Objects[num] != nil {
				continue
			}
			switch {
			case w.containers[num] || w.xrefObjs[num] || isStructuralAt(data, e):
				structural++
			case e.Compressed && broken[e.StreamObjNum]:
				inBroken++
			default:
				lost = append(lost, num)
			}
		}
		var extra []int
		for num := range doc.Objects {
			if _, ok := w.inUse[num]; !ok {
				extra = append(extra, num)
			}
		}
		if len(lost) > 0 || len(extra) > 0 {
			sort.Ints(lost)
			sort.Ints(extra)
			failures = append(failures, fmt.Sprintf("%s: %d in-use entries not materialised %v, %d objects no entry lists %v", rel, len(lost), head(lost), len(extra), head(extra)))
		}
	}
	t.Logf("%d files: %d compared (%d hybrid sections), %d rebuilt by scan, %d whose chain the independent walk cannot follow; dropped on purpose: %d structural objects, %d objects in broken object streams",
		len(files), compared, hybrids, rebuilt, unwalkable, structural, inBroken)
	if compared < len(files)*9/10 {
		t.Errorf("only %d of %d files were compared; the walk or Read regressed", compared, len(files))
	}
	if hybrids == 0 {
		t.Error("no hybrid-reference section in the corpus was recognised (the Isartor manual has one)")
	}
	if len(failures) > 0 {
		t.Errorf("%d files lost or invented objects:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
}

// isStructuralAt reports whether a type-1 entry's object is an object stream or
// cross-reference stream (which Read drops even when no entry points into it).
func isStructuralAt(data []byte, e XRefEntry) bool {
	if e.Compressed || e.Offset < 0 || e.Offset >= int64(len(data)) {
		return false
	}
	p := syntax.NewParser(data)
	p.Lexer().SetPosition(e.Offset)
	iobj, err := p.ParseIndirectObject()
	if err != nil {
		return false
	}
	st, ok := iobj.Value.(*object.Stream)
	if !ok {
		return false
	}
	typ, _ := st.Dict.Get("Type").(object.Name)
	return typ == "XRef" || typ == "ObjStm"
}

func head(s []int) []int {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// TestCorpusIncrementalUpdatesOfXRefStreamFiles: every file in the veraPDF
// corpus whose newest cross-reference section is a stream takes an incremental
// update, an incremental PAdES B-T signature and an archival time-stamp with
// revocation material (B-LTA), and each result reads back with the new objects
// resolving and every original object intact.
//
// The verifier's verdicts are checked on real files too: the new signature
// must be intact over the signed file, and after the archival update — which
// changes the catalog, the form and a page of a file pdf0 did not write — every
// change must still be recognised as permitted (ChangesAllowed), the
// signature chain to the CA and the time-stamp to the TSA root, the leaf read
// as not revoked, and the PAdES assessment a conformant B-LTA.
//
// Every such file, not a sample: they are 400-odd small files and the whole
// run takes seconds. Files the writers refuse on purpose are counted, by
// reason, and must stay a minority.
func TestCorpusIncrementalUpdatesOfXRefStreamFiles(t *testing.T) {
	ca, caKey := signtest.CA(t, "pdf0 corpus CA")
	cert, key := signtest.Issue(t, &x509.Certificate{
		SerialNumber: big.NewInt(4242),
		Subject:      pkix.Name{CommonName: "pdf0 corpus signer"},
		NotBefore:    signtest.NotBefore,
		NotAfter:     signtest.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}, ca, caKey)
	tsaCert, tsaKey := signtest.TSAIssuedBy(t, ca, caKey)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	opts := sign.VerifyOptions{Roots: roots}
	material := ValidationData{
		Certs: []*x509.Certificate{ca, tsaCert},
		CRLs:  [][]byte{signtest.MakeCRL(t, ca, caKey, nil)},
		OCSPs: [][]byte{signtest.MakeOCSP(t, cert, ca, caKey, "good")},
	}
	root := corpusRoot(t)
	var eligible, refused int
	reasons := map[string]int{}
	var failures []string
	fail := func(rel, format string, args ...any) {
		failures = append(failures, rel+": "+fmt.Sprintf(format, args...))
	}
	for _, path := range testfiles.VeraPDFCorpus.Files(t, "", testfiles.IsPDF) {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		orig, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		secs := orig.Source().Sections()
		if orig.Source().Rebuilt() || len(secs) == 0 || secs[0].Kind() != XRefStreamSection {
			continue
		}
		eligible++
		switch {
		case orig.Encrypted:
			reasons["encrypted"]++
			refused++
			continue
		case len(orig.brokenObjStms) > 0:
			reasons["broken object stream"]++
			refused++
			continue
		}
		catRef, ok := orig.Trailer.Get("Root").(object.IndirectRef)
		if !ok || orig.ResolveDict(catRef) == nil {
			reasons["no indirect catalog"]++
			refused++
			continue
		}

		// 1. Add an object, point the catalog at it, append an update.
		doc := readBytes(t, data)
		marker := &object.Dictionary{}
		marker.Set("PDF0Marker", object.Boolean(true))
		ref := doc.Add(marker)
		if ref.Number <= orig.Source().MaxObjectNumber() {
			fail(rel, "Add allocated %d, not above the file's %d", ref.Number, orig.Source().MaxObjectNumber())
		}
		doc.ResolveDict(catRef).Set("PDF0Marker", ref)
		var out bytes.Buffer
		if err := doc.WriteIncremental(&out, []int{catRef.Number, ref.Number}); err != nil {
			fail(rel, "WriteIncremental: %v", err)
			continue
		}
		re, err := Read(bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			fail(rel, "re-read after WriteIncremental: %v", err)
			continue
		}
		if m := re.ResolveDict(re.ResolveDict(re.Trailer.Get("Root")).Get("PDF0Marker")); m == nil || m.Get("PDF0Marker") != object.Boolean(true) {
			fail(rel, "the added object %d does not resolve after re-read", ref.Number)
		}
		if re.Source().Sections()[0].Kind() != XRefStreamSection {
			fail(rel, "the update of a stream file is a %s section", re.Source().Sections()[0].Kind())
		}
		compareUnchanged(rel, orig, re, map[int]bool{catRef.Number: true}, fail)

		// 2. Sign incrementally; the new signature must verify over the whole
		// file.
		out.Reset()
		if err := readBytes(t, data).WriteSignedIncremental(&out, cert, key, WithSignatureTimestamp(tsaCert, tsaKey)); err != nil {
			if isSigningPrecondition(err) {
				reasons["signing: "+err.Error()]++
				continue
			}
			fail(rel, "WriteSignedIncremental: %v", err)
			continue
		}
		signed := append([]byte(nil), out.Bytes()...)
		sd, err := Read(bytes.NewReader(signed), int64(len(signed)))
		if err != nil {
			fail(rel, "re-read after signing: %v", err)
			continue
		}
		ours := ourSignature(verifySigs(t, sd, opts), cert)
		if ours == nil {
			fail(rel, "VerifySignatures did not find the new signature")
			continue
		}
		if !ours.DocumentUnmodified() || !ours.Intact() || !ours.TrustedChain || !ours.TimestampTrusted {
			fail(rel, "the new signature is not an intact, trusted, time-stamped signature over an unmodified document: valid=%v covers=%v allowed=%v trusted=%v ts=%v err=%v chainErr=%v", ours.Valid, ours.CoversWholeDocument, ours.ChangesAllowed, ours.TrustedChain, ours.TimestampTrusted, ours.Err, ours.ChainErr)
		}
		compareUnchanged(rel, orig, sd, changedBySigning(orig, sd), fail)

		// 3. Archive-time-stamp the signed file: the signature stays valid but
		// no longer covers the whole file, and the time-stamp does.
		out.Reset()
		if err := sd.WriteArchivalTimestamp(&out, material, tsaCert, tsaKey); err != nil {
			fail(rel, "WriteArchivalTimestamp: %v", err)
			continue
		}
		stamped := out.Bytes()
		td, err := Read(bytes.NewReader(stamped), int64(len(stamped)))
		if err != nil {
			fail(rel, "re-read after time-stamping: %v", err)
			continue
		}
		after := ourSignature(verifySigs(t, td, opts), cert)
		if after == nil || !after.Valid || after.CoversWholeDocument {
			fail(rel, "after the time-stamp the signature is %+v; want valid, not covering the new revision", after)
			continue
		}
		if !after.Intact() || !after.TrustedChain || after.Revocation.Status != sign.RevocationGood {
			fail(rel, "after the archival update the signature is not intact, trusted and unrevoked: allowed=%v disallowed=%v trusted=%v revocation=%v", after.ChangesAllowed, after.DisallowedChanges, after.TrustedChain, after.Revocation)
		}
		var lta *sign.PAdESResult
		for _, p := range padesOf(t, td, opts) {
			if p.SignerCommonName == cert.Subject.CommonName {
				lta = &p
			}
		}
		if lta == nil || lta.Level != sign.PAdESBLTA || !lta.Conformant || !lta.ChangesAllowed {
			fail(rel, "the archived signature is not a conformant B-LTA: %+v", lta)
		}
		if !docTimeStampCovers(td, int64(len(stamped))) {
			fail(rel, "no document time-stamp covers the whole time-stamped file")
		}
		if td.ResolveDict(td.ResolveDict(td.Trailer.Get("Root")).Get("DSS")) == nil {
			fail(rel, "the catalog has no /DSS after the time-stamp")
		}
		compareUnchanged(rel, orig, td, changedBySigning(orig, td), fail)
	}
	t.Logf("%d files end in a cross-reference stream; %d refused on purpose: %v", eligible, refused, reasons)
	if eligible < 100 {
		t.Errorf("only %d xref-stream files found; the corpus or the source record regressed", eligible)
	}
	if refused*10 > eligible {
		t.Errorf("%d of %d files refused", refused, eligible)
	}
	if len(failures) > 0 {
		t.Errorf("%d failures:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
}

// compareUnchanged checks that every object of orig, other than the numbers in
// changed, is present and equal in updated.
func compareUnchanged(rel string, orig, updated *Document, changed map[int]bool, fail func(string, string, ...any)) {
	var bad []int
	for num, iobj := range orig.Objects {
		if changed[num] {
			if updated.Objects[num] == nil {
				bad = append(bad, num)
			}
			continue
		}
		u := updated.Objects[num]
		if u == nil || !object.Equal(iobj.Value, u.Value) {
			bad = append(bad, num)
		}
	}
	if len(bad) > 0 {
		sort.Ints(bad)
		fail(rel, "%d original objects missing or changed after the update: %v", len(bad), head(bad))
	}
}

// changedBySigning returns the original objects a signing writer legitimately
// replaces: the catalog, the page the widget is attached to, and the
// interactive form. They are the ones whose value differs in updated and that
// are one of those three kinds; everything else must be unchanged.
func changedBySigning(orig, updated *Document) map[int]bool {
	out := map[int]bool{}
	for num, iobj := range orig.Objects {
		u := updated.Objects[num]
		if u == nil || object.Equal(iobj.Value, u.Value) {
			continue
		}
		d, ok := u.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		t, _ := d.Get("Type").(object.Name)
		if t == "Catalog" || t == "Page" || d.Get("Fields") != nil {
			out[num] = true
		}
	}
	return out
}

func ourSignature(results []sign.Result, cert *x509.Certificate) *sign.Result {
	for i := range results {
		if results[i].SignerCommonName == cert.Subject.CommonName {
			return &results[i]
		}
	}
	return nil
}

// docTimeStampCovers reports whether a /DocTimeStamp dictionary's /ByteRange
// runs from the start to the end of a file of the given size.
func docTimeStampCovers(d *Document, size int64) bool {
	for _, iobj := range d.Objects {
		dict, ok := iobj.Value.(*object.Dictionary)
		if !ok {
			continue
		}
		if tp, _ := dict.Get("Type").(object.Name); tp != "DocTimeStamp" {
			continue
		}
		br, _ := dict.Get("ByteRange").(object.Array)
		if len(br) != 4 {
			continue
		}
		a, _ := br[0].(object.Integer)
		c, _ := br[2].(object.Integer)
		l, _ := br[3].(object.Integer)
		if a == 0 && int64(c)+int64(l) == size {
			return true
		}
	}
	return false
}

// isSigningPrecondition reports the refusals signingTarget makes for documents
// a signature field cannot be added to.
func isSigningPrecondition(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "has no page") || strings.Contains(msg, "is a direct object") || strings.Contains(msg, "has no catalog")
}
