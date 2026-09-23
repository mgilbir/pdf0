package core

import (
	"encoding/asn1"
	"fmt"

	"sort"
	"strings"

	"unicode/utf8"

	"github.com/mgilbir/pdf0/internal/pdfdoc"
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

// XMPText decodes an XMP packet to UTF-8 text, with MetadataContent's Reason.
// Every XMP consumer goes through it so no site can route the document's
// identification back through the content budget.
func (doc View) XMPText(stream *object.Stream) (string, Reason) {
	data, r := doc.MetadataContent(stream)
	return DecodeXMPToUTF8(data), r
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

// IsAnnotation reports whether dict is an annotation.
//
// It takes the view because both keys it reads may be indirect references, and
// it gates every annotation rule in the package: a document that writes
// `/Type 9 0 R` naming `/Annot` is a legal document, and reading the key
// without resolving it made that document's annotations invisible to the
// checks rather than conforming.
func (v View) IsAnnotation(dict *object.Dictionary) bool {
	if t, ok := v.ResolveName(dict.Get("Type")); ok && t == "Annot" {
		return true
	}
	// Also detect annotations by Subtype + Rect (some PDFs omit /Type)
	if _, ok := v.ResolveName(dict.Get("Subtype")); ok && dict.Get("Rect") != nil {
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

// ValidLanguageTag reports whether tag matches the language-identifier grammar
// a PDF /Lang entry must follow: a primary tag of 1–8 letters, then any number
// of hyphen-separated subtags of 1–8 characters each.
//
// digitsInSubtags selects between the two vintages the PDF/A parts are written
// against, and they genuinely differ. ISO 19005-1 cites PDF Reference 9.8.1,
// hence RFC 1766, where a subtag is 1*8ALPHA — so "en-12" is not a language
// identifier at Level 1a. ISO 19005-2 and -3 cite ISO 32000-1 14.9.2, hence
// RFC 3066, which widened a subtag to alphanumerics — which is why the parts-2/3
// corpus offers "ru-petr1708" as a *conforming* file. One rule for both parts
// would have to be wrong about one of them.
//
// The empty string is the caller's business: /Lang is explicitly permitted to be
// empty ("the language is unknown"), but a language *identifier* is not.
func ValidLanguageTag(tag string, digitsInSubtags bool) bool {
	subs := strings.Split(tag, "-")
	if first := subs[0]; len(first) < 1 || len(first) > 8 || !allAlpha(first) {
		return false
	}
	for _, s := range subs[1:] {
		if len(s) < 1 || len(s) > 8 {
			return false
		}
		if digitsInSubtags {
			if !allAlnum(s) {
				return false
			}
		} else if !allAlpha(s) {
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

// DocumentXMP returns the document's decoded XMP metadata packet, or "", with
// the Reason: ReasonAbsent when the catalog names no metadata stream.
func (doc View) DocumentXMP() (string, Reason) {
	cat := doc.Catalog()
	if cat == nil {
		return "", ReasonAbsent
	}
	stream, ok := doc.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return "", ReasonAbsent
	}
	return doc.XMPText(stream)
}

// DecodePDFTextString converts a PDF text string to UTF-8. Text strings are
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
	// Neither byte-order mark: the string is PDFDocEncoded (ISO 32000-2
	// 7.9.2.2). It agrees with ASCII only in the printable range, so passing
	// the bytes through made "Caf\351" the invalid UTF-8 "Caf\xe9" rather than
	// "Café", and every comparison against XMP (which is Unicode) failed on it.
	return pdfdoc.Decode(b)
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
