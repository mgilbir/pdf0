package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

func TestContentStreamNumberLimits(t *testing.T) {
	lim1b := implLimits{rule: "6.1.12", stringLen: 65535, realLimit: 32767}
	lim2b := implLimits{rule: "6.1.13", stringLen: 32767, realLimit: 3.403e38}
	check := func(lim implLimits, tok string) bool {
		got := false
		checkContentNumberLimit([]byte(tok), lim, 0, func(string, int) { got = true })
		return got
	}
	if !check(lim1b, "60000.1") {
		t.Error("real over 32767 must be flagged at 1b")
	}
	if check(lim1b, "-32767.0") {
		t.Error("32767.0 is within the 1b limit")
	}
	if !check(lim1b, "-32767.9") {
		t.Error("32767.9 exceeds the 1b real limit")
	}
	if check(lim2b, "60000.1") {
		t.Error("60000.1 is within the 2b real limit")
	}
	if !check(lim2b, "2157483648") {
		t.Error("integer over 2^31-1 must be flagged")
	}
	if check(lim2b, "2147483647") {
		t.Error("2^31-1 is the max valid integer")
	}
	// An astronomically large integer exceeds even the real limit (readers
	// convert overflowing integers to reals) and is flagged.
	if !check(lim2b, strings.Repeat("9", 40)) {
		t.Error("astronomically large integer must be flagged as out of range")
	}
	// Every PDF spelling of a number is judged by its value.
	if !check(lim1b, "+32768.") || !check(lim1b, "-32768.5") || check(lim1b, "-.5") || check(lim1b, "+2147483647") {
		t.Error("a signed or point-ended real, or a signed integer, was misjudged")
	}
	if !check(lim2b, "-2147483649") || check(lim2b, "-2147483648") {
		t.Error("the integer limit is [-2^31, 2^31-1]")
	}
	// A token that is not a PDF number (ISO 32000-2 7.3.3 has no exponent and
	// one period at most) has no magnitude to judge. The CFF real reader this
	// used to go through read "1E40" as 10^40 and "1e40" as 140.
	for _, tok := range []string{"1E40", "1e40", "1.2.3", "-Inf"} {
		if check(lim1b, tok) {
			t.Errorf("%q is not a PDF number, and was judged against the real limit", tok)
		}
	}
}

func TestMaxCMapCID(t *testing.T) {
	cmap := "begincidrange\n<0000> <00ff> 0\n<2100> <21ff> 65400\nendcidrange"
	if got := core.CMapMaxCID(core.Canceler{}, []byte(cmap)); got != 65400+0xff {
		t.Errorf("range CID max: got %d", got)
	}
	cmap2 := "begincidchar\n<0041> 70000\nendcidchar"
	if got := core.CMapMaxCID(core.Canceler{}, []byte(cmap2)); got != 70000 {
		t.Errorf("char CID max: got %d", got)
	}
	// Entries on one line, and lines ended by CR alone, are entries all the
	// same: the line-based reader saw only the first of each line.
	cmap3 := "begincidrange <0000> <00ff> 0 <2100> <21ff> 65400 endcidrange\rbegincidchar\r<0041> 70000\rendcidchar"
	if got := core.CMapMaxCID(core.Canceler{}, []byte(cmap3)); got != 70000 {
		t.Errorf("one-line and CR-only CMap: CID max %d, want 70000", got)
	}
}

func TestXPacketHeaderChecks(t *testing.T) {
	if !xpacketHasAttr(`<?xpacket bytes="870" begin='x' id='y'`, "bytes") {
		t.Error("bytes attribute not detected")
	}
	if !xpacketHasAttr(`<?xpacket encoding="UTF-8" begin='x'`, "encoding") {
		t.Error("encoding attribute not detected")
	}
	if xpacketHasAttr(`<?xpacket begin='x' id='y'`, "bytes") {
		t.Error("false positive on clean header")
	}
}

func TestXMPIsUTF8(t *testing.T) {
	if !xmpIsUTF8([]byte("<?xpacket?>plain")) {
		t.Error("plain ASCII is UTF-8")
	}
	if xmpIsUTF8([]byte{0xFE, 0xFF, 0, 'x'}) {
		t.Error("UTF-16BE BOM is not UTF-8")
	}
	if !xmpIsUTF8([]byte{0xEF, 0xBB, 0xBF, 'a'}) {
		t.Error("UTF-8 BOM is UTF-8")
	}
}

// TestSameICCProfile pins both directions, because they come from different
// places and want opposite errors.
//
// The answer feeds a rule that reports a violation when it is *yes* — an
// ICCBased colour space must not embed the same profile as the output intent —
// so a wrong "same" turns a conforming file into a reported one.
func TestSameICCProfile(t *testing.T) {
	// A profile of the given length whose ID field carries id and whose last
	// byte carries mark, so two profiles can differ in the ID, in the content,
	// or in neither.
	mk := func(id, mark byte) *object.Stream {
		data := make([]byte, 128)
		data[16] = 'C' // colour space marker area (irrelevant here)
		for i := 84; i < 100; i++ {
			data[i] = id
		}
		data[127] = mark
		s := &object.Stream{Dict: object.Dictionary{}, Data: data}
		s.Dict.Set("Length", object.Integer(len(data)))
		return s
	}
	doc := mkView(map[int]*object.IndirectObject{}, nil)

	if !sameICCProfile(doc, mk(1, 1), mk(1, 1)) {
		t.Error("two identical profiles must be the same")
	}

	// The corpus's ruling, not a reading: PDF_A-4 6-2-4-2-t03-pass-d embeds two
	// 557,188-byte profiles differing in one byte of their IDs and in nothing
	// else, and it is a *pass* file. Comparing content with the ID zeroed
	// reports it as violating the rule it was written to pass.
	if sameICCProfile(doc, mk(1, 1), mk(2, 1)) {
		t.Error("profiles differing only in their Profile ID must differ")
	}

	// And the half the ID cannot be trusted for. It is sixteen bytes in a
	// stream the document supplies — a claim the file makes about itself — so
	// an equal ID is not proof that the colours are the same.
	if sameICCProfile(doc, mk(1, 1), mk(1, 2)) {
		t.Error("two profiles with different content were called the same because " +
			"they claimed the same Profile ID")
	}

	// A zero ID is common and says nothing, so the content decides.
	if !sameICCProfile(doc, mk(0, 1), mk(0, 1)) {
		t.Error("zero-ID identical content must be the same")
	}
	if sameICCProfile(doc, mk(0, 1), mk(0, 2)) {
		t.Error("zero-ID different content must differ")
	}

	if sameICCProfile(doc, nil, mk(1, 1)) || sameICCProfile(doc, mk(1, 1), nil) ||
		sameICCProfile(doc, nil, nil) {
		t.Error("a nil profile compared equal to something")
	}
}

func TestColorantUTF8Nested(t *testing.T) {
	// DeviceN colorant with invalid UTF-8, nested in Resources/ColorSpace.
	deviceN := object.Array{object.Name("DeviceN"), object.Array{object.Name("Cyan\xc2")}, object.Name("DeviceCMYK"), object.IndirectRef{Number: 9}}
	csDict := &object.Dictionary{}
	csDict.Set("CS0", deviceN)
	res := &object.Dictionary{}
	res.Set("ColorSpace", csDict)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Resources", res)
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: page},
	}})
	referenced(doc, 1)
	if !hasRuleMsg(checkNameUTF8(doc, PDFA2b), "6.1.8") {
		t.Error("nested invalid-UTF8 colorant must be flagged")
	}
}

func TestAnnotFieldType(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, nil)
	parent := &object.Dictionary{}
	parent.Set("FT", object.Name("Btn"))
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: parent}
	widget := &object.Dictionary{}
	widget.Set("Subtype", object.Name("Widget"))
	widget.Set("Parent", object.IndirectRef{Number: 5})
	if got := annotFieldType(doc, widget); got != "Btn" {
		t.Errorf("inherited FT: got %q", got)
	}
}
