package pdf0

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file tests Read's recovery of files whose cross-reference data is
// damaged: what the scan rebuild finds when the objects a reader needs first
// (the catalog, the encryption dictionary's trailer) live where a byte-level
// scan cannot see them, and what an older section that fails reports.

// objStmFile lays out a PDF 1.7 file with the compressed objects packed into
// object stream 1 (in object-number order) and the uncompressed ones as
// top-level objects. With xrefStream set it ends in a /Type /XRef stream that
// lists every object, whose dictionary carries /Root root when root is
// non-zero. startxref maps the xref stream's offset (0 when there is none) to
// the value written after the startxref keyword.
func objStmFile(compressed, uncompressed map[int]string, root int, xrefStream bool, startxref func(xrefAt int) int) []byte {
	cnums := sortedKeys(compressed)
	var hdr, body bytes.Buffer
	index := map[int]int{}
	for i, n := range cnums {
		fmt.Fprintf(&hdr, "%d %d ", n, body.Len())
		body.WriteString(compressed[n])
		body.WriteByte('\n')
		index[n] = i
	}
	enc := zlibBytes(append(hdr.Bytes(), body.Bytes()...))

	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	offs := map[int]int{1: b.Len()}
	fmt.Fprintf(&b, "1 0 obj\n<< /Type /ObjStm /N %d /First %d /Filter /FlateDecode /Length %d >>\nstream\n", len(cnums), hdr.Len(), len(enc))
	b.Write(enc)
	b.WriteString("\nendstream\nendobj\n")
	for _, n := range sortedKeys(uncompressed) {
		offs[n] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, uncompressed[n])
	}
	xrefAt := 0
	if xrefStream {
		xnum := 1
		for _, n := range append(append([]int{}, cnums...), sortedKeys(uncompressed)...) {
			xnum = max(xnum, n)
		}
		xnum++
		xrefAt = b.Len()
		offs[xnum] = xrefAt
		var rows []byte
		for n := 0; n <= xnum; n++ {
			switch {
			case offs[n] > 0:
				rows = append(append(append(rows, 1), be(offs[n], 4)...), 0, 0)
			case compressed[n] != "":
				rows = append(append(append(rows, 2), be(1, 4)...), be(index[n], 2)...)
			default:
				rows = append(append(append(rows, 0), be(0, 4)...), 0xff, 0xff)
			}
		}
		rz := zlibBytes(rows)
		rootEntry := ""
		if root != 0 {
			rootEntry = fmt.Sprintf(" /Root %d 0 R", root)
		}
		fmt.Fprintf(&b, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 4 2]%s /Filter /FlateDecode /Length %d >>\nstream\n", xnum, xnum+1, rootEntry, len(rz))
		b.Write(rz)
		b.WriteString("\nendstream\nendobj\n")
	}
	fmt.Fprintf(&b, "startxref\n%d\n%%%%EOF\n", startxref(xrefAt))
	return b.Bytes()
}

func sortedKeys(m map[int]string) []int {
	out := make([]int, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// rootNumber is the object number the document's /Root names, or 0.
func rootNumber(doc *Document) int {
	ref, _ := doc.Trailer.Get("Root").(object.IndirectRef)
	return ref.Number
}

// TestRebuildFindsCatalogInObjStm is docs audit 2026-10-11 D4: an
// xref-stream file whose catalog lives in an object stream, read after its
// startxref broke. The scan rebuild finds no traditional trailer and no
// uncompressed catalog; the file opened only once the trailer was recovered
// from the cross-reference stream's dictionary and the object streams were
// unpacked before the catalog was looked for.
func TestRebuildFindsCatalogInObjStm(t *testing.T) {
	compressed := map[int]string{
		2: "<< /Type /Catalog /Pages 3 0 R >>",
		3: "<< /Type /Pages /Kids [4 0 R] /Count 1 >>",
	}
	uncompressed := map[int]string{4: "<< /Type /Page /Parent 3 0 R /MediaBox [0 0 612 792] >>"}
	for _, tc := range []struct {
		name      string
		startxref func(int) int
		rebuilt   bool
	}{
		{"intact", func(at int) int { return at }, false},
		{"startxref inside the stream", func(at int) int { return at + 3 }, true},
		{"startxref outside the file", func(int) int { return 99999999 }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := objStmFile(compressed, uncompressed, 2, true, tc.startxref)
			doc, err := Read(bytes.NewReader(file), int64(len(file)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got := doc.Source().Rebuilt(); got != tc.rebuilt {
				t.Fatalf("Rebuilt() = %v, want %v", got, tc.rebuilt)
			}
			if n := rootNumber(doc); n != 2 {
				t.Errorf("/Root = %v, want 2 0 R", doc.Trailer.Get("Root"))
			}
			if doc.view().Catalog() == nil {
				t.Fatal("catalog not reachable")
			}
			if n := doc.PageCount(); n != 1 {
				t.Errorf("PageCount() = %d, want 1", n)
			}
			for _, key := range []object.Name{"Type", "W", "Size", "Filter", "Length"} {
				if doc.Trailer.Get(key) != nil {
					t.Errorf("trailer keeps the cross-reference stream's /%s", key)
				}
			}
		})
	}
}

// TestRebuildNewestTrailerWins: when the rebuild finds both a trailer keyword
// and a cross-reference stream carrying /Root, the one later in the file is the
// trailer of the newest update (ISO 32000-2 7.5.6) and is used whole.
func TestRebuildNewestTrailerWins(t *testing.T) {
	compressed := map[int]string{
		2: "<< /Type /Catalog /Pages 3 0 R >>",
		3: "<< /Type /Pages /Kids [] /Count 0 >>",
	}
	outside := func(int) int { return 99999999 }
	t.Run("trailer keyword after the stream", func(t *testing.T) {
		file := objStmFile(compressed, nil, 2, true, outside)
		i := bytes.LastIndex(file, []byte("startxref"))
		file = append(append(append([]byte{}, file[:i]...), "trailer\n<< /Root 2 0 R /Marker /Keyword >>\n"...), file[i:]...)
		doc := readRaw(t, file)
		if m, _ := doc.Trailer.Get("Marker").(object.Name); m != "Keyword" {
			t.Errorf("trailer /Marker = %v, want the later trailer keyword's", doc.Trailer.Get("Marker"))
		}
	})
	t.Run("trailer keyword before the stream", func(t *testing.T) {
		// A comment inside object 5 is skipped when the object parses, but the
		// scan for trailer keywords sees it.
		file := objStmFile(compressed, map[int]string{5: "% trailer << /Root 2 0 R /Marker /Keyword >>\n<< /Filler true >>"}, 2, true, outside)
		doc := readRaw(t, file)
		if !bytes.Contains(file, []byte("% trailer << /Root")) {
			t.Fatal("fixture: no trailer keyword")
		}
		if m := doc.Trailer.Get("Marker"); m != nil {
			t.Errorf("trailer /Marker = %v; the older trailer keyword beat the newer stream", m)
		}
		if n := rootNumber(doc); n != 2 {
			t.Errorf("/Root = %v, want 2 0 R", doc.Trailer.Get("Root"))
		}
	})
}

// TestRebuildSynthesizesRootFromObjStm is the other half of D4: no trailer
// survives anywhere — no trailer keyword, no cross-reference stream — and the
// only catalog is compressed. /Root is synthesized from it, which needs the
// object streams unpacked first.
func TestRebuildSynthesizesRootFromObjStm(t *testing.T) {
	file := objStmFile(map[int]string{
		2: "<< /Type /Catalog /Pages 3 0 R >>",
		3: "<< /Type /Pages /Kids [] /Count 0 >>",
	}, nil, 0, false, func(int) int { return 3 })
	doc, err := Read(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !doc.Source().Rebuilt() {
		t.Fatal("not rebuilt; the fixture does not exercise recovery")
	}
	if n := rootNumber(doc); n != 2 {
		t.Fatalf("synthesized /Root = %v, want 2 0 R", doc.Trailer.Get("Root"))
	}
}

// TestRebuildRootSynthesisIsByObjectNumber: with one catalog compressed and
// another uncompressed, and no trailer to choose between them, the catalog with
// the lowest object number becomes /Root — wherever it is stored. Before D4
// only uncompressed objects were candidates, so the compressed catalog lost
// even when it came first.
func TestRebuildRootSynthesisIsByObjectNumber(t *testing.T) {
	const pages = "<< /Type /Pages /Kids [] /Count 0 >>"
	for _, tc := range []struct {
		name                     string
		compressed, uncompressed map[int]string
		want                     int
	}{
		{
			"compressed first",
			map[int]string{2: "<< /Type /Catalog /Pages 3 0 R >>", 3: pages},
			map[int]string{6: "<< /Type /Catalog /Pages 3 0 R >>"},
			2,
		},
		{
			"uncompressed first",
			map[int]string{8: "<< /Type /Catalog /Pages 3 0 R >>", 3: pages},
			map[int]string{6: "<< /Type /Catalog /Pages 3 0 R >>"},
			6,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := objStmFile(tc.compressed, tc.uncompressed, 0, false, func(int) int { return 3 })
			for range 5 { // map iteration must not decide it
				doc, err := Read(bytes.NewReader(file), int64(len(file)))
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				if n := rootNumber(doc); n != tc.want {
					t.Fatalf("synthesized /Root = %v, want %d 0 R", doc.Trailer.Get("Root"), tc.want)
				}
			}
		})
	}
}

// TestRebuildRetryFindsCatalogInObjStm covers the second way into a rebuilt
// table: the cross-reference table parses, so its trailer is used, but every
// offset in it is wrong and the load fails, so Read rebuilds and retries. That
// path unpacks object streams too, and when the trailer names no /Root the
// compressed catalog must still be found.
func TestRebuildRetryFindsCatalogInObjStm(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trailer string
	}{
		{"trailer names the root", "<< /Size 4 /Root 2 0 R >>"},
		{"trailer without root", "<< /Size 4 >>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := objStmFile(map[int]string{
				2: "<< /Type /Catalog /Pages 3 0 R >>",
				3: "<< /Type /Pages /Kids [] /Count 0 >>",
			}, nil, 0, false, func(int) int { return 0 })
			body = body[:bytes.LastIndex(body, []byte("startxref"))]
			xrefAt := len(body)
			// Object 1's offset is shifted 5 bytes, into its own header.
			file := fmt.Appendf(body, "xref\n0 2\n0000000000 65535 f \n%010d 00000 n \ntrailer\n%s\nstartxref\n%d\n%%%%EOF\n",
				bytes.Index(body, []byte("1 0 obj"))+5, tc.trailer, xrefAt)
			doc, err := Read(bytes.NewReader(file), int64(len(file)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !doc.Source().Rebuilt() {
				t.Fatal("not rebuilt; the fixture does not exercise the retry")
			}
			if n := rootNumber(doc); n != 2 {
				t.Errorf("/Root = %v, want 2 0 R", doc.Trailer.Get("Root"))
			}
			if doc.view().Catalog() == nil {
				t.Error("catalog not reachable")
			}
		})
	}
}

// TestRebuildDecryptsObjStmCatalog: the encrypted form of D4. A file pdf0
// writes encrypted with a cross-reference stream keeps /Encrypt and /ID only
// in that stream's dictionary and packs the catalog into an encrypted object
// stream. With startxref broken, the rebuild must find the encryption
// parameters there, decrypt the container, and only then look for the
// catalog.
func TestRebuildDecryptsObjStmCatalog(t *testing.T) {
	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "1.7", usedXRefStream: true}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	d.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	d.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	note := &object.Dictionary{}
	note.Set("Text", object.String{Value: []byte("plaintext survives")})
	d.Objects[3] = &object.IndirectObject{Number: 3, Value: note}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	if err := d.SetEncryption("user", "owner"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	good := buf.Bytes()
	if bytes.Contains(good, []byte("trailer")) || bytes.Contains(good, []byte("plaintext survives")) {
		t.Fatal("fixture: expected no trailer keyword and no plaintext in the file")
	}
	control, err := ReadWithPassword(bytes.NewReader(good), int64(len(good)), "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := control.Source().Offset(1); ok {
		t.Fatal("fixture: the catalog was not packed into an object stream")
	}

	i := bytes.LastIndex(good, []byte("startxref"))
	file := append(append([]byte{}, good[:i]...), "startxref\n99999999\n%%EOF\n"...)
	doc, err := ReadWithPassword(bytes.NewReader(file), int64(len(file)), "user")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !doc.Source().Rebuilt() {
		t.Fatal("not rebuilt")
	}
	if doc.Locked() || !doc.Encrypted {
		t.Fatalf("Locked() = %v, Encrypted = %v; want a decrypted document (lock reason: %v)", doc.Locked(), doc.Encrypted, doc.LockReason())
	}
	if n := rootNumber(doc); n != 1 {
		t.Errorf("/Root = %v, want 1 0 R", doc.Trailer.Get("Root"))
	}
	if len(doc.brokenObjStms)+len(doc.skippedObjStms) > 0 {
		t.Errorf("object streams not unpacked: broken %v, skipped %v", doc.brokenObjStms, doc.skippedObjStms)
	}
	nd := doc.ResolveDict(object.IndirectRef{Number: 3})
	if s, _ := nd.Get("Text").(object.String); string(s.Value) != "plaintext survives" {
		t.Errorf("object 3 /Text = %q, want the decrypted plaintext", s.Value)
	}
}

// TestOlderXRefSectionTripIsReported is docs audit 2026-10-11 D36. The newest
// section is a traditional table whose /Prev names an older cross-reference
// stream that pdf0 declines to decode: over the decoded-stream limit, or under
// a filter it does not implement. The chain is cut there, so objects only
// that section located are missing, and the reason is now recorded as a read
// trip, as it is when the newest section is the one declined. A malformed
// older section stays silent: it is a defect of the file, not a guard pdf0
// applied.
func TestOlderXRefSectionTripIsReported(t *testing.T) {
	head, offs := numberedPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
	})
	build := func(before, filter string, data []byte) []byte {
		var b bytes.Buffer
		b.Write(head)
		b.WriteString(before)
		oldAt := b.Len()
		fmt.Fprintf(&b, "3 0 obj\n<< /Type /XRef /Size 4 /W [1 3 1] /Root 1 0 R%s /Length %d >>\nstream\n", filter, len(data))
		b.Write(data)
		b.WriteString("\nendstream\nendobj\n")
		newAt := b.Len()
		fmt.Fprintf(&b, "xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Size 4 /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
			offs[1], offs[2], oldAt, newAt)
		return b.Bytes()
	}
	var rows []byte
	rows = append(rows, 0, 0, 0, 0, 255)
	for i := 1; i <= 2; i++ {
		rows = append(append(append(rows, 1), be(offs[i], 3)...), 0)
	}
	rows = append(rows, 0, 0, 0, 0, 0)
	flate := build("", " /Filter /FlateDecode", zlibBytes(rows))
	// A standalone "xref" just before the older section is where Read looks
	// for a section that is not at its stated offset; parsing there fails as
	// malformed, which must not hide why the stated section was not read.
	strayKeyword := build("% xref\n", " /Filter /FlateDecode", zlibBytes(rows))

	for _, tc := range []struct {
		name  string
		file  []byte
		opts  []Option
		guard string // "" = no trip expected
	}{
		{"control", flate, nil, ""},
		{"over the decode limit", flate, []Option{WithMaxDecodedStreamBytes(8)}, core.GuardDecodedStream},
		{"over the decode limit, stray xref keyword before it", strayKeyword, []Option{WithMaxDecodedStreamBytes(8)}, core.GuardDecodedStream},
		{"unsupported filter", build("", " /Filter /DCTDecode", rows), nil, core.GuardUnsupportedFilter},
		{"malformed", build("", " /Filter /FlateDecode", []byte("not flate data")), nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := readRaw(t, tc.file, tc.opts...)
			if doc.Source().Rebuilt() {
				t.Fatal("rebuilt; the newest section should have been used")
			}
			wantSections := 1
			if tc.name == "control" {
				wantSections = 2
			}
			if got := len(doc.Source().sections); got != wantSections {
				t.Fatalf("%d sections read, want %d", got, wantSections)
			}
			var msgs []string
			for _, tr := range doc.readLimits.Snapshot() {
				msgs = append(msgs, tr.Message())
			}
			if tc.guard == "" {
				if len(msgs) > 0 {
					t.Errorf("unexpected read trips: %q", msgs)
				}
				return
			}
			if len(msgs) != 1 || !strings.Contains(msgs[0], tc.guard) || !strings.Contains(msgs[0], "older cross-reference section") {
				t.Errorf("read trips = %q, want one %s trip about the older section", msgs, tc.guard)
			}
		})
	}
}
