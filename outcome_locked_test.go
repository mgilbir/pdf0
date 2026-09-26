package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfua"
)

// TestICCLimitIsReported is audit 2026-09-22 C109: a caller's lowered ICC (or
// decode) bound made ICCProfileData return nil and record nothing, so a PDF/A-4
// document validated with no finding of any kind — not a violation, and not
// the "limit" finding README and doc.go promise for every trip.
func TestICCLimitIsReported(t *testing.T) {
	var buf bytes.Buffer
	if err := mustPDFADoc(t, pdfa.PDFA4).Write(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if vs := ValidatePDFA(readRaw(t, raw), pdfa.PDFA4); len(vs) != 0 {
		t.Fatalf("control: the skeleton is not clean: %v", findingKeys(vs))
	}
	for name, opts := range map[string][]Option{
		"ICC bound":            {WithMaxICCProfileBytes(1)},
		"ICC and decode bound": {WithMaxICCProfileBytes(1), WithMaxDecodedStreamBytes(1)},
	} {
		vs := ValidatePDFA(readRaw(t, raw, opts...), pdfa.PDFA4)
		if !hasFinding(vs, "limit", "") {
			t.Errorf("%s: no limit finding at all: %v", name, findingKeys(vs))
		}
		for _, v := range vs {
			if !IsCheckerFinding(v) {
				t.Errorf("%s: a conformance finding from data that was not read: %s %s", name, v.Rule, v.Message)
			}
		}
	}
	if vs := ValidatePDFA(readRaw(t, raw, WithMaxICCProfileBytes(1)), pdfa.PDFA4); !hasFinding(vs, "limit", core.GuardICCProfile) {
		t.Errorf("the ICC bound's trip does not name it: %v", findingKeys(vs))
	}
}

// TestCompressedMetadataOverALimitIsNotJudged: a metadata stream pdf0 declined
// to decode was judged for well-formedness as the bytes it holds — compressed
// bytes, which are not XML — and a conforming document got "the XMP packet is
// not well-formed" (the C47 shape, in checkXMPWellFormed).
func TestCompressedMetadataOverALimitIsNotJudged(t *testing.T) {
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	meta, ok := doc.Resolve(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Metadata")).(*object.Stream)
	if !ok {
		t.Fatal("the skeleton has no metadata stream")
	}
	meta.Data = zlibBytes(meta.Data)
	meta.Dict.Set("Filter", object.Name("FlateDecode"))
	meta.Dict.Set("Length", object.Integer(len(meta.Data)))
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if vs := ValidatePDFA(readRaw(t, raw), pdfa.PDFA2b); len(vs) != 0 {
		t.Fatalf("control: the skeleton with compressed metadata is not clean: %v", findingKeys(vs))
	}
	vs := ValidatePDFA(readRaw(t, raw, WithMaxDecodedStreamBytes(64)), pdfa.PDFA2b)
	for _, v := range vs {
		if !IsCheckerFinding(v) {
			t.Errorf("a conformance finding from metadata pdf0 did not decode: %s %s", v.Rule, v.Message)
		}
	}
	if !hasFinding(vs, "limit", core.GuardDecodedStream) {
		t.Errorf("no decoded-stream-size finding: %v", findingKeys(vs))
	}
}

// lockedUADocument is a document with everything PDF/UA's identification and
// language rules read — a valid /Lang, a pdfuaid:part and a dc:title in its
// metadata — encrypted with a user password.
func lockedUADocument(t *testing.T) []byte {
	t.Helper()
	const packet = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="" xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" xmlns:dc="http://purl.org/dc/elements/1.1/"><pdfuaid:part>1</pdfuaid:part><dc:title><rdf:Alt><rdf:li xml:lang="x-default">A title</rdf:li></rdf:Alt></dc:title></rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
	file := buildRawPDF(onePage(
		rawObj{dict: "<<>>", stream: []byte("q Q")}, "",
		rawObj{dict: "<</Type/Metadata/Subtype/XML>>", stream: []byte(packet)},
	))
	file = bytes.Replace(file, []byte("<</Type/Catalog/Pages 2 0 R>>"), []byte("<</Type/Catalog/Pages 2 0 R/Metadata 5 0 R/Lang(en-US)/MarkInfo<</Marked true>>/ViewerPreferences<</DisplayDocTitle true>>>>"), 1)
	doc := readRaw(t, rebuildXRef(t, file))
	if err := doc.SetEncryption("user-secret", "owner-secret"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rebuildXRef makes a hand-edited file's offsets right again by reading it and
// writing it back.
func rebuildXRef(t *testing.T, file []byte) []byte {
	t.Helper()
	doc := readRaw(t, file)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLockedDocumentIsNotValidatedAsPlaintext is audit 2026-09-22 C63. PDF/UA
// permits encryption, and a document read without its password is Locked: its
// strings and streams are ciphertext. PDF/UA asserted 7.2 ("not a valid
// language identifier") from the ciphertext of en-US, and 5 (no PDF/UA
// identifier) from the ciphertext of the metadata, with no checker finding. A
// Locked run now reports "not decrypted" up front, once, and the string and
// stream checks decline.
func TestLockedDocumentIsNotValidatedAsPlaintext(t *testing.T) {
	file := lockedUADocument(t)
	valueRules := func(vs []pdfua.Violation) []string {
		var out []string
		for _, v := range vs {
			switch {
			case v.Clause == "7.2" && strings.Contains(v.Message, "not a valid language identifier"),
				v.Clause == "5",
				strings.Contains(v.Message, "dc:title"),
				strings.Contains(v.Message, "default language"):
				out = append(out, v.Clause+": "+v.Message)
			}
		}
		return out
	}

	unlocked, err := ReadWithPassword(bytes.NewReader(file), int64(len(file)), "user-secret")
	if err != nil || unlocked.Locked() {
		t.Fatalf("control: the password does not open the fixture (%v)", err)
	}
	if got := valueRules(ValidatePDFUA(unlocked)); len(got) != 0 {
		t.Fatalf("control: the decrypted document fails the rules this test watches: %v", got)
	}

	locked := readRaw(t, file)
	if !locked.Locked() {
		t.Fatal("the fixture is not Locked without its password")
	}
	vs := ValidatePDFUA(locked)
	if got := valueRules(vs); len(got) != 0 {
		t.Errorf("findings asserted from ciphertext: %v", got)
	}
	var notDecrypted int
	for _, v := range vs {
		if v.Clause == "limit" && strings.Contains(v.Message, core.GuardLocked) && strings.Contains(v.Message, "was not decrypted") {
			notDecrypted++
		}
	}
	if notDecrypted != 1 {
		t.Errorf("%d up-front not-decrypted findings, want exactly one: %v", notDecrypted, vs)
	}
	// Every validator carries it.
	if !hasFinding(ValidatePDFA(locked, pdfa.PDFA2b), "limit", core.GuardLocked) {
		t.Error("PDF/A does not report the document as not decrypted")
	}
}

// TestStringsOfALockedDocumentAreCiphertext pins what the producer tells a
// check, independent of any rule.
func TestStringsOfALockedDocumentAreCiphertext(t *testing.T) {
	locked := readRaw(t, lockedUADocument(t))
	cat := locked.view().Catalog()
	if _, r := locked.view().StringValue(cat.Get("Lang")); r != core.ReasonLocked {
		t.Errorf("/Lang of a Locked document: %v, want locked", r)
	}
	if _, st := locked.view().DocumentXMPPacket(); st != core.XMPLimit {
		t.Errorf("metadata of a Locked document: status %v, want XMPLimit (declined)", st)
	}
}
