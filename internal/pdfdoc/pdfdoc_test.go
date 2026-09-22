package pdfdoc

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	for _, c := range []struct {
		s    string
		want []byte
	}{
		{"abc", []byte("abc")},
		{"caf\u00E9", []byte{'c', 'a', 'f', 0xE9}},
		{"\u20AC5", []byte{0xA0, '5'}},                   // euro sign at 0xA0
		{"\u2022\u2014\uFB01", []byte{0x80, 0x84, 0x93}}, // bullet, em dash, fi
		{"\u02D8\u02DC", []byte{0x18, 0x1F}},             // breve, small tilde
		{"a\tb\n", []byte("a\tb\n")},
	} {
		got, ok := Encode(c.s)
		if !ok || !bytes.Equal(got, c.want) {
			t.Errorf("Encode(%+q) = % X, %v; want % X", c.s, got, ok, c.want)
		}
		if back := Decode(c.want); back != c.s {
			t.Errorf("Decode(% X) = %+q, want %+q", c.want, back, c.s)
		}
	}
	// Not representable, or an undefined code.
	for _, s := range []string{"\u0100", "\u4E2D", "\u00AD", "\x7F", "\x01", "\u0080", "\u009F"} {
		if b, ok := Encode(s); ok {
			t.Errorf("Encode(%+q) = % X, want not representable", s, b)
		}
	}
	if got := Decode([]byte{0x7F, 0x9F, 0xAD, 0x01}); got != "\uFFFD\uFFFD\uFFFD\uFFFD" {
		t.Errorf("undefined codes decoded to %+q", got)
	}
}

// TestTableMatchesSpec compares every defined code with Table D.3 of the
// ISO 32000-2 text, when the spec and pdftotext are available locally.
func TestTableMatchesSpec(t *testing.T) {
	spec, _ := filepath.Abs("../../spec/pdf2.0/ISO_32000-2_sponsored-ec2.pdf")
	if _, err := os.Stat(spec); err != nil {
		t.Skip("ISO 32000-2 not present under spec/")
	}
	tool, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext not available")
	}
	out, err := exec.Command(tool, "-layout", "-f", "870", "-l", "895", spec, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(out, []byte("Table D.3"))
	if i < 0 {
		t.Fatal("Table D.3 not found in the extracted pages")
	}
	row := regexp.MustCompile(`0x([0-9a-f]{2})\s+[0-7]{4}\s+(?:U\+([0-9A-F]{4}))?`)
	seen := 0
	for _, m := range row.FindAllSubmatch(out[i:], 256) {
		code, _ := strconv.ParseUint(string(m[1]), 16, 8)
		seen++
		if code == 0x16 {
			continue // the table's typo; see high
		}
		if len(m[2]) == 0 {
			if decode[code] != -1 {
				t.Errorf("0x%02X: undefined in Table D.3, decoded to U+%04X", code, decode[code])
			}
			continue
		}
		want, _ := strconv.ParseUint(string(m[2]), 16, 32)
		if decode[code] >= 0 && decode[code] != rune(want) {
			t.Errorf("0x%02X: decoded to U+%04X, Table D.3 says U+%04X", code, decode[code], want)
		}
	}
	if seen != 256 {
		t.Errorf("read %d rows of Table D.3, want 256", seen)
	}
}
