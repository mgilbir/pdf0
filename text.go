package pdf0

import (
	"strconv"
	"strings"
)

// ExtractText returns the visible text of every page in reading order, pages
// separated by a form feed. Text is decoded through each font's ToUnicode CMap;
// glyphs without a ToUnicode mapping are dropped. Layout is approximate: line
// breaks follow the text-positioning operators and wide inter-glyph gaps become
// spaces.
func (d *Document) ExtractText() string {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return ""
	}
	var b strings.Builder
	for i, pg := range collectPages(d, catalog.Get("Pages")) {
		if i > 0 {
			b.WriteByte('\f')
		}
		b.WriteString(d.ExtractPageText(pg.dict))
	}
	return b.String()
}

// ExtractPageText returns the visible text of a single page dictionary.
func (d *Document) ExtractPageText(page *Dictionary) string {
	content := getContentStreamData(d, page.Get("Contents"))
	if len(content) == 0 {
		return ""
	}
	fonts := d.pageFontMaps(page)

	var out strings.Builder
	var curMap map[int]rune
	curTwoByte := false
	toks := tokenizeContent(content)
	var operands []contentToken

	show := func(raw []byte) {
		for _, r := range decodeShown(raw, curMap, curTwoByte) {
			out.WriteRune(r)
		}
	}
	for _, tk := range toks {
		if tk.kind != ctOp {
			operands = append(operands, tk)
			continue
		}
		switch tk.op {
		case "Tf":
			if len(operands) >= 1 {
				if f, ok := fonts[operands[0].name]; ok {
					curMap, curTwoByte = f.toUnicode, f.twoByte
				} else {
					curMap, curTwoByte = nil, false
				}
			}
		case "Tj", "'", "\"":
			if tk.op != "Tj" {
				out.WriteByte('\n')
			}
			if len(operands) >= 1 {
				show(operands[len(operands)-1].str)
			}
		case "TJ":
			for _, el := range operands {
				switch el.kind {
				case ctString:
					show(el.str)
				case ctNumber:
					if el.num < -100 { // wide negative adjustment ≈ a space
						out.WriteByte(' ')
					}
				}
			}
		case "Td", "TD", "T*":
			out.WriteByte('\n')
		}
		operands = operands[:0]
	}
	return out.String()
}

type fontText struct {
	toUnicode map[int]rune
	twoByte   bool
}

// pageFontMaps resolves the page's /Font resources to their ToUnicode maps.
func (d *Document) pageFontMaps(page *Dictionary) map[string]fontText {
	out := map[string]fontText{}
	res := d.ResolveDict(page.Get("Resources"))
	if res == nil {
		return out
	}
	fontDict := d.ResolveDict(res.Get("Font"))
	if fontDict == nil {
		return out
	}
	for _, name := range fontDict.Keys {
		f := d.ResolveDict(fontDict.Get(name))
		if f == nil {
			continue
		}
		twoByte := false
		if st, _ := f.Get("Subtype").(Name); st == "Type0" {
			twoByte = true
		}
		out[string(name)] = fontText{toUnicode: parseToUnicodeMap(d, f), twoByte: twoByte}
	}
	return out
}

// decodeShown maps a shown byte string to runes. It prefers the font's
// ToUnicode CMap; for a simple (single-byte) font it falls back to the byte
// value as Latin-1 (a close approximation of WinAnsi for printable text), which
// recovers ASCII text from the standard fonts that carry no ToUnicode map.
func decodeShown(raw []byte, toUnicode map[int]rune, twoByte bool) []rune {
	var runes []rune
	step := 1
	if twoByte {
		step = 2
	}
	for i := 0; i+step <= len(raw); i += step {
		code := int(raw[i])
		if twoByte {
			code = int(raw[i])<<8 | int(raw[i+1])
		}
		if r, ok := toUnicode[code]; ok {
			runes = append(runes, r)
			continue
		}
		if !twoByte && code >= 32 && code < 256 {
			runes = append(runes, rune(code))
		}
	}
	return runes
}

// --- content-stream tokenizer ---

type ctKind int

const (
	ctOp ctKind = iota
	ctNumber
	ctString
	ctName
	ctArrayStart
	ctArrayEnd
)

type contentToken struct {
	kind ctKind
	op   string
	name string
	str  []byte
	num  float64
}

// tokenizeContent splits a content stream into operand/operator tokens. It is
// lenient: unrecognized bytes are skipped. Array and dictionary delimiters are
// surfaced so TJ arrays can be read; inline images (BI…ID…EI) are stepped over.
func tokenizeContent(data []byte) []contentToken {
	var toks []contentToken
	i := 0
	for i < len(data) {
		c := data[i]
		switch {
		case isContentSpace(c):
			i++
		case c == '%':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case c == '(':
			s, ni := scanContentLiteral(data, i)
			toks = append(toks, contentToken{kind: ctString, str: s})
			i = ni
		case c == '<' && i+1 < len(data) && data[i+1] == '<':
			i += 2 // dictionary start — skip; not needed for text
		case c == '>' && i+1 < len(data) && data[i+1] == '>':
			i += 2
		case c == '<':
			s, ni := scanContentHex(data, i)
			toks = append(toks, contentToken{kind: ctString, str: s})
			i = ni
		case c == '/':
			n, ni := scanContentName(data, i)
			toks = append(toks, contentToken{kind: ctName, name: n})
			i = ni
		case c == '[':
			toks = append(toks, contentToken{kind: ctArrayStart})
			i++
		case c == ']':
			toks = append(toks, contentToken{kind: ctArrayEnd})
			i++
		case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
			num, ni := scanContentNumber(data, i)
			toks = append(toks, contentToken{kind: ctNumber, num: num})
			i = ni
		default:
			word, ni := scanContentWord(data, i)
			i = ni
			if word == "" {
				i++
				continue
			}
			if word == "BI" {
				i = skipContentInlineImage(data, i)
				continue
			}
			toks = append(toks, contentToken{kind: ctOp, op: word})
		}
	}
	return toks
}

func isContentSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0
}

func isContentDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func scanContentLiteral(data []byte, i int) ([]byte, int) {
	i++ // '('
	var out []byte
	depth := 1
	for i < len(data) {
		c := data[i]
		switch c {
		case '\\':
			i++
			if i >= len(data) {
				return out, i
			}
			switch e := data[i]; e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			default:
				if e >= '0' && e <= '7' {
					v := 0
					for k := 0; k < 3 && i < len(data) && data[i] >= '0' && data[i] <= '7'; k++ {
						v = v*8 + int(data[i]-'0')
						i++
					}
					out = append(out, byte(v))
					continue
				}
				out = append(out, e)
			}
			i++
		case '(':
			depth++
			out = append(out, c)
			i++
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, c)
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

func scanContentHex(data []byte, i int) ([]byte, int) {
	i++ // '<'
	var digits []byte
	for i < len(data) && data[i] != '>' {
		if !isContentSpace(data[i]) {
			digits = append(digits, data[i])
		}
		i++
	}
	if i < len(data) {
		i++ // '>'
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for k := 0; k < len(out); k++ {
		out[k] = hexNibble(digits[2*k])<<4 | hexNibble(digits[2*k+1])
	}
	return out, i
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func scanContentName(data []byte, i int) (string, int) {
	i++ // '/'
	start := i
	for i < len(data) && !isContentSpace(data[i]) && !isContentDelimiter(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

func scanContentNumber(data []byte, i int) (float64, int) {
	start := i
	if data[i] == '-' || data[i] == '+' {
		i++
	}
	for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == '.') {
		i++
	}
	f, _ := strconv.ParseFloat(string(data[start:i]), 64)
	return f, i
}

func scanContentWord(data []byte, i int) (string, int) {
	start := i
	for i < len(data) && !isContentSpace(data[i]) && !isContentDelimiter(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

// skipInlineImage steps past a BI…ID…EI inline image.
func skipContentInlineImage(data []byte, i int) int {
	if idx := indexKeyword(data, i, "EI"); idx >= 0 {
		return idx + 2
	}
	return len(data)
}

func indexKeyword(data []byte, from int, kw string) int {
	for i := from; i+len(kw) <= len(data); i++ {
		if string(data[i:i+len(kw)]) == kw &&
			(i == 0 || isContentSpace(data[i-1])) &&
			(i+len(kw) == len(data) || isContentSpace(data[i+len(kw)])) {
			return i
		}
	}
	return -1
}
