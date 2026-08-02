package core

import (
	"encoding/asn1"
	"fmt"
	"github.com/mgilbir/pdf0/internal/font"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/pdf0/object"
)

// Small document queries the validators share: the object number a dictionary
// was reached through, whether a dictionary is an annotation, the header version,
// and an XMP packet decoded to UTF-8.

// dictObjNum finds the object number whose value is the given dictionary. During
// a validation run a reverse index is built once in the cache and reused, so the
// many per-font and per-cell lookups do not each scan the whole object table
// (which is quadratic on large documents — hundreds of thousands of objects).
func (d View) DictObjNum(target *object.Dictionary) int {
	// Two cross-reference slots may point at the same bytes, in which case Read
	// stores one parsed value under both object numbers (see parsedByOffset), so
	// a *object.Dictionary can be the value of more than one object. Both loops below
	// therefore answer with the LOWEST such number rather than with whichever
	// one the range happens to reach first: d.Objects is a Go map and Go
	// randomises its iteration order per run, so "first" would put a different
	// object number in a validator's report on each run over the same file.
	// Numeric order is a total order, so this answer is reproducible — which is
	// load-bearing, as reports are diffed run against run.
	if c := d.Run; c != nil {
		if c.dictNum == nil {
			c.dictNum = make(map[*object.Dictionary]int, len(d.Objects))
			for num, iobj := range d.Objects {
				if dp, ok := iobj.Value.(*object.Dictionary); ok {
					if prev, dup := c.dictNum[dp]; !dup || num < prev {
						c.dictNum[dp] = num
					}
				}
			}
		}
		if n, ok := c.dictNum[target]; ok {
			return n
		}
		return -1
	}
	best := -1
	for num, iobj := range d.Objects {
		if dp, ok := iobj.Value.(*object.Dictionary); ok && dp == target {
			if best < 0 || num < best {
				best = num
			}
		}
	}
	return best
}

// xmpText decodes an XMP packet to UTF-8 text. Every XMP consumer goes through
// it so no site can route the document's identification back through the
// content budget.
func (doc View) XMPText(stream *object.Stream) string {
	return DecodeXMPToUTF8(doc.MetadataContent(stream))
}

func DecodeXMPToUTF8(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// Check for BOM
	if len(data) >= 4 {
		// UTF-32 BE BOM: 00 00 FE FF
		if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
			return decodeUTF32(data[4:], true)
		}
		// UTF-32 LE BOM: FF FE 00 00
		if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
			return decodeUTF32(data[4:], false)
		}
	}
	if len(data) >= 2 {
		// UTF-16 BE BOM: FE FF
		if data[0] == 0xFE && data[1] == 0xFF {
			return decodeUTF16(data[2:], true)
		}
		// UTF-16 LE BOM: FF FE
		if data[0] == 0xFF && data[1] == 0xFE {
			return decodeUTF16(data[2:], false)
		}
	}

	// UTF-8 BOM: EF BB BF - just skip it
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return string(data[3:])
	}

	// Heuristic: detect encoding without BOM (check UTF-32 before UTF-16)
	if len(data) >= 4 {
		// UTF-32 BE: 00 00 00 xx
		if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] != 0x00 {
			return decodeUTF32(data, true)
		}
		// UTF-32 LE: xx 00 00 00
		if data[0] != 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x00 {
			return decodeUTF32(data, false)
		}
		// UTF-16 BE: 00 xx
		if data[0] == 0x00 && data[1] != 0x00 {
			return decodeUTF16(data, true)
		}
		// UTF-16 LE: xx 00
		if data[0] != 0x00 && data[1] == 0x00 {
			return decodeUTF16(data, false)
		}
	}

	return string(data)
}

func decodeUTF16(data []byte, bigEndian bool) string {
	if len(data) < 2 {
		return ""
	}
	var buf []byte
	for i := 0; i+1 < len(data); i += 2 {
		var codeUnit uint16
		if bigEndian {
			codeUnit = uint16(data[i])<<8 | uint16(data[i+1])
		} else {
			codeUnit = uint16(data[i+1])<<8 | uint16(data[i])
		}

		// Handle surrogate pairs
		if codeUnit >= 0xD800 && codeUnit <= 0xDBFF {
			if i+3 < len(data) {
				var low uint16
				if bigEndian {
					low = uint16(data[i+2])<<8 | uint16(data[i+3])
				} else {
					low = uint16(data[i+3])<<8 | uint16(data[i+2])
				}
				if low >= 0xDC00 && low <= 0xDFFF {
					r := rune(0x10000 + (rune(codeUnit-0xD800)<<10 | rune(low-0xDC00)))
					var tmp [4]byte
					n := utf8.EncodeRune(tmp[:], r)
					buf = append(buf, tmp[:n]...)
					i += 2
					continue
				}
			}
			buf = append(buf, 0xEF, 0xBF, 0xBD) // replacement char
			continue
		}

		var tmp [4]byte
		n := utf8.EncodeRune(tmp[:], rune(codeUnit))
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

func decodeUTF32(data []byte, bigEndian bool) string {
	if len(data) < 4 {
		return ""
	}
	var buf []byte
	for i := 0; i+3 < len(data); i += 4 {
		var codePoint uint32
		if bigEndian {
			codePoint = uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3])
		} else {
			codePoint = uint32(data[i+3])<<24 | uint32(data[i+2])<<16 | uint32(data[i+1])<<8 | uint32(data[i])
		}

		r := rune(codePoint)
		if !utf8.ValidRune(r) {
			r = 0xFFFD
		}
		var tmp [4]byte
		n := utf8.EncodeRune(tmp[:], r)
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

func IsAnnotation(dict *object.Dictionary) bool {
	if t, ok := dict.Get("Type").(object.Name); ok && t == "Annot" {
		return true
	}
	// Also detect annotations by Subtype + Rect (some PDFs omit /Type)
	if _, ok := dict.Get("Subtype").(object.Name); ok && dict.Get("Rect") != nil {
		return true
	}
	return false
}

// parsePDFVersion splits a "1.6"-style version string into major and minor.
func ParsePDFVersion(v string) (major, minor int, ok bool) {
	dot := strings.IndexByte(v, '.')
	if dot <= 0 || dot == len(v)-1 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(v, "%d.%d", &major, &minor); err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// CatalogPages returns the catalog's /Pages value, the root of the page tree,
// or nil when there is no catalog.
func (d View) CatalogPages() object.Object {
	if cat := d.Catalog(); cat != nil {
		return cat.Get("Pages")
	}
	return nil
}

// validBCP47 reports whether tag is a syntactically well-formed BCP 47 (RFC
// 5646) language tag. It validates the subtag structure rather than a registry:
// a non-empty primary language of 2–8 letters (or an x-/i- private/grandfathered
// singleton), followed by subtags of 1–8 alphanumerics each.
func ValidBCP47(tag string) bool {
	subs := strings.Split(tag, "-")
	first := subs[0]
	if len(first) == 1 {
		if first != "x" && first != "i" && first != "X" && first != "I" {
			return false // a lone singleton cannot be the primary language
		}
	} else if len(first) < 2 || len(first) > 8 || !allAlpha(first) {
		return false
	}
	for _, s := range subs[1:] {
		if len(s) < 1 || len(s) > 8 || !allAlnum(s) {
			return false
		}
	}
	return true
}

func (d View) IsTrue(o object.Object) bool {
	b, ok := d.Resolve(o).(object.Boolean)
	return ok && bool(b)
}

func allAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func allAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// DocumentXMP returns the document's decoded XMP metadata packet, or "".
func (doc View) DocumentXMP() string {
	cat := doc.Catalog()
	if cat == nil {
		return ""
	}
	stream, ok := doc.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return ""
	}
	return doc.XMPText(stream)
}

// decodePDFTextString converts a PDF text string to UTF-8. Text strings are
// either UTF-16BE with a BOM (PDF 2.0 adds UTF-8 with a BOM) or
// PDFDocEncoded; comparing raw bytes against UTF-8 XMP values made every
// UTF-16 Info entry "inconsistent" with its metadata counterpart.
func DecodePDFTextString(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		// UTF-16BE
		u := b[2:]
		var sb strings.Builder
		for i := 0; i+1 < len(u); i += 2 {
			r := rune(u[i])<<8 | rune(u[i+1])
			if r >= 0xD800 && r <= 0xDBFF && i+3 < len(u) {
				lo := rune(u[i+2])<<8 | rune(u[i+3])
				if lo >= 0xDC00 && lo <= 0xDFFF {
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
					i += 2
				}
			}
			sb.WriteRune(r)
		}
		return sb.String()
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return string(b[3:]) // UTF-8 with BOM (PDF 2.0)
	}
	// PDFDocEncoding matches ASCII in the printable range; pass through.
	return string(b)
}

// parseToUnicodeMap parses a font's ToUnicode CMap into a map from character
// code to the first Unicode scalar value it produces. Used to tell whether a
// rendered glyph represents whitespace (ISO 32000-1 9.10.3).
func (doc View) ParseToUnicodeMap(fontDict *object.Dictionary) map[int]rune {
	s, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*object.Stream)
	if !ok {
		return nil
	}
	data := doc.Content(s)
	if data == nil {
		return nil
	}
	m := map[int]rune{}
	str := string(data)
	scan := func(begin, end string, isRange bool) {
		rest := str
		for {
			b := strings.Index(rest, begin)
			if b < 0 {
				return
			}
			e := strings.Index(rest[b:], end)
			if e < 0 {
				return
			}
			// Section body lies between the two markers. Guard against a
			// malformed stream where end overlaps begin (low > high).
			lo, hi := b+len(begin), b+e
			if lo > hi {
				rest = rest[b+e+len(end):]
				continue
			}
			for _, line := range strings.Split(rest[lo:hi], "\n") {
				// Tokens are <hhhh> groups, often with no separating space
				// (e.g. <0003><0003><0020>).
				f := AngleTokens(line)
				if isRange && len(f) >= 3 {
					lo, hi, r := HexVal4(f[0]), HexVal4(f[1]), FirstRuneFromHex(f[2])
					if lo >= 0 && hi >= lo && hi-lo < 65536 && r != 0 {
						for c := lo; c <= hi; c++ {
							m[c] = r + rune(c-lo)
						}
					}
				} else if !isRange && len(f) >= 2 {
					if src := HexVal4(f[0]); src >= 0 {
						if r := FirstRuneFromHex(f[1]); r != 0 {
							m[src] = r
						}
					}
				}
			}
			rest = rest[b+e+len(end):]
		}
	}
	scan("beginbfchar", "endbfchar", false)
	scan("beginbfrange", "endbfrange", true)
	return m
}

// AngleTokens returns the <...> tokens in a line, each including the angle
// brackets.
func AngleTokens(line string) []string {
	var out []string
	for {
		i := strings.IndexByte(line, '<')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(line[i:], '>')
		if j < 0 {
			return out
		}
		out = append(out, line[i:i+j+1])
		line = line[i+j+1:]
	}
}

// HexVal4 parses a <hhhh> hex token to an int, or -1.
func HexVal4(s string) int {
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	if s == "" {
		return -1
	}
	v, ok := font.ParseHexN(s)
	if !ok {
		return -1
	}
	return v
}

// FirstRuneFromHex returns the first UTF-16 code unit of a <hhhh...> token as
// a rune, or 0.
func FirstRuneFromHex(tok string) rune {
	tok = strings.TrimPrefix(strings.TrimSuffix(tok, ">"), "<")
	if len(tok) > 4 {
		tok = tok[:4]
	}
	if v, ok := font.ParseHexN(tok); ok {
		return rune(v)
	}
	return 0
}

// sortedObjectNums returns every object number in doc.Objects in ascending
// order. Checks that must be reproducible iterate it instead of ranging the map
// directly: Go randomises map iteration order on every run, so any check whose
// output depends on WHICH object it reaches first — rather than on the set of
// objects it reaches — reported a different object number each time the same
// file was validated. Ascending object number is a total order, so it does not.
//
// Only the checks that are order-sensitive pay for the sort; the many checks
// that emit one finding per object and are sorted afterwards keep ranging the
// map directly.
func (doc View) SortedObjectNums() []int {
	nums := make([]int, 0, len(doc.Objects))
	for num := range doc.Objects {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	return nums
}

// streamFiltersSupported reports whether every filter on the stream is one that
// decodeStreamData can actually apply. Callers use this to tell "we could not
// inspect this stream" apart from "this stream is corrupt": a decode failure on
// an unsupported-but-legal filter must not be reported as a violation.
func StreamFiltersSupported(stream *object.Stream) bool {
	filter := stream.Dict.Get("Filter")
	if filter == nil {
		return true
	}
	parms := stream.Dict.Get("DecodeParms")
	switch f := filter.(type) {
	case object.Name:
		return IsSupportedFilter(f) && predictorSupported(PredictorFromDict(ParmsDictAt(parms, 0)))
	case object.Array:
		for i, e := range f {
			name, ok := e.(object.Name)
			if !ok || !IsSupportedFilter(name) {
				return false
			}
			if !predictorSupported(PredictorFromDict(ParmsDictAt(parms, i))) {
				return false
			}
		}
		return true
	}
	return false
}

// IsSupportedFilter reports whether applyFilter can decode the named filter.
func IsSupportedFilter(name object.Name) bool {
	switch name {
	case "FlateDecode", "LZWDecode", "ASCIIHexDecode":
		return true
	}
	return false
}

// predictorSupported reports whether applyPredictor can reverse the given
// predictor parameters. TIFF horizontal differencing with sub-byte components
// is the one legal-but-unimplemented combination.
func predictorSupported(p PredictorParms) bool {
	switch {
	case p.Predictor == 1:
		return true
	case p.Predictor == 2:
		return p.BitsPerComponent == 8 || p.BitsPerComponent == 16
	case p.Predictor >= 10 && p.Predictor <= 15:
		return true
	}
	return false
}

type CMSSignedData struct {
	Parsed          bool // the bytes are a well-formed SignedData ContentInfo
	HasCertificate  bool // the certificates field carries at least one certificate
	SignerInfoCount int  // number of SignerInfo entries
}

// parseCMSSignedData decodes a DER-encoded CMS/PKCS#7 SignedData structure far
// enough to report whether it embeds a signing certificate and how many
// SignerInfos it contains. It never errors: a blob that is not SignedData (or is
// truncated) simply comes back with parsed=false, since the raw signature bytes
// of an adbe.x509.rsa_sha1 signature are not CMS.
func ParseCMSSignedData(der []byte) CMSSignedData {
	// ContentInfo ::= SEQUENCE { contentType OID, content [0] EXPLICIT ANY }
	var ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		return CMSSignedData{}
	}
	if !ci.ContentType.Equal(oidSignedData) || len(ci.Content.Bytes) == 0 {
		return CMSSignedData{}
	}

	// SignedData ::= SEQUENCE {
	//   version, digestAlgorithms SET, encapContentInfo SEQUENCE,
	//   certificates [0] IMPLICIT OPTIONAL, crls [1] IMPLICIT OPTIONAL,
	//   signerInfos SET OF SignerInfo }
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue   `asn1:"optional,tag:1"`
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return CMSSignedData{}
	}
	return CMSSignedData{
		Parsed:          true,
		HasCertificate:  len(sd.Certificates.Bytes) > 0,
		SignerInfoCount: len(sd.SignerInfos),
	}
}

// oidSignedData is id-signedData (RFC 5652 §5.1): 1.2.840.113549.1.7.2.
// oidSignedData is the CMS SignedData content type (RFC 5652). The signing
// code has its own copy; an OID assigned in 1997 is not going to drift.
var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
