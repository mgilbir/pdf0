package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// fontView is a View holding one Type 0 font dictionary with the given
// /Encoding, and the object for a stream encoding at number 2.
func fontView(enc object.Object, cmapStream *object.Stream) (View, *object.Dictionary) {
	f := &object.Dictionary{}
	f.Set("Subtype", object.Name("Type0"))
	f.Set("Encoding", enc)
	tr := object.Dictionary{}
	v := View{
		Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: f}},
		Trailer: &tr,
		Limits:  DefaultLimits(),
	}
	if cmapStream != nil {
		v.Objects[2] = &object.IndirectObject{Number: 2, Value: cmapStream}
	}
	return v, f
}

func codeLengths(codes []Code) []int {
	var out []int
	for _, c := range codes {
		out = append(out, c.Bytes)
	}
	return out
}

// Every predefined CMap cuts codes by its own codespace, which is what a
// two-byte cut got wrong for all but Identity and the Uni*-UCS2 family.
func TestFontCodesCutByThePredefinedCodespace(t *testing.T) {
	for _, c := range []struct {
		enc  string
		s    []byte
		want []int
	}{
		// Shift-JIS: ASCII and half-width katakana are one byte, kanji two.
		{"90ms-RKSJ-H", []byte{'A', 0x88, 0x9F, 0xB1, 'B'}, []int{1, 2, 1, 1}},
		// EUC-JP: one-byte ASCII, two-byte JIS X 0208, and the 8E prefix.
		{"EUC-H", []byte{'A', 0xB0, 0xA1, 0x8E, 0xB1}, []int{1, 2, 2}},
		// GB 18030: the two- and four-byte ranges share a first byte.
		{"GBK2K-H", []byte{0x81, 0x40, 0x81, 0x30, 0x81, 0x30, 'A'}, []int{2, 4, 1}},
		// UTF-16: a surrogate pair is one four-byte code.
		{"UniJIS-UTF16-H", []byte{0x00, 'A', 0xD8, 0x3D, 0xDE, 0x00}, []int{2, 4}},
		{"Identity-H", []byte{0x00, 0x41, 0x00, 0x42}, []int{2, 2}},
	} {
		v, f := fontView(object.Name(c.enc), nil)
		fc, ok := LoadFontCodes(v, f)
		if !ok {
			t.Errorf("%s: no code cutter", c.enc)
			continue
		}
		if got := codeLengths(fc.Codes(c.s)); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: cut % X into %v, want %v", c.enc, c.s, got, c.want)
		}
	}
}

// The Uni* CMaps' codes are Unicode; the others' are not, and say nothing.
func TestFontCodesUnicode(t *testing.T) {
	v, f := fontView(object.Name("UniJIS-UTF16-H"), nil)
	fc, _ := LoadFontCodes(v, f)
	var got []rune
	for _, c := range fc.Codes([]byte{0x00, 'A', 0xD8, 0x3D, 0xDE, 0x00}) {
		rs, ok := fc.Unicode(c)
		if !ok {
			t.Fatalf("code %X has no Unicode", c.Value)
		}
		got = append(got, rs...)
	}
	if string(got) != "A😀" {
		t.Errorf("got %q, want %q", string(got), "A😀")
	}
	v, f = fontView(object.Name("90ms-RKSJ-H"), nil)
	fc, _ = LoadFontCodes(v, f)
	if _, ok := fc.Unicode(fc.Codes([]byte{'A'})[0]); ok {
		t.Error("a Shift-JIS code was read as Unicode")
	}
}

// An embedded CMap LoadCMap refuses still cuts codes: its own codespace, plus
// that of the predefined CMap it defers to.
func TestFontCodesFromARefusedEmbeddedCMap(t *testing.T) {
	src := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"/90ms-RKSJ-H usecmap\n" +
		"1 begincodespacerange <F040> <F9FC> endcodespacerange\n" +
		"1 begincidrange <F040> <F9FC> 8000 endcidrange\nendcmap\n"
	st := object.NewStream(&object.Dictionary{}, []byte(src))
	v, f := fontView(object.IndirectRef{Number: 2}, st)
	if _, ok := LoadCMap(v, f); ok {
		t.Fatal("the fixture must be a CMap LoadCMap refuses")
	}
	fc, ok := LoadFontCodes(v, f)
	if !ok {
		t.Fatal("no code cutter")
	}
	if got := codeLengths(fc.Codes([]byte{'A', 0xF0, 0x40, 0x88, 0x9F})); fmt.Sprint(got) != "[1 2 2]" {
		t.Errorf("cut into %v, want [1 2 2]", got)
	}
}

// Every name the module treats as predefined has a codespace here.
func TestEveryPredefinedCMapHasACodespace(t *testing.T) {
	for name := range PredefinedCMaps {
		if len(predefinedCodespaces[name]) == 0 {
			t.Errorf("%s has no codespace", name)
		}
	}
	if len(predefinedCodespaces) != len(PredefinedCMaps) {
		t.Errorf("%d codespaces for %d predefined CMaps", len(predefinedCodespaces), len(PredefinedCMaps))
	}
}

// TestPredefinedCodespacesMatchAdobe checks the table against Adobe's CMap
// files where they are installed (poppler-data puts them in
// /usr/share/poppler/cMap). It is an oracle, not a fixture: the table is
// committed, and this is how it was checked.
func TestPredefinedCodespacesMatchAdobe(t *testing.T) {
	const root = "/usr/share/poppler/cMap"
	if _, err := os.Stat(root); err != nil {
		t.Skipf("Adobe CMaps not installed at %s", root)
	}
	files := map[string]string{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files[d.Name()] = p
		}
		return nil
	})
	block := regexp.MustCompile(`(?s)begincodespacerange(.*?)endcodespacerange`)
	hexTok := regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
	usecmap := regexp.MustCompile(`/(\S+)\s+usecmap`)
	var read func(name string, depth int) []codespaceRange
	read = func(name string, depth int) []codespaceRange {
		data, err := os.ReadFile(files[name])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out []codespaceRange
		for _, b := range block.FindAllStringSubmatch(string(data), -1) {
			toks := hexTok.FindAllStringSubmatch(b[1], -1)
			for i := 0; i+1 < len(toks); i += 2 {
				lo, n := hexCode("<" + toks[i][1] + ">")
				hi, _ := hexCode("<" + toks[i+1][1] + ">")
				out = append(out, codespaceRange{bytes: n, lo: lo, hi: hi})
			}
		}
		if len(out) == 0 && depth < 4 {
			if m := usecmap.FindStringSubmatch(string(data)); m != nil {
				return read(m[1], depth+1)
			}
		}
		return out
	}
	for name, want := range predefinedCodespaces {
		if _, ok := files[name]; !ok {
			t.Errorf("%s: not among the installed CMaps", name)
			continue
		}
		if got := read(name, 0); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: Adobe's codespace is %v, the table has %v", name, got, want)
		}
	}
}
