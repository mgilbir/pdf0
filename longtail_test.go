package pdf0

import (
	"strings"
	"testing"
)

func TestContentStreamNumberLimits(t *testing.T) {
	lim1b := implLimits{rule: "6.1.12", stringLen: 65535, realLimit: 32767}
	lim2b := implLimits{rule: "6.1.13", stringLen: 32767, realLimit: 3.403e38}
	check := func(lim implLimits, tok string) bool {
		got := false
		checkContentNumberLimit(tok, lim, 0, func(string, int) { got = true })
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
}

func TestMaxCMapCID(t *testing.T) {
	cmap := "begincidrange\n<0000> <00ff> 0\n<2100> <21ff> 65400\nendcidrange"
	if got := maxCMapCID([]byte(cmap)); got != 65400+0xff {
		t.Errorf("range CID max: got %d", got)
	}
	cmap2 := "begincidchar\n<0041> 70000\nendcidchar"
	if got := maxCMapCID([]byte(cmap2)); got != 70000 {
		t.Errorf("char CID max: got %d", got)
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

func TestSameICCProfile(t *testing.T) {
	mk := func(id byte) *Stream {
		data := make([]byte, 128)
		data[16] = 'C' // colour space marker area (irrelevant here)
		for i := 84; i < 100; i++ {
			data[i] = id
		}
		s := &Stream{Dict: Dictionary{}, Data: data}
		s.Dict.Set("Length", Integer(len(data)))
		return s
	}
	doc := &Document{Objects: map[int]*IndirectObject{}}
	a, b := mk(1), mk(1)
	if !sameICCProfile(doc, a, b) {
		t.Error("equal non-zero Profile IDs must be the same")
	}
	c := mk(2)
	if sameICCProfile(doc, a, c) {
		t.Error("different non-zero Profile IDs must differ")
	}
	// One zero ID: fall back to content comparison (both zeroed -> equal).
	z1, z2 := mk(0), mk(0)
	if !sameICCProfile(doc, z1, z2) {
		t.Error("zero-ID identical content must be the same")
	}
}

func TestColorantUTF8Nested(t *testing.T) {
	// DeviceN colorant with invalid UTF-8, nested in Resources/ColorSpace.
	deviceN := Array{Name("DeviceN"), Array{Name("Cyan\xc2")}, Name("DeviceCMYK"), IndirectRef{Number: 9}}
	csDict := &Dictionary{}
	csDict.Set("CS0", deviceN)
	res := &Dictionary{}
	res.Set("ColorSpace", csDict)
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Resources", res)
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: page},
	}}
	if !hasRuleMsg(checkNameUTF8(doc, PDFA2b), "6.1.8") {
		t.Error("nested invalid-UTF8 colorant must be flagged")
	}
}

func TestAnnotFieldType(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	parent := &Dictionary{}
	parent.Set("FT", Name("Btn"))
	doc.Objects[5] = &IndirectObject{Number: 5, Value: parent}
	widget := &Dictionary{}
	widget.Set("Subtype", Name("Widget"))
	widget.Set("Parent", IndirectRef{Number: 5})
	if got := annotFieldType(doc, widget); got != "Btn" {
		t.Errorf("inherited FT: got %q", got)
	}
}
