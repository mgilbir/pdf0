package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"sync"
	"testing"
)

// The zero value must be safe: a caller who sets nothing gets the defaults, and
// so does a hand-built Document that never went through Read.
func TestLimitsZeroValueMeansDefaults(t *testing.T) {
	want := core.DefaultLimits()

	if got := resolveLimits(nil); got != want {
		t.Errorf("resolveLimits(nil) = %+v, want %+v", got, want)
	}
	if got := (&Document{}).lim(); got != want {
		t.Errorf("hand-built Document limits = %+v, want %+v", got, want)
	}
	var nilDoc *Document
	if got := nilDoc.lim(); got != want {
		t.Errorf("nil Document limits = %+v, want %+v", got, want)
	}
	// withDefaults is idempotent, which is what makes the accessor safe to call
	// on an already-resolved struct.
	if got := want.WithDefaults(); got != want {
		t.Errorf("withDefaults not idempotent: %+v vs %+v", got, want)
	}
}

// Every option must reach the resolved struct, and setting one must not disturb
// the others. A missing field in withDefaults would show up here as a zero.
func TestEveryOptionAppliesAndIsIsolated(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
		get  func(core.Limits) int64
		want int64
	}{
		{"DecodedStreamBytes", WithMaxDecodedStreamBytes(1234), func(l core.Limits) int64 { return int64(l.DecodedStreamBytes) }, 1234},
		{"DecodedContentBytes", WithMaxDecodedContentBytes(2345), func(l core.Limits) int64 { return l.DecodedContentBytes }, 2345},
		{"ObjectStreamBytes", WithMaxObjectStreamBytes(3456), func(l core.Limits) int64 { return l.ObjectStreamBytes }, 3456},
		{"ContentStreamBytes", WithMaxContentStreamBytes(4567), func(l core.Limits) int64 { return int64(l.ContentStreamBytes) }, 4567},
		{"ICCProfileBytes", WithMaxICCProfileBytes(5678), func(l core.Limits) int64 { return int64(l.ICCProfileBytes) }, 5678},
		{"XMPPacketBytes", WithMaxXMPPacketBytes(6789), func(l core.Limits) int64 { return int64(l.XMPPacketBytes) }, 6789},
		{"CIDRangeSpan", WithMaxCIDRangeSpan(7890), func(l core.Limits) int64 { return int64(l.CIDRangeSpan) }, 7890},
		{"RoleMapSteps", WithMaxRoleMapSteps(8901), func(l core.Limits) int64 { return int64(l.RoleMapSteps) }, 8901},
		{"TableGridFills", WithMaxTableGridFills(9012), func(l core.Limits) int64 { return l.TableGridFills }, 9012},
		{"PostScriptSteps", WithMaxPostScriptSteps(1357), func(l core.Limits) int64 { return int64(l.PostScriptSteps) }, 1357},
		{"CmapWork", WithMaxCmapWork(2468), func(l core.Limits) int64 { return int64(l.CmapWork) }, 2468},
	}
	def := core.DefaultLimits()
	for _, tc := range cases {
		got := resolveLimits([]Option{tc.opt})
		if v := tc.get(got); v != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, v, tc.want)
		}
		// Exactly one field may differ from the defaults.
		diffs := 0
		for _, other := range cases {
			if other.get(got) != other.get(def) {
				diffs++
			}
		}
		if diffs != 1 {
			t.Errorf("%s: %d fields differ from defaults, want exactly 1", tc.name, diffs)
		}
	}
}

// Options apply in order, so a later one wins — the usual functional-option
// contract.
func TestLaterOptionWins(t *testing.T) {
	l := resolveLimits([]Option{WithMaxDecodedStreamBytes(1 << 20), WithMaxDecodedStreamBytes(2 << 20)})
	if l.DecodedStreamBytes != 2<<20 {
		t.Errorf("got %d, want %d", l.DecodedStreamBytes, 2<<20)
	}
}

// The write-side object-stream cap must derive from the reader's cap, never be
// set independently: a container the writer emits but the reader refuses loses
// every object it holds.
func TestObjStmMaxRawDerivesFromDecodedStreamLimit(t *testing.T) {
	l := resolveLimits([]Option{WithMaxDecodedStreamBytes(8192)})
	if got := l.ObjStmMaxRaw(); got != 4096 {
		t.Errorf("objStmMaxRaw = %d, want 4096", got)
	}
	if got := core.DefaultLimits().ObjStmMaxRaw(); got != core.DefaultMaxDecodedStreamBytes/2 {
		t.Errorf("default objStmMaxRaw = %d, want %d", got, core.DefaultMaxDecodedStreamBytes/2)
	}
}

// Read must carry the resolved limits onto the Document so validation and
// extraction inherit what Read was given.
func TestReadCarriesLimitsToDocument(t *testing.T) {
	pdf := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxContentStreamBytes(4242))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := doc.lim().ContentStreamBytes; got != 4242 {
		t.Errorf("Document limits: contentStreamBytes = %d, want 4242", got)
	}
	// Unset options still get defaults.
	if got := doc.lim().DecodedStreamBytes; got != core.DefaultMaxDecodedStreamBytes {
		t.Errorf("unset limit = %d, want default %d", got, core.DefaultMaxDecodedStreamBytes)
	}
}

// The variadic form must keep every existing call site compiling and behaving
// exactly as before.
func TestReadWithoutOptionsIsUnchanged(t *testing.T) {
	pdf := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := doc.lim(); got != core.DefaultLimits() {
		t.Errorf("Read with no options: %+v, want defaults %+v", got, core.DefaultLimits())
	}
	if _, err := ParseXRefStream(&object.Stream{Dict: object.Dictionary{}}); err == nil {
		t.Error("ParseXRefStream with no options should still reject a stream with no /W")
	}
}

// Validating one Document from several goroutines is supported. The limits
// struct travels by value and is never mutated after resolution, so this stays
// race-free; run with -race to make the assertion meaningful.
func TestLimitsSafeUnderConcurrentValidation(t *testing.T) {
	pdf := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxDecodedContentBytes(32<<20))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := doc.lim().DecodedContentBytes; got != 32<<20 {
				t.Errorf("concurrent read of limits = %d, want %d", got, int64(32<<20))
			}
			_ = ValidatePDFA(doc, pdfa.PDFA1b)
		}()
	}
	wg.Wait()
}

// Two documents read with different limits must not interfere — the failure
// mode package-level vars would have had.
func TestLimitsArePerDocument(t *testing.T) {
	pdf := buildMinimalPDF()
	strict, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxDecodedStreamBytes(1<<20))
	if err != nil {
		t.Fatalf("read strict: %v", err)
	}
	loose, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxDecodedStreamBytes(64<<20))
	if err != nil {
		t.Fatalf("read loose: %v", err)
	}
	if strict.lim().DecodedStreamBytes != 1<<20 {
		t.Errorf("strict document limit = %d", strict.lim().DecodedStreamBytes)
	}
	if loose.lim().DecodedStreamBytes != 64<<20 {
		t.Errorf("loose document limit = %d", loose.lim().DecodedStreamBytes)
	}
}

// A lowered decoded-stream cap must actually be enforced at the leaf.
func TestDecodedStreamLimitIsEnforced(t *testing.T) {
	payload := bytes.Repeat([]byte("A"), 256<<10)
	var buf bytes.Buffer
	buf.Write(core.FlateEncode(payload))

	lim := resolveLimits([]Option{WithMaxDecodedStreamBytes(64 << 10)})
	if _, err := core.FlateDecode(core.Canceler{}, buf.Bytes(), lim); err == nil {
		t.Error("expected the lowered decoded-stream cap to reject a 256 KiB payload")
	}
	if got, err := core.FlateDecode(core.Canceler{}, buf.Bytes(), core.DefaultLimits()); err != nil {
		t.Errorf("default limits should accept the same payload: %v", err)
	} else if len(got) != len(payload) {
		t.Errorf("decoded %d bytes, want %d", len(got), len(payload))
	}
}

// The document's resolved limits must reach the file's *own* cross-reference
// stream. It is a Flate stream the file controls like any other, so a caller who
// lowered the decompression-bomb ceiling for untrusted uploads has to get that
// ceiling applied here too; parseXRefSection used to hand parseXRefStream a
// fresh defaultLimits(), silently bypassing the caller's configuration.
//
// Nothing changes under the defaults: doc.lim() is then defaultLimits() field
// for field, and the largest cross-reference stream measured across the veraPDF
// corpus, the Cal Poly PDF/VT suite, the WTPDF set, the Factur-X invoices and
// the PDF 2.0 reference files decodes to 430,350 bytes — 0.4% of the 100 MB
// default, with none above 1 MiB.
func TestXRefStreamHonoursConfiguredDecodeLimit(t *testing.T) {
	doc := &Document{
		Objects:        map[int]*object.IndirectObject{},
		usedXRefStream: true, // makes Write emit a cross-reference stream
		Version:        "2.0",
	}
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", object.IndirectRef{Number: 2})
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: catalog}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	data := buf.Bytes()

	if _, err := Read(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("default limits should read this document: %v", err)
	}
	if _, err := Read(bytes.NewReader(data), int64(len(data)), WithMaxDecodedStreamBytes(4)); err == nil {
		t.Error("a 4-byte decoded-stream cap must not be bypassed for the file's own cross-reference stream")
	}
}
