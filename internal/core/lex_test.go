package core

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// lexAll renders every token of data as "kind:value", with dictionaries
// skipped whole when skipDicts is set.
func lexAll(data string, skipDicts bool) []string {
	var out []string
	lx := NewContentLexer(Canceler{}, []byte(data))
	var t ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case ContentOperator:
			out = append(out, "op:"+string(t.Raw))
		case ContentNumber:
			out = append(out, "num:"+string(t.Raw))
		case ContentName:
			out = append(out, "name:"+t.Name())
		case ContentString, ContentHexString:
			out = append(out, fmt.Sprintf("str:%q", t.Bytes()))
		case ContentArrayStart, ContentArrayEnd:
			out = append(out, string(t.Raw))
		case ContentDictStart:
			if skipDicts {
				out = append(out, "dict:"+string(lx.SkipDict(&t)))
			} else {
				out = append(out, "<<")
			}
		case ContentDictEnd:
			out = append(out, ">>")
		case ContentInlineImage:
			out = append(out, fmt.Sprintf("image:%q:%q", bytes.TrimSpace(t.Params), t.Data))
		}
	}
	return out
}

func TestContentLexerTokens(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		skip     bool
		want     string
	}{
		{"operators and operands", "q 1 0 0 1 .5 -2 cm /F1 12 Tf", false,
			"op:q num:1 num:0 num:0 num:1 num:.5 num:-2 op:cm name:F1 num:12 op:Tf"},
		{"arrays and strings", "[(a) -20 <41>] TJ", false,
			`[ str:"a" num:-20 str:"A" ] op:TJ`},
		{"comment", "q % Q in a comment\nQ", false, "op:q op:Q"},
		{"name escape", "/F#31 Tf", false, "name:F1 op:Tf"},
		{"true false null are keywords", "true false null", false, "op:true op:false op:null"},
		{"a number run is one token", "12abc Td", false, "num:12abc op:Td"},
		{"stray delimiters start nothing", ") } { > q", false, "op:q"},
		{"dictionary as tokens", "/P <</MCID 3>> BDC", false, "name:P << name:MCID num:3 >> op:BDC"},
		{"dictionary skipped", "/P <</MCID 3 /A (x>>y)>> BDC", true, "name:P dict:<</MCID 3 /A (x>>y)>> op:BDC"},
		{"nested dictionary skipped", "/P <</A <</B 1>> /C 2>> BDC", true, "name:P dict:<</A <</B 1>> /C 2>> op:BDC"},
		{"inline image", "q BI /W 2 /H 1 /CS /G ID \x01\x02 EI Q", false, `op:q image:"/W 2 /H 1 /CS /G":"\x01\x02" op:Q`},
		{"inline image with EI inside data, /L honoured", "BI /W 4 /H 1 /L 4 ID EI k EI Q", false, `image:"/W 4 /H 1 /L 4":"EI k" op:Q`},
		{"literal string escapes", `(a\nb\051\\) Tj`, false, `str:"a\nb)\\" op:Tj`},
		{"string line continuation", "(ab\\\ncd) Tj", false, `str:"abcd" op:Tj`},
		{"string end of line is LF", "(a\r\nb\rc) Tj", false, `str:"a\nb\nc" op:Tj`},
		{"hex string white space and odd length", "<41 4 2 4> Tj", false, `str:"AB@" op:Tj`},
		{"hex string non-digit is skipped", "<41zz42> Tj", false, `str:"AB" op:Tj`},
	} {
		got := strings.Join(lexAll(tc.in, tc.skip), " ")
		if got != tc.want {
			t.Errorf("%s: %q\n got %s\nwant %s", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestContentLexerDropsBinaryRuns: a run of regular bytes too long to be a
// keyword is binary and is dropped whole; a number is not, whatever its length.
func TestContentLexerDropsBinaryRuns(t *testing.T) {
	binary := strings.Repeat("A", MaxContentTokenLen) + "k"
	if got := strings.Join(lexAll("q "+binary+" Q", false), " "); got != "op:q op:Q" {
		t.Errorf("a %d-byte run: got %s, want op:q op:Q", len(binary), got)
	}
	long := strings.Repeat("9", 400)
	if got := lexAll(long+" w", false); len(got) != 2 || got[0] != "num:"+long {
		t.Errorf("a 400-digit number was not read as one number: %v", got)
	}
}

// TestInlineImageParams reads the parameter entries the rules ask about.
func TestInlineImageParams(t *testing.T) {
	got := ParseInlineImageParams([]byte(" /W 1 /F [/AHx /LZW] /DP << /Predictor 2 >> /I true /Intent /Perceptual "))
	var parts []string
	for _, p := range got {
		var vs []string
		for _, v := range p.Value {
			vs = append(vs, string(v.Raw))
		}
		parts = append(parts, fmt.Sprintf("%s=%v%v", p.Key, p.Array, vs))
	}
	want := "W=false[1] F=true[AHx LZW] DP=false[<< /Predictor 2 >>] I=false[true] Intent=false[Perceptual]"
	if s := strings.Join(parts, " "); s != want {
		t.Errorf("got  %s\nwant %s", s, want)
	}
}

// TestContentLexerHostile: shapes that have stalled, looped or recursed in a
// content scanner before. Each must finish in time proportional to its length.
func TestContentLexerHostile(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: time.Minute, MaxStack: 1 << 20}, func(t *testing.T) {
		const n = 1 << 20
		for name, in := range map[string][]byte{
			"stray parens":         bytes.Repeat([]byte(")"), n),
			"unterminated string":  append([]byte("("), bytes.Repeat([]byte("(a"), n)...),
			"unterminated hex":     append([]byte("<"), bytes.Repeat([]byte("4"), n)...),
			"nested dictionaries":  bytes.Repeat([]byte("<<"), n),
			"BI in the parameters": append([]byte("BI "), bytes.Repeat([]byte("BI "), n)...),
			"many inline images":   bytes.Repeat([]byte("BI /W 1 ID x EI "), n/16),
			"unterminated comment": append([]byte("%"), bytes.Repeat([]byte("q"), n)...),
		} {
			start := time.Now()
			count := 0
			lx := NewContentLexer(Canceler{}, in)
			var tk ContentTok
			for lx.Next(&tk) {
				if tk.Kind == ContentDictStart {
					lx.SkipDict(&tk)
				}
				count++
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("%s: %v for %d bytes", name, d, len(in))
			}
			_ = count
		}
	})
}

// TestConsumersSeeTheSameStrings is C150's scenario: the PDF/UA content pass
// and text extraction (TokenizeContent) and the font-usage walk the PDF/A
// glyph rules read (then buildFontEvents) used to decode the same string operand
// differently, so the two validators judged different glyph codes. Each input
// here decoded differently under at least one pair of the old tokenizers.
func TestConsumersSeeTheSameStrings(t *testing.T) {
	for _, in := range []string{
		"(\x00\x41\\\n\x00\x42) Tj", // line continuation inside an Identity-H string
		"(\x00\x41\r\n\x00\x42) Tj", // an unescaped CR LF
		"<0041 zz 0042> Tj",         // a non-hex byte in a hex string
		"(\\101\\0) Tj",
	} {
		var viaTokenize [][]byte
		for tk := range TokenizeContent(Canceler{}, []byte(in)) {
			if tk.Kind == KindString {
				viaTokenize = append(viaTokenize, tk.Str)
			}
		}
		d := newTestDoc("BT /F1 12 Tf "+in+" ET", nil)
		f1 := d.add(object.NewDictionary(ent("Type", object.Name("Font")), ent("Subtype", object.Name("Type1")), ent("BaseFont", object.Name("Helvetica"))))
		d.page.Set("Resources", res(sub("Font", ent("F1", f1))))
		var viaFonts [][]byte
		if u := CollectFontTextUsage(d.v)[d.v.ResolveDict(f1)]; u != nil {
			viaFonts = u.Strings
		}
		if fmt.Sprintf("%q", viaTokenize) != fmt.Sprintf("%q", viaFonts) {
			t.Errorf("%q: TokenizeContent saw %q, the font-usage walk %q", in, viaTokenize, viaFonts)
		}
	}
}
