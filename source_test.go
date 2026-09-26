package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"github.com/mgilbir/pdf0/sign"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// Tests for the source record (source.go), the object-number allocator, and the
// Read and WriteIncremental behaviour that rests on them (audit 2026-09-22 C3,
// C4, C7, C96, C101, C102, C125, C126, C127, C128).

// fileBuilder assembles a PDF byte by byte, so a test can give a file exactly
// the cross-reference structure it is about: tables, streams, hybrids, object
// streams, and incremental updates chained by /Prev.
type fileBuilder struct {
	buf    bytes.Buffer
	offs   map[int]int // object number → offset of its newest definition
	prev   int         // offset of the newest section, -1 before the first
	maxNum int
}

func newFileBuilder() *fileBuilder {
	b := &fileBuilder{offs: map[int]int{}, prev: -1}
	b.buf.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	return b
}

func (b *fileBuilder) note(num int) {
	if num > b.maxNum {
		b.maxNum = num
	}
}

// obj writes "num 0 obj body endobj".
func (b *fileBuilder) obj(num int, body string) {
	b.note(num)
	b.offs[num] = b.buf.Len()
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}

// objStm writes object stream num holding the given objects, in order, and
// returns each one's index.
func (b *fileBuilder) objStm(num int, nums []int, bodies []string) map[int]int {
	var idx, content strings.Builder
	index := map[int]int{}
	for i, n := range nums {
		b.note(n)
		fmt.Fprintf(&idx, "%d %d ", n, content.Len())
		content.WriteString(bodies[i])
		content.WriteString("\n")
		index[n] = i
	}
	data := idx.String() + content.String()
	b.obj(num, fmt.Sprintf("<< /Type /ObjStm /N %d /First %d /Length %d >>\nstream\n%s\nendstream", len(nums), idx.Len(), len(data), data))
	return index
}

// xent is one cross-reference entry: type 0 (free: f2 next, f3 gen), 1 (f2
// offset, f3 gen) or 2 (f2 container, f3 index).
type xent struct{ num, typ, f2, f3 int }

// inUse returns type-1 entries for the newest definitions of nums.
func (b *fileBuilder) inUse(nums ...int) []xent {
	var out []xent
	for _, n := range nums {
		off, ok := b.offs[n]
		if !ok {
			panic(fmt.Sprintf("object %d was never written", n))
		}
		out = append(out, xent{num: n, typ: 1, f2: off})
	}
	return out
}

func sortedXents(es []xent) []xent {
	out := append([]xent(nil), es...)
	sort.Slice(out, func(i, j int) bool { return out[i].num < out[j].num })
	return out
}

func xentIndex(es []xent) string {
	var parts []string
	for i := 0; i < len(es); {
		j := i
		for j+1 < len(es) && es[j+1].num == es[j].num+1 {
			j++
		}
		parts = append(parts, fmt.Sprintf("%d %d", es[i].num, j-i+1))
		i = j + 1
	}
	return strings.Join(parts, " ")
}

func (b *fileBuilder) prevEntry() string {
	if b.prev < 0 {
		return ""
	}
	return fmt.Sprintf(" /Prev %d", b.prev)
}

// xrefStreamBody writes a cross-reference stream object numbered num holding
// entries (plus its own), with the extra trailer text, and returns its offset
// without writing startxref.
func (b *fileBuilder) xrefStreamObject(num int, entries []xent, trailer string) int {
	b.note(num)
	off := b.buf.Len()
	es := sortedXents(append(entries, xent{num: num, typ: 1, f2: off}))
	var data []byte
	for _, e := range es {
		b.note(e.num)
		data = append(data, byte(e.typ), byte(e.f2>>24), byte(e.f2>>16), byte(e.f2>>8), byte(e.f2), byte(e.f3>>8), byte(e.f3))
	}
	fmt.Fprintf(&b.buf, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 4 2] /Index [%s]%s %s /Length %d >>\nstream\n", num, b.maxNum+1, xentIndex(es), b.prevEntry(), trailer, len(data))
	b.buf.Write(data)
	b.buf.WriteString("\nendstream\nendobj\n")
	b.offs[num] = off
	return off
}

// xrefStream writes a cross-reference stream section and its startxref.
func (b *fileBuilder) xrefStream(num int, entries []xent, trailer string) {
	off := b.xrefStreamObject(num, entries, trailer)
	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", off)
	b.prev = off
}

// xrefTable writes a traditional section and its trailer. withHead adds the
// free-list head entry for object 0.
func (b *fileBuilder) xrefTable(entries []xent, withHead bool, trailer string) {
	if withHead {
		entries = append(entries, xent{num: 0, typ: 0, f3: 65535})
	}
	es := sortedXents(entries)
	off := b.buf.Len()
	b.buf.WriteString("xref\n")
	for i := 0; i < len(es); {
		j := i
		for j+1 < len(es) && es[j+1].num == es[j].num+1 {
			j++
		}
		fmt.Fprintf(&b.buf, "%d %d\n", es[i].num, j-i+1)
		for k := i; k <= j; k++ {
			b.note(es[k].num)
			kind := "n"
			if es[k].typ == 0 {
				kind = "f"
			}
			fmt.Fprintf(&b.buf, "%010d %05d %s\r\n", es[k].f2, es[k].f3, kind)
		}
		i = j + 1
	}
	fmt.Fprintf(&b.buf, "trailer\n<< /Size %d%s %s >>\nstartxref\n%d\n%%%%EOF\n", b.maxNum+1, b.prevEntry(), trailer, off)
	b.prev = off
}

func (b *fileBuilder) bytes() []byte { return append([]byte(nil), b.buf.Bytes()...) }

func readBytes(t *testing.T, data []byte) *Document {
	t.Helper()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return doc
}

func flateBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// xrefStreamHighFile is an xref-stream file whose highest numbers are file
// structure Read removes from Objects: object stream 5 (holding object 4) and
// the cross-reference stream 6. Max over Objects is 4.
func xrefStreamHighFile() []byte {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /Info4 4 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] >>")
	idx := b.objStm(5, []int{4}, []string{"<< /Kind /Compressed >>"})
	entries := append(b.inUse(1, 2, 3, 5), xent{num: 4, typ: 2, f2: 5, f3: idx[4]}, xent{num: 0, typ: 0, f3: 65535})
	b.xrefStream(6, entries, "/Root 1 0 R")
	return b.bytes()
}

// TestAllocatorSkipsSourceStructure is audit C3 at its root: every allocation
// path numbers above what the file uses, not above what Objects holds.
func TestAllocatorSkipsSourceStructure(t *testing.T) {
	data := xrefStreamHighFile()
	doc := readBytes(t, data)
	if _, ok := doc.Objects[5]; ok {
		t.Fatal("object stream 5 is still in Objects; the file structure was not normalised away")
	}
	src := doc.Source()
	if got := src.MaxObjectNumber(); got != 6 {
		t.Errorf("MaxObjectNumber = %d, want 6 (the cross-reference stream)", got)
	}
	if got := src.DeclaredSize(); got != 7 {
		t.Errorf("DeclaredSize = %d, want 7", got)
	}
	if ref := doc.Add(object.Integer(1)); ref.Number != 7 {
		t.Errorf("Add allocated %d, want 7 (5 and 6 belong to the file)", ref.Number)
	}
	if ref := doc.Add(object.Integer(2)); ref.Number != 8 {
		t.Errorf("second Add allocated %d, want 8", ref.Number)
	}

	// SetEncryption numbers its /Encrypt dictionary through the same allocator.
	enc := readBytes(t, data)
	if err := enc.SetEncryption("user", "owner"); err != nil {
		t.Fatal(err)
	}
	encRef, _ := enc.Trailer.Get("Encrypt").(object.IndirectRef)
	if encRef.Number <= 6 {
		t.Errorf("SetEncryption numbered /Encrypt %d, a number the file uses", encRef.Number)
	}

	// AppendPages numbers the copied objects through it too.
	app := readBytes(t, data)
	other := readBytes(t, buildMinimalPDF())
	app.AppendPages(other)
	for num := range app.Objects {
		if num == 5 || num == 6 {
			t.Errorf("AppendPages stored an object under %d, a number the source file uses for its structure", num)
		}
	}

	// And the signing writer: its new objects must not redefine 5 or 6.
	signed, changed, err := withSignatureField(readBytes(t, data))
	if err != nil {
		t.Fatal(err)
	}
	for _, num := range changed {
		if num == 5 || num == 6 {
			t.Errorf("withSignatureField wrote object %d, a number the source file uses", num)
		}
	}
	if signed.Source().Len() != int64(len(data)) {
		t.Error("the signing clone lost the source record")
	}
}

// TestViewAllocIsTheDocumentAllocator: a subsystem that adds objects through a
// View (EmbedFacturX) gets the document's allocator, and a hand-built View has
// none.
func TestViewAllocIsTheDocumentAllocator(t *testing.T) {
	doc := readBytes(t, xrefStreamHighFile())
	v := doc.view()
	if v.Alloc == nil {
		t.Fatal("Document.view has no allocator")
	}
	if n := v.Alloc(); n != 7 {
		t.Errorf("View.Alloc = %d, want 7", n)
	}
	if ref := doc.Add(object.Null{}); ref.Number != 8 {
		t.Errorf("Add after View.Alloc = %d, want 8 (the two share one allocator)", ref.Number)
	}
	if (core.View{}).Alloc != nil {
		t.Error("a hand-built View has an allocator")
	}
}

// TestIncrementalAfterAddOnXRefStreamFile is audit C3 scenario (a): Add, then
// WriteIncremental, then re-read, and the new object must resolve.
func TestIncrementalAfterAddOnXRefStreamFile(t *testing.T) {
	orig := xrefStreamHighFile()
	doc := readBytes(t, orig)
	marker := &object.Dictionary{}
	marker.Set("Marker", object.Boolean(true))
	ref := doc.Add(marker)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Set("Extra", ref)

	var out bytes.Buffer
	if err := doc.WriteIncremental(&out, []int{1, ref.Number}); err != nil {
		t.Fatalf("WriteIncremental: %v", err)
	}
	if !bytes.HasPrefix(out.Bytes(), orig) {
		t.Fatal("the update does not begin with the original bytes")
	}
	doc2 := readBytes(t, out.Bytes())
	got := doc2.ResolveDict(doc2.ResolveDict(doc2.Trailer.Get("Root")).Get("Extra"))
	if got == nil || got.Get("Marker") != object.Boolean(true) {
		t.Fatalf("after re-read /Extra (object %d) does not resolve to the marker", ref.Number)
	}
	// The compressed object in the untouched object stream is still there.
	if d := doc2.ResolveDict(object.IndirectRef{Number: 4}); d == nil || d.Get("Kind") != object.Name("Compressed") {
		t.Error("object 4, stored in object stream 5, was lost by the update")
	}
	// An update of a stream-only file is itself a cross-reference stream
	// chained by /Prev, and its /Size is not below the file's.
	secs := doc2.Source().Sections()
	if len(secs) != 2 || secs[0].Kind() != XRefStreamSection || secs[1].Kind() != XRefStreamSection {
		t.Fatalf("sections after update: %v", sectionKinds(secs))
	}
	if sz, _ := secs[0].Trailer().Get("Size").(object.Integer); int(sz) < 8 {
		t.Errorf("the update's /Size is %d, below what the file uses", sz)
	}
	if bytes.Contains(out.Bytes()[len(orig):], []byte("\nxref\n")) || bytes.Contains(out.Bytes()[len(orig):], []byte("trailer")) {
		t.Error("the update of a stream-only file uses the xref/trailer keywords")
	}
}

func sectionKinds(secs []XRefSection) []string {
	var out []string
	for _, s := range secs {
		out = append(out, s.Kind().String())
	}
	return out
}

// TestSignIncrementalOnXRefStreamFile is audit C3 scenario (b): an incremental
// signature over an xref-stream file must be found and verify.
func TestSignIncrementalOnXRefStreamFile(t *testing.T) {
	orig := xrefStreamHighFile()
	cert, key := signtest.CertKey(t)
	var out bytes.Buffer
	if err := readBytes(t, orig).WriteSignedIncremental(&out, cert, key); err != nil {
		t.Fatalf("WriteSignedIncremental: %v", err)
	}
	signed := out.Bytes()
	doc2 := readBytes(t, signed)
	res := verifySigs(t, doc2, sign.VerifyOptions{})
	if len(res) != 1 {
		t.Fatalf("VerifySignatures found %d signatures, want 1", len(res))
	}
	if !res[0].DocumentUnmodified() {
		t.Errorf("the signature does not verify as covering an unmodified document: %+v", res[0])
	}
}

// TestWriteIncrementalRefusesObjectStreamNumber: redefining (or deleting) an
// object stream's number would orphan every object stored in it.
func TestWriteIncrementalRefusesObjectStreamNumber(t *testing.T) {
	doc := readBytes(t, xrefStreamHighFile())
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: object.Integer(1)}
	err := doc.WriteIncremental(&bytes.Buffer{}, []int{5})
	if err == nil || !strings.Contains(err.Error(), "object stream") {
		t.Fatalf("WriteIncremental redefining object stream 5 = %v, want a refusal", err)
	}
	delete(doc.Objects, 5)
	if err := doc.WriteIncremental(&bytes.Buffer{}, []int{5}); err == nil {
		t.Fatal("WriteIncremental deleting object stream 5 was accepted")
	}
}

// TestOlderXRefStreamDoesNotShadowRedefinition is the reader side of C3: an
// update that reuses an older cross-reference stream's number for a real object
// must win, not be skipped as "already loaded" and then deleted.
func TestOlderXRefStreamDoesNotShadowRedefinition(t *testing.T) {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /Extra 3 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.xrefStream(3, append(b.inUse(1, 2), xent{num: 0, typ: 0, f3: 65535}), "/Root 1 0 R")
	// The update redefines 3 as an ordinary object.
	b.obj(3, "<< /Marker /Redefined >>")
	b.xrefStream(4, b.inUse(3), "/Root 1 0 R")
	doc := readBytes(t, b.bytes())
	d := doc.ResolveDict(object.IndirectRef{Number: 3})
	if d == nil || d.Get("Marker") != object.Name("Redefined") {
		t.Fatalf("object 3 = %v, want the update's redefinition", doc.Resolve(object.IndirectRef{Number: 3}))
	}
}

// hybridFile is audit C4's synthetic hybrid: the table marks object 5 free,
// and only the /XRefStm stream (object 4) says it is in object stream 3.
func hybridFile() []byte {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /Extra 5 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	idx := b.objStm(3, []int{5}, []string{"<< /Marker true >>"})
	xs := b.xrefStreamObject(4, []xent{{num: 5, typ: 2, f2: 3, f3: idx[5]}}, "")
	b.xrefTable(append(b.inUse(1, 2, 3), xent{num: 4, typ: 1, f2: xs}, xent{num: 5, typ: 0, f3: 1}), true, fmt.Sprintf("/Root 1 0 R /XRefStm %d", xs))
	return b.bytes()
}

// TestHybridXRefStm is audit C4: the /XRefStm entries are merged at the
// section's precedence, and the object survives Read and Write.
func TestHybridXRefStm(t *testing.T) {
	doc := readBytes(t, hybridFile())
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	extra := doc.ResolveDict(cat.Get("Extra"))
	if extra == nil || extra.Get("Marker") != object.Boolean(true) {
		t.Fatalf("object 5, listed only by /XRefStm, is missing: /Extra = %v", doc.Resolve(cat.Get("Extra")))
	}
	secs := doc.Source().Sections()
	if len(secs) != 1 || secs[0].Kind() != XRefHybridSection {
		t.Fatalf("sections = %v, want one hybrid", sectionKinds(secs))
	}
	if _, num, ok := secs[0].XRefStm(); !ok || num != 4 {
		t.Errorf("XRefStm object = %d, %v; want 4", num, ok)
	}
	if e, ok := secs[0].Entry(5); !ok || !e.Compressed || e.StreamObjNum != 3 {
		t.Errorf("section entry for 5 = %+v, %v; want compressed in 3", e, ok)
	}

	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc2 := readBytes(t, out.Bytes())
	if d := doc2.ResolveDict(doc2.ResolveDict(doc2.Trailer.Get("Root")).Get("Extra")); d == nil || d.Get("Marker") != object.Boolean(true) {
		t.Error("object 5 did not survive Write")
	}
}

// TestHybridTableEntryWins: when a hybrid section's table lists an object in
// use, its /XRefStm entry for the same number does not replace it (the table
// is searched first, ISO 32000-2 7.5.8.4).
func TestHybridTableEntryWins(t *testing.T) {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.obj(5, "<< /From /Table >>")
	idx := b.objStm(3, []int{6}, []string{"<< /From /Stream >>"})
	xs := b.xrefStreamObject(4, []xent{{num: 5, typ: 2, f2: 3, f3: idx[6]}}, "")
	b.xrefTable(append(b.inUse(1, 2, 3, 5), xent{num: 4, typ: 1, f2: xs}), true, fmt.Sprintf("/Root 1 0 R /XRefStm %d", xs))
	doc := readBytes(t, b.bytes())
	if d := doc.ResolveDict(object.IndirectRef{Number: 5}); d == nil || d.Get("From") != object.Name("Table") {
		t.Fatalf("object 5 = %v, want the table's definition", doc.Resolve(object.IndirectRef{Number: 5}))
	}
}

// TestHybridCorpusManual is audit C4 on the real file: the Isartor manual's
// /XRefStm lists 408 compressed objects that Read used to drop.
func TestHybridCorpusManual(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(corpusRoot(t), "Isartor test files", "doc", "Isartor test suite manual.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	doc := readBytes(t, data)
	var hybrid *XRefSection
	secs := doc.Source().Sections()
	for i := range secs {
		if secs[i].Kind() == XRefHybridSection {
			hybrid = &secs[i]
		}
	}
	if hybrid == nil {
		t.Fatalf("no hybrid section found; sections = %v", sectionKinds(secs))
	}
	compressed, missing := 0, 0
	for _, num := range hybrid.Numbers() {
		e, _ := hybrid.Entry(num)
		if !e.Compressed {
			continue
		}
		compressed++
		if doc.Objects[num] == nil {
			missing++
		}
	}
	if compressed != 408 || missing != 0 {
		t.Fatalf("/XRefStm lists %d compressed objects, %d missing; want 408, 0", compressed, missing)
	}

	// Write round-trips every object.
	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	doc2 := readBytes(t, out.Bytes())
	if len(doc2.Objects) != len(doc.Objects) {
		t.Errorf("Write kept %d of %d objects", len(doc2.Objects), len(doc.Objects))
	}
	for num, iobj := range doc.Objects {
		o2 := doc2.Objects[num]
		if o2 == nil || !Equal(iobj.Value, o2.Value) {
			t.Errorf("object %d changed in the round trip", num)
		}
	}
	// A linearized file's first-page section and main section form one
	// revision.
	if revs := doc.Source().Revisions(); len(revs) != 1 || revs[0].End != int64(len(data)) {
		t.Errorf("revisions = %+v, want one ending at %d", revs, len(data))
	}
}

// TestXRefStreamEntryBombs is audit C7: a 19.6 KB file whose cross-reference
// stream decodes to twenty million entries. Free entries are held as runs, so
// a file of them reads in a few megabytes; in-use entries are bounded by what
// the file could hold, so a section listing too many is refused and the table
// rebuilt by scan.
func TestXRefStreamEntryBombs(t *testing.T) {
	const n = 20_000_000
	// bombSection writes an xref stream numbered num with /Index [start n] and
	// every entry the single byte entryByte under /W [1 0 0].
	bombSection := func(t *testing.T, b *fileBuilder, num, start int, entryByte byte, extra string) {
		enc := flateBytes(t, bytes.Repeat([]byte{entryByte}, n))
		off := b.buf.Len()
		fmt.Fprintf(&b.buf, "%d 0 obj\n<< /Type /XRef /Size %d /Index [%d %d] /W [1 0 0] /Root 1 0 R%s %s /Filter /FlateDecode /Length %d >>\nstream\n", num, start+n, start, n, b.prevEntry(), extra, len(enc))
		b.buf.Write(enc)
		fmt.Fprintf(&b.buf, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", off)
		b.prev = off
	}
	// base is a valid one-section file; the bomb is an update chained to it,
	// numbering its entries from 3 so that they hide nothing the base defines.
	withBase := func(t *testing.T, entryByte byte) []byte {
		b := newFileBuilder()
		b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
		b.xrefStream(3, b.inUse(1, 2), "/Root 1 0 R")
		bombSection(t, b, 4, 5, entryByte, "")
		return b.bytes()
	}
	limits := hostile.Limits{MaxRSS: 128 << 20, Timeout: 60 * time.Second}

	t.Run("audit repro", func(t *testing.T) {
		// The audit's file exactly: one section, twenty million free entries
		// from 0, and nothing in use.
		hostile.Run(t, limits, func(t *testing.T) {
			b := newFileBuilder()
			b.obj(1, "<< /Type /Catalog >>")
			bombSection(t, b, 2, 0, 0, "")
			f := b.bytes()
			if len(f) > 20<<10 {
				t.Fatalf("bomb is %d bytes; the point is a small file", len(f))
			}
			doc, err := Read(bytes.NewReader(f), int64(len(f)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if len(doc.Objects) != 0 {
				t.Errorf("%d objects from a table that lists none in use", len(doc.Objects))
			}
			if got := doc.Source().DeclaredSize(); got != n {
				t.Errorf("DeclaredSize = %d, want %d", got, n)
			}
		})
	})
	t.Run("free update", func(t *testing.T) {
		hostile.Run(t, limits, func(t *testing.T) {
			f := withBase(t, 0)
			doc, err := Read(bytes.NewReader(f), int64(len(f)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if doc.Source().Rebuilt() {
				t.Error("an update of free entries was treated as broken")
			}
			if doc.ResolveDict(doc.Trailer.Get("Root")) == nil {
				t.Error("the catalog the base section defines is missing")
			}
			if got := doc.Source().MaxObjectNumber(); got != 5+n-1 {
				t.Errorf("MaxObjectNumber = %d, want %d (free entries count)", got, 5+n-1)
			}
			if ref := doc.Add(object.Null{}); ref.Number != 5+n {
				t.Errorf("Add = %d, want %d (above every number the file uses)", ref.Number, 5+n)
			}
		})
	})
	t.Run("in-use update", func(t *testing.T) {
		hostile.Run(t, limits, func(t *testing.T) {
			f := withBase(t, 1)
			doc, err := Read(bytes.NewReader(f), int64(len(f)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !doc.Source().Rebuilt() {
				t.Error("a section listing twenty million in-use objects in a 20 KB file was accepted")
			}
			if doc.ResolveDict(doc.Trailer.Get("Root")) == nil {
				t.Error("the rebuilt document has no catalog")
			}
		})
	})
}

// TestParseXRefStreamFieldWidth is audit C101: a /W field wider than eight
// bytes is refused, not summed into an overflow that panics.
func TestParseXRefStreamFieldWidth(t *testing.T) {
	for _, w := range []int64{9, math.MaxInt64, math.MaxInt64 / 2} {
		st := &object.Stream{Data: []byte{1, 2, 3, 4, 5, 6}}
		st.Dict.Set("Type", object.Name("XRef"))
		st.Dict.Set("Size", object.Integer(2))
		st.Dict.Set("W", object.Array{object.Integer(1), object.Integer(w), object.Integer(1)})
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("/W [1 %d 1]: ParseXRefStream panicked: %v", w, r)
				}
			}()
			if _, err := ParseXRefStream(st); err == nil || !strings.Contains(err.Error(), "/W[1]") {
				t.Errorf("/W [1 %d 1]: err = %v, want a /W[1] range error", w, err)
			}
		}()
	}
}

// TestXRefSubsectionOverflow is audit C128: a subsection whose start is near
// MaxInt must not wrap into negative object numbers, in either form.
func TestXRefSubsectionOverflow(t *testing.T) {
	st := &object.Stream{Data: []byte{1, 0, 9, 0, 1, 0, 9, 0}}
	st.Dict.Set("Type", object.Name("XRef"))
	st.Dict.Set("W", object.Array{object.Integer(1), object.Integer(2), object.Integer(1)})
	st.Dict.Set("Index", object.Array{object.Integer(math.MaxInt64), object.Integer(2)})
	if tab, err := ParseXRefStream(st); err == nil {
		t.Fatalf("/Index [MaxInt64 2] accepted: %v", tab.Entries)
	}
	st.Dict.Set("Index", object.Array{object.Integer(syntax.MaxObjectNumber), object.Integer(2)})
	if _, err := ParseXRefStream(st); err == nil {
		t.Fatal("/Index running past MaxObjectNumber accepted")
	}
	if _, err := ParseXRefTable([]byte("9223372036854775807 2\n0000000009 00000 n\r\n0000000019 00000 n\r\ntrailer"), 0); err == nil {
		t.Fatal("table subsection starting at MaxInt64 accepted")
	}
	// The largest number itself is fine.
	st.Dict.Set("Index", object.Array{object.Integer(syntax.MaxObjectNumber), object.Integer(1)})
	st.Data = []byte{1, 0, 9, 0}
	if tab, err := ParseXRefStream(st); err != nil || tab.Entries[syntax.MaxObjectNumber].Offset != 9 {
		t.Fatalf("/Index [MaxObjectNumber 1]: %v, %v", tab, err)
	}
}

// danglingContainerFile has object 5 in object stream 9, which the file does
// not contain (or, with container, contains as something else).
func danglingContainerFile(container string) []byte {
	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /Extra 5 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	entries := append(b.inUse(1, 2), xent{num: 5, typ: 2, f2: 9})
	if container != "" {
		b.obj(9, container)
		entries = append(entries, b.inUse(9)...)
	}
	b.xrefStream(3, entries, "/Root 1 0 R")
	return b.bytes()
}

// TestBrokenContainerDoesNotFailRead is audit C102: a type-2 entry whose
// container is missing, not a stream, or holds an unparseable object loses
// that object — recorded, and refused by Write — not the whole file.
func TestBrokenContainerDoesNotFailRead(t *testing.T) {
	cases := map[string]string{
		"missing":      "",
		"not a stream": "<< /Type /ObjStm >>",
		"unparseable":  "<< /Type /ObjStm /N 1 /First 4 /Length 6 >>\nstream\n5 0 [\nendstream",
		"short index":  "<< /Type /ObjStm /N 0 /First 0 /Length 1 >>\nstream\n \nendstream",
	}
	for name, container := range cases {
		t.Run(name, func(t *testing.T) {
			doc := readBytes(t, danglingContainerFile(container))
			if len(doc.brokenObjStms) != 1 || doc.brokenObjStms[0] != 9 {
				t.Errorf("brokenObjStms = %v, want [9]", doc.brokenObjStms)
			}
			if doc.ResolveDict(doc.Trailer.Get("Root")) == nil {
				t.Error("the rest of the document was not read")
			}
			if err := doc.Write(&bytes.Buffer{}); err == nil {
				t.Error("Write accepted a document missing object 5")
			}
		})
	}
}

// TestGenerationAbove65535 is audit C125: an object definition whose
// generation does not fit the 5-digit field is refused by the parser and by
// the rebuild scan alike, and nothing pdf0 writes carries one.
func TestGenerationAbove65535(t *testing.T) {
	p := NewParser([]byte("2 999999 obj\n<< >>\nendobj"))
	if _, err := p.ParseIndirectObject(); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Errorf("ParseIndirectObject(2 999999 obj) = %v, want a generation error", err)
	}
	if _, err := ParseXRefTable([]byte("0 2\n0000000000 65535 f\r\n0000000009 70000 n\r\ntrailer"), 0); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Errorf("table entry with generation 70000 = %v, want a generation error", err)
	}
	st := &object.Stream{Data: []byte{1, 0, 9, 0x01, 0x11, 0x70}} // type 1, offset 9, generation 70000
	st.Dict.Set("Type", object.Name("XRef"))
	st.Dict.Set("Size", object.Integer(1))
	st.Dict.Set("W", object.Array{object.Integer(1), object.Integer(2), object.Integer(3)})
	if _, err := ParseXRefStream(st); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Errorf("stream entry with generation 70000 = %v, want a generation error", err)
	}

	b := newFileBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 3 0 R >>")
	b.obj(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	off := b.buf.Len()
	b.buf.WriteString("2 999999 obj\n<< /Big true >>\nendobj\n")
	b.xrefTable(append(b.inUse(1, 3), xent{num: 2, typ: 1, f2: off, f3: 99999}), true, "/Root 1 0 R")
	doc := readBytes(t, b.bytes())
	if doc.Objects[2] != nil {
		t.Errorf("object 2 with generation 999999 was loaded: %+v", doc.Objects[2])
	}
	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasSuffix(line, " n\r") && len(line) != 19 {
			t.Errorf("malformed xref line %q", line)
		}
	}

	doc.Objects[7] = &object.IndirectObject{Number: 7, Generation: 70000, Value: object.Null{}}
	if err := doc.Write(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Errorf("Write of generation 70000 = %v, want a generation error", err)
	}
	// The same through a cross-reference stream, which has no fixed-width
	// field to overflow: the object header itself must not be written.
	xs := readBytes(t, xrefStreamHighFile())
	xs.Objects[7] = &object.IndirectObject{Number: 7, Generation: 70000, Value: object.Null{}}
	if err := xs.Write(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Errorf("xref-stream Write of generation 70000 = %v, want a generation error", err)
	}
	for _, o := range []*object.IndirectObject{
		{Number: 1, Generation: 70000},
		{Number: 1, Generation: -1},
		{Number: -1}, // 0 parses, and is refused by the document writers instead
		{Number: syntax.MaxObjectNumber + 1},
	} {
		o.Value = object.Null{}
		var buf bytes.Buffer
		if err := NewSerializer(&buf).WriteIndirectObject(o); err == nil || buf.Len() != 0 {
			t.Errorf("WriteIndirectObject(%d %d) = %v with %d bytes, want an error and nothing written", o.Number, o.Generation, err, buf.Len())
		}
	}
}

// TestTrailingJunkAndBadStartXRef is audit C126: more than 1 KB after %%EOF,
// or a startxref past the end of the file, is recovered, not fatal.
func TestTrailingJunkAndBadStartXRef(t *testing.T) {
	f := buildMinimalPDF()
	junk := append(append([]byte{}, f...), make([]byte, 1100)...)
	doc := readBytes(t, junk)
	if doc.Source().Rebuilt() {
		t.Error("trailing junk: the startxref further back was not used")
	}
	if len(doc.PageList()) != 1 {
		t.Errorf("trailing junk: %d pages, want 1", len(doc.PageList()))
	}

	i := bytes.LastIndex(f, []byte("startxref\n"))
	bad := append(append([]byte{}, f[:i]...), []byte("startxref\n99999999\n%%EOF\n")...)
	doc = readBytes(t, bad)
	if !doc.Source().Rebuilt() {
		t.Error("startxref past EOF: expected a scan rebuild")
	}
	if len(doc.PageList()) != 1 {
		t.Errorf("startxref past EOF: %d pages, want 1", len(doc.PageList()))
	}
	if err := doc.WriteIncremental(&bytes.Buffer{}, []int{1}); err == nil {
		t.Error("WriteIncremental chained an update onto a rebuilt cross-reference table")
	}
}

type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

// TestWriteIncrementalHygiene is audit C127.
func TestWriteIncrementalHygiene(t *testing.T) {
	t.Run("error leaves the writer untouched", func(t *testing.T) {
		doc := readBytes(t, buildMinimalPDF())
		doc.Objects[9] = &object.IndirectObject{Number: 9, Value: object.Array{object.Real(math.NaN())}}
		w := &countingWriter{}
		if err := doc.WriteIncremental(w, []int{9}); err == nil {
			t.Fatal("an unserialisable object was accepted")
		}
		if w.n != 0 {
			t.Errorf("the writer received %d bytes before the error", w.n)
		}
	})
	t.Run("second ID refreshed, first kept", func(t *testing.T) {
		b := newFileBuilder()
		b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
		b.xrefTable(b.inUse(1, 2), true, "/Root 1 0 R /ID [<00112233445566778899AABBCCDDEEFF> <00112233445566778899AABBCCDDEEFF>]")
		orig := b.bytes()
		doc := readBytes(t, orig)
		doc.Add(object.Integer(1))
		var out bytes.Buffer
		if err := doc.WriteIncremental(&out, []int{3}); err != nil {
			t.Fatal(err)
		}
		doc2 := readBytes(t, out.Bytes())
		id, _ := doc2.Trailer.Get("ID").(object.Array)
		if len(id) != 2 {
			t.Fatalf("/ID = %v", doc2.Trailer.Get("ID"))
		}
		first, _ := id[0].(object.String)
		second, _ := id[1].(object.String)
		if fmt.Sprintf("%X", first.Value) != "00112233445566778899AABBCCDDEEFF" {
			t.Errorf("first /ID string changed to %X", first.Value)
		}
		if bytes.Equal(second.Value, first.Value) || len(second.Value) != 16 {
			t.Errorf("second /ID string not refreshed: %X", second.Value)
		}
		// Deterministic: the same update yields the same identifier.
		var again bytes.Buffer
		if err := readBytesAdd(t, orig).WriteIncremental(&again, []int{3}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again.Bytes(), out.Bytes()) {
			t.Error("the same update produced different bytes")
		}
	})
	t.Run("freed objects: gen+1, linked list", func(t *testing.T) {
		b := newFileBuilder()
		b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
		b.obj(3, "(three)")
		b.obj(4, "(four)")
		es := b.inUse(1, 2, 3, 4)
		es[3].f3 = 7 // object 4 is at generation 7
		b.xrefTable(es, true, "/Root 1 0 R")
		orig := b.bytes()
		// Rewrite object 4's header to generation 7 so the table and body agree.
		orig = bytes.Replace(orig, []byte("4 0 obj"), []byte("4 7 obj"), 1)
		doc := readBytes(t, orig)
		delete(doc.Objects, 3)
		delete(doc.Objects, 4)
		var out bytes.Buffer
		if err := doc.WriteIncremental(&out, []int{3, 4}); err != nil {
			t.Fatal(err)
		}
		tail := out.String()[len(orig):]
		for _, want := range []string{
			"0000000003 65535 f\r\n", // head → 3
			"0000000004 00001 f\r\n", // 3 → 4, generation 0+1
			"0000000000 00008 f\r\n", // 4 → end, generation 7+1
		} {
			if !strings.Contains(tail, want) {
				t.Errorf("update lacks free entry %q:\n%s", want, tail)
			}
		}
		doc2 := readBytes(t, out.Bytes())
		if doc2.Objects[3] != nil || doc2.Objects[4] != nil {
			t.Error("deleted objects are still present after re-read")
		}
		if sz := doc2.Source().Sections()[0].Trailer().Get("Size"); sz != object.Integer(5) {
			t.Errorf("update /Size = %v, want 5", sz)
		}
	})
	t.Run("size never lowered", func(t *testing.T) {
		b := newFileBuilder()
		b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
		b.xrefTable(b.inUse(1, 2), true, "/Root 1 0 R")
		orig := bytes.Replace(b.bytes(), []byte("/Size 3"), []byte("/Size 40"), 1)
		doc := readBytes(t, orig)
		var out bytes.Buffer
		if err := doc.WriteIncremental(&out, []int{1}); err != nil {
			t.Fatal(err)
		}
		if sz := readBytes(t, out.Bytes()).Source().Sections()[0].Trailer().Get("Size"); sz != object.Integer(40) {
			t.Errorf("update /Size = %v, want the file's 40", sz)
		}
	})
	t.Run("in-memory document refused", func(t *testing.T) {
		doc := NewDocument()
		err := doc.WriteIncremental(&bytes.Buffer{}, []int{1})
		if err == nil || !strings.Contains(err.Error(), "built in memory") {
			t.Errorf("WriteIncremental on an in-memory document = %v", err)
		}
	})
}

func readBytesAdd(t *testing.T, data []byte) *Document {
	doc := readBytes(t, data)
	doc.Add(object.Integer(1))
	return doc
}

// TestSourceRevisions checks the revision boundaries of a file with two
// incremental updates.
func TestSourceRevisions(t *testing.T) {
	orig := buildMinimalPDF()
	doc := readBytes(t, orig)
	var u1 bytes.Buffer
	doc.Add(object.Integer(1))
	if err := doc.WriteIncremental(&u1, []int{4}); err != nil {
		t.Fatal(err)
	}
	doc2 := readBytes(t, u1.Bytes())
	doc2.Add(object.Integer(2))
	var u2 bytes.Buffer
	if err := doc2.WriteIncremental(&u2, []int{5}); err != nil {
		t.Fatal(err)
	}
	src := readBytes(t, u2.Bytes()).Source()
	revs := src.Revisions()
	want := []int64{int64(len(orig)), int64(u1.Len()), int64(u2.Len())}
	if len(revs) != 3 {
		t.Fatalf("revisions = %+v, want 3", revs)
	}
	for i, r := range revs {
		if r.End != want[i] {
			t.Errorf("revision %d ends at %d, want %d", i, r.End, want[i])
		}
		if len(r.Sections) != 1 || r.Sections[0] != 2-i {
			t.Errorf("revision %d sections = %v, want [%d]", i, r.Sections, 2-i)
		}
	}
	secs := src.Sections()
	if secs[0].Revision() != 2 || secs[2].Revision() != 0 {
		t.Errorf("section revisions = %d, %d", secs[0].Revision(), secs[2].Revision())
	}
	if src.Len() != int64(u2.Len()) {
		t.Errorf("Len = %d, want %d", src.Len(), u2.Len())
	}
	buf := make([]byte, 8)
	if _, err := src.ReaderAt().ReadAt(buf, 0); err != nil || string(buf) != "%PDF-2.0" {
		t.Errorf("ReaderAt reads %q, %v", buf, err)
	}
	if e, ok := src.Entry(5); !ok || e.Compressed {
		t.Errorf("Entry(5) = %+v, %v", e, ok)
	}
}

// TestInMemoryDocumentHasEmptySource: a built document reports an empty
// record, never nil.
func TestInMemoryDocumentHasEmptySource(t *testing.T) {
	src := NewDocument().Source()
	if src == nil || src.Len() != 0 || len(src.Sections()) != 0 || src.MaxObjectNumber() != 0 || src.Rebuilt() {
		t.Errorf("in-memory Source = %+v", src)
	}
	if _, ok := src.Offset(1); ok {
		t.Error("in-memory Source reports an offset")
	}
}

// TestAddIsLinear is audit C96: Add must not rescan Objects. Adding four
// times as many objects must cost about four times as long, not sixteen.
// Each size takes the fastest of three runs to keep scheduler noise out.
func TestAddIsLinear(t *testing.T) {
	run := func(n int) time.Duration {
		best := time.Duration(math.MaxInt64)
		for rep := 0; rep < 3; rep++ {
			doc := &Document{}
			start := time.Now()
			for i := 0; i < n; i++ {
				doc.Add(object.Integer(i))
			}
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}
	small, large := run(10_000), run(40_000)
	if large > 10*small {
		t.Fatalf("10k Adds took %v, 40k took %v: growth is superlinear", small, large)
	}
}

// TestAddAfterDirectInsert: a number stored in Objects directly, after the
// allocator has started, is stepped over, not reused.
func TestAddAfterDirectInsert(t *testing.T) {
	doc := &Document{}
	a := doc.Add(object.Integer(1))
	doc.Objects[a.Number+1] = &object.IndirectObject{Number: a.Number + 1, Value: object.Null{}}
	b := doc.Add(object.Integer(2))
	if b.Number == a.Number+1 || b.Number == a.Number {
		t.Fatalf("Add reused %d", b.Number)
	}
	delete(doc.Objects, b.Number)
	if c := doc.Add(object.Integer(3)); c.Number == b.Number {
		t.Fatalf("Add handed out %d twice", c.Number)
	}
}
