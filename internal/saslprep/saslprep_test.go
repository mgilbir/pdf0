package saslprep

import (
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// TestRFC4013Examples is the table of RFC 4013 §3, verbatim.
func TestRFC4013Examples(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"I\u00ADX", "IX", true}, // SOFT HYPHEN mapped to nothing
		{"user", "user", true},   // no transformation
		{"USER", "USER", true},   // case preserved
		{"\u00AA", "a", true},    // output is NFKC
		{"\u2168", "IX", true},   // output is NFKC, will match #1
		{"\u0007", "", false},    // prohibited character
		{"\u06271", "", false},   // bidirectional check
	} {
		for _, stored := range []bool{false, true} {
			got, err := Prepare(c.in, stored)
			if c.ok && (err != nil || got != c.want) {
				t.Errorf("Prepare(%+q, %v) = %+q, %v; want %+q", c.in, stored, got, err, c.want)
			}
			if !c.ok && !errors.Is(err, ErrProhibited) {
				t.Errorf("Prepare(%+q, %v) = %+q, %v; want ErrProhibited", c.in, stored, got, err)
			}
		}
	}
}

// TestMappingAndBidi pins the rules the RFC examples do not reach.
func TestMappingAndBidi(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"a\u00A0b", "a b", true},              // C.1.2 non-ASCII space → SPACE
		{"a\u3000b", "a b", true},              // IDEOGRAPHIC SPACE → SPACE
		{"a\u200Bb", "ab", true},               // ZWSP is B.1 and C.1.2: removed, not spaced
		{"a\uFE0Fb", "ab", true},               // variation selector: B.1
		{"\uFB01", "fi", true},                 // compatibility ligature
		{"", "", true},                         // the empty password stays empty
		{"\u05D0\u05D1", "\u05D0\u05D1", true}, // all RandALCat
		{"\u05D0a\u05D1", "", false},           // RandALCat with LCat
		{"\u05D01", "", false},                 // RandALCat not last
		{"1\u05D0", "", false},                 // RandALCat not first
		{"a\u200Eb", "", false},                // LEFT-TO-RIGHT MARK: C.8
		{"a\uE000", "", false},                 // private use: C.3
		{"a\uFFFD", "", false},                 // REPLACEMENT CHARACTER: C.6
		{"a\U000E0041", "", false},             // TAG LATIN CAPITAL LETTER A: C.9
		{"a\u2FF0", "", false},                 // IDEOGRAPHIC DESCRIPTION: C.7
		{"a\u0085", "", false},                 // NEXT LINE: C.2.2
		{"a\x7f", "", false},                   // DELETE: C.2.1
		{"\xff", "", false},                    // not UTF-8
	} {
		got, err := Prepare(c.in, false)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("Prepare(%+q) = %+q, %v; want %+q", c.in, got, err, c.want)
		}
		if !c.ok && !errors.Is(err, ErrProhibited) {
			t.Errorf("Prepare(%+q) = %+q, %v; want ErrProhibited", c.in, got, err)
		}
	}
}

// TestUnassignedStoredVsQuery: a code point unassigned in Unicode 3.2 (here
// U+0221, assigned in Unicode 4.0) is refused in a stored string and passed
// through in a query (RFC 3454 §7).
func TestUnassignedStoredVsQuery(t *testing.T) {
	if _, err := Prepare("x\u0221", true); !errors.Is(err, ErrProhibited) {
		t.Errorf("stored: err = %v, want ErrProhibited", err)
	}
	if got, err := Prepare("x\u0221", false); err != nil || got != "x\u0221" {
		t.Errorf("query: got %+q, %v", got, err)
	}
}

// python runs a script under python3, skipping when it (or its frozen Unicode
// 3.2 database, which the stdlib stringprep module is built on) is absent.
func python(t *testing.T, script string, stdin string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available: the independent stringprep oracle cannot run")
	}
	cmd := exec.Command(py, "-c", script)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python3: %v", err)
	}
	return string(out)
}

// TestTablesMatchPythonStringprep compares every generated table against
// Python's stdlib stringprep module — an independent transcription of RFC 3454
// over the same Unicode 3.2 database — at every code point.
func TestTablesMatchPythonStringprep(t *testing.T) {
	if testing.Short() {
		t.Skip("enumerates all 1.1M code points in python")
	}
	const script = `
import stringprep
for n in ["a1","b1","c12","c21","c22","c3","c4","c5","c6","c7","c8","c9","d1","d2"]:
    f = getattr(stringprep, "in_table_" + n)
    rs, lo, prev = [], None, None
    for cp in range(0x110000):
        if f(chr(cp)):
            if lo is None: lo = cp
            prev = cp
        elif lo is not None:
            rs.append((lo, prev)); lo = None
    if lo is not None: rs.append((lo, prev))
    print(n, " ".join("%X-%X" % r for r in rs))
`
	ours := map[string]table{
		"a1": unassignedA1, "b1": mappedToNothingB1, "c12": nonASCIISpaceC12,
		"c21": asciiControlC21, "c22": nonASCIIControlC22, "c3": privateUseC3,
		"c4": nonCharacterC4, "c5": surrogateC5, "c6": notPlainTextC6,
		"c7": notCanonicalC7, "c8": displayPropertyC8, "c9": taggingC9,
		"d1": randALCatD1, "d2": lCatD2,
	}
	lines := strings.Split(strings.TrimSpace(python(t, script, "")), "\n")
	if len(lines) != len(ours) {
		t.Fatalf("python printed %d tables, want %d", len(lines), len(ours))
	}
	for _, line := range lines {
		name, spec, _ := strings.Cut(line, " ")
		want := parseRanges(t, spec)
		got := ours[name]
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("table %s: generated %d ranges, python %d; first difference at %s", name, len(got), len(want), firstDiff(got, want))
		}
	}
}

func parseRanges(t *testing.T, spec string) table {
	var out table
	for _, f := range strings.Fields(spec) {
		a, b, _ := strings.Cut(f, "-")
		lo, err1 := strconv.ParseInt(a, 16, 32)
		hi, err2 := strconv.ParseInt(b, 16, 32)
		if err1 != nil || err2 != nil {
			t.Fatalf("bad range %q", f)
		}
		out = append(out, [2]rune{rune(lo), rune(hi)})
	}
	return out
}

func firstDiff(a, b table) string {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return fmt.Sprintf("index %d: %X vs %X", i, a[i], b[i])
		}
	}
	return "the shorter table's end"
}

// nfkcCorrigenda are the code points assigned in Unicode 3.2 whose NFKC form
// in the current Unicode version differs from Unicode 3.2's — the changes of
// Unicode Corrigendum #4 (five CJK compatibility ideographs whose 3.2
// decompositions were wrong). SASLprep pins Unicode 3.2, so for these five a
// current-Unicode implementation and a 3.2 one disagree; pdf0 follows the
// corrected mapping, as does every maintained normalizer. Measured, not
// assumed: TestNFKCMatchesUnicode32 recomputes the set.
var nfkcCorrigenda = map[rune]bool{0x2F868: true, 0x2F874: true, 0x2F91F: true, 0x2F95F: true, 0x2F9BF: true}

// TestNFKCMatchesUnicode32 compares x/text's NFKC with Python's frozen Unicode
// 3.2 NFKC for every code point assigned in Unicode 3.2, and pins the
// differences to nfkcCorrigenda.
func TestNFKCMatchesUnicode32(t *testing.T) {
	if testing.Short() {
		t.Skip("enumerates all 1.1M code points in python")
	}
	const script = `
import stringprep, unicodedata
u = unicodedata.ucd_3_2_0
for cp in range(0x110000):
    c = chr(cp)
    if 0xD800 <= cp <= 0xDFFF or stringprep.in_table_a1(c): continue
    n = u.normalize("NFKC", c)
    if n != c: print("%X %s" % (cp, " ".join("%X" % ord(x) for x in n)))
`
	py := map[rune]string{}
	for _, line := range strings.Split(strings.TrimSpace(python(t, script, "")), "\n") {
		f := strings.Fields(line)
		cp, _ := strconv.ParseInt(f[0], 16, 32)
		var sb strings.Builder
		for _, h := range f[1:] {
			r, _ := strconv.ParseInt(h, 16, 32)
			sb.WriteRune(rune(r))
		}
		py[rune(cp)] = sb.String()
	}
	if len(py) < 4000 {
		t.Fatalf("python reported only %d changing code points; the oracle is not working", len(py))
	}
	diff := map[rune]bool{}
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF || unassignedA1.contains(cp) {
			continue
		}
		want, ok := py[cp]
		if !ok {
			want = string(cp)
		}
		if got := norm.NFKC.String(string(cp)); got != want {
			diff[cp] = true
		}
	}
	if fmt.Sprint(diff) != fmt.Sprint(nfkcCorrigenda) {
		t.Errorf("NFKC differs from Unicode 3.2 at %v; pinned %v", keys(diff), keys(nfkcCorrigenda))
	}
}

func keys(m map[rune]bool) []string {
	var out []string
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if m[cp] {
			out = append(out, fmt.Sprintf("U+%04X", cp))
		}
	}
	return out
}

// pyPrepare is a reference SASLprep over Python's stringprep tables and Unicode
// 3.2 NFKC, written from RFC 4013 independently of Prepare. One input per line
// (hex UTF-8, so control characters survive); prints the hex output or "ERR".
const pyPrepare = `
import stringprep, unicodedata, sys
S = stringprep
PROHIB = [S.in_table_c12, S.in_table_c21, S.in_table_c22, S.in_table_c3, S.in_table_c4,
          S.in_table_c5, S.in_table_c6, S.in_table_c7, S.in_table_c8, S.in_table_c9]
def prep(s, stored):
    s = "".join(" " if S.in_table_c12(c) else c for c in s if not S.in_table_b1(c))
    s = unicodedata.ucd_3_2_0.normalize("NFKC", s)
    for c in s:
        if any(f(c) for f in PROHIB): return None
        if stored and S.in_table_a1(c): return None
    if any(S.in_table_d1(c) for c in s):
        if any(S.in_table_d2(c) for c in s): return None
        if not (S.in_table_d1(s[0]) and S.in_table_d1(s[-1])): return None
    return s
for line in sys.stdin:
    stored, h = line.split()
    s = bytes.fromhex(h).decode("utf-8", "surrogatepass")
    r = prep(s, stored == "1")
    print("ERR" if r is None else (r.encode("utf-8").hex() or "-"))
`

// TestPrepareMatchesPythonReference runs Prepare and an independent Python
// SASLprep over the same inputs — every interesting single code point in both
// modes, plus random strings drawn from the tables' edges — and requires the
// same output or the same refusal.
func TestPrepareMatchesPythonReference(t *testing.T) {
	var inputs []string
	add := func(s string) { inputs = append(inputs, s) }
	// Every code point in, and just outside, every table, alone and embedded.
	for _, tb := range []table{unassignedA1, mappedToNothingB1, nonASCIISpaceC12, asciiControlC21,
		nonASCIIControlC22, privateUseC3, nonCharacterC4, notPlainTextC6, notCanonicalC7,
		displayPropertyC8, taggingC9, randALCatD1, lCatD2} {
		for _, r := range tb {
			for _, cp := range []rune{r[0] - 1, r[0], (r[0] + r[1]) / 2, r[1], r[1] + 1} {
				if cp < 0 || cp > 0x10FFFF || cp >= 0xD800 && cp <= 0xDFFF {
					continue
				}
				add(string(cp))
				add("a" + string(cp) + "b")
				add("\u05D0" + string(cp) + "\u05D1")
			}
		}
	}
	rng := rand.New(rand.NewSource(1))
	pool := []rune{'a', 'Z', '1', ' ', 0xA0, 0xAD, 0x200B, 0x05D0, 0x0627, 0x0660, 0x00E9, 0x0301, 0x2168, 0xFB01, 0x3000, 0xFF21, 0x1100, 0x1161, 0x0221, 0xE000, 0x200E}
	for range 3000 {
		var sb strings.Builder
		for range rng.Intn(6) + 1 {
			sb.WriteRune(pool[rng.Intn(len(pool))])
		}
		add(sb.String())
	}

	var stdin strings.Builder
	type job struct {
		in     string
		stored bool
	}
	var jobs []job
	for _, in := range inputs {
		for _, stored := range []bool{false, true} {
			jobs = append(jobs, job{in, stored})
			flag := "0"
			if stored {
				flag = "1"
			}
			fmt.Fprintf(&stdin, "%s %x\n", flag, in)
		}
	}
	out := strings.Split(strings.TrimSpace(python(t, pyPrepare, stdin.String())), "\n")
	if len(out) != len(jobs) {
		t.Fatalf("python answered %d of %d inputs", len(out), len(jobs))
	}
	mismatches := 0
	for i, j := range jobs {
		got, err := Prepare(j.in, j.stored)
		want := out[i]
		ok := want != "ERR"
		if ok {
			if want == "-" {
				want = ""
			}
			var b []byte
			fmt.Sscanf(want, "%x", &b)
			want = string(b)
		}
		if !utf8.ValidString(j.in) {
			continue
		}
		if ok != (err == nil) || ok && got != want {
			// The five Corrigendum #4 characters normalize differently by design.
			if containsAny(j.in, nfkcCorrigenda) {
				continue
			}
			mismatches++
			if mismatches <= 10 {
				t.Errorf("Prepare(%+q, stored=%v) = %+q, %v; python says %+q (ok=%v)", j.in, j.stored, got, err, want, ok)
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d of %d inputs disagree with the Python reference", mismatches, len(jobs))
	}
	t.Logf("%d inputs agree with the Python reference", len(jobs))
}

func containsAny(s string, set map[rune]bool) bool {
	for _, r := range s {
		if set[r] {
			return true
		}
	}
	return false
}
