package core

import (
	"fmt"
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
