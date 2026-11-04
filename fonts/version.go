package fonts

import "github.com/mgilbir/pdf0/object"

// The PDF version an embedding needs.
//
// A caller that writes its own document, and its own header, has to declare a
// version at least as late as every feature the file uses, or a reader that
// holds the file to its header may refuse what it does not expect: a strict
// embedder such as xdvipdfmx warns about a file that uses a later feature than
// it declares. What a face is written as decides which features those are:
// the OpenType font file a CFF face is wrapped in is PDF 1.6, the bare CFF is
// PDF 1.3, a Type 3 font painting a colour glyph through a soft mask is
// PDF 1.4. So the writer, which is the one place that knows what it wrote,
// says so (Embedded.Version), rather than leaving every caller to work it out
// from the face and the options.
//
// It is computed from the objects as they are added, not decided beside them:
// versionAllocator sees every object EmbedForms adds, whichever path wrote it,
// and objectVersion reads what each one uses. A path that comes to write a
// feature later than any it wrote before therefore raises the version without
// anyone remembering to; TestEveryFeatureTheWriterUsesHasAVersion fails on a
// key or name the table below has not classified, so a feature cannot be added
// without its version being decided.
//
// The versions are ISO 32000-2's, where it marks a feature "(PDF 1.n)". Every
// feature not listed is in PDF 1.0 or 1.1, which no reader of a PDF this
// package writes predates: Flate, which every stream here is compressed
// with, is PDF 1.2 already.

// pdfVersion is a PDF version as minor*10 of 1.x: 12 is PDF 1.2. PDF 2.0
// features are not written by this package.
type pdfVersion int

func (v pdfVersion) String() string {
	return "1." + string(rune('0'+int(v)%10))
}

// keyVersions are the dictionary keys that need a version later than 1.1,
// whatever their value.
var keyVersions = map[object.Name]pdfVersion{
	// Table 109/121: ToUnicode is PDF 1.2.
	"ToUnicode": 12,
	// Table 120: FontFile3 is PDF 1.2.
	"FontFile3": 12,
	// A composite font's own entries (9.7, Tables 119, 115, 116, 122, 124),
	// none of which exists before composite fonts did, in PDF 1.2.
	"DescendantFonts": 12, "CIDSystemInfo": 12, "Registry": 12, "Ordering": 12,
	"Supplement": 12, "DW": 12, "W": 12, "DW2": 12, "W2": 12, "CIDToGIDMap": 12,
	"CIDSet": 12,
	// A Type 3 font's and a form's /Resources (Tables 110, 93) are PDF 1.2;
	// the ExtGState resources inside them are too (8.4.5).
	"Resources": 12,
	"ExtGState": 12,
	// Table 145 (image dictionary): an explicit /Mask is PDF 1.3.
	"Mask": 13,
	// 8.7.4.3: shading dictionaries and the Shading resource are PDF 1.3.
	"Shading":     13,
	"ShadingType": 13,
	// PDF 1.4 transparency: a soft mask (Tables 57, 145), a group (Tables 93,
	// 96), the blend mode and the alpha constants (Table 57).
	"SMask": 14,
	"Group": 14,
	"BM":    14,
	"ca":    14,
	"CA":    14,
}

// subtypeVersions are the /Subtype values that need a later version than
// 1.1: the font file formats (Table 124) and the font kinds (9.7).
var subtypeVersions = map[object.Name]pdfVersion{
	"Type0":        12, // a composite font, 9.7.1
	"CIDFontType0": 12,
	// FontFile2 for a CIDFontType2 is PDF 1.3 (Table 124). The descriptor
	// that carries it cannot tell it is a CIDFont's, so the CIDFont's own
	// subtype carries the version; one written by name (Forms.Named), with
	// no FontFile2, states 1.3 where 1.2 would do.
	"CIDFontType2":  13,
	"Type1C":        12,
	"CIDFontType0C": 13,
	"OpenType":      16,
}

// filterVersions are the stream filters later than 1.1 (Table 6).
var filterVersions = map[object.Name]pdfVersion{
	"FlateDecode": 12,
}

// functionVersions are the function types (7.10): type 0 is PDF 1.2, types
// 2, 3 and 4 are PDF 1.3.
var functionVersions = map[int64]pdfVersion{0: 12, 2: 13, 3: 13, 4: 13}

// objectVersion is the latest version a feature of o needs, 11 if none
// needs more than PDF 1.1. It reads o and what o holds directly, not what it
// refers to: each referenced object is added, and read, on its own.
func objectVersion(o object.Object) pdfVersion {
	v := pdfVersion(11)
	raise := func(w pdfVersion) {
		if w > v {
			v = w
		}
	}
	var walk func(o object.Object)
	walk = func(o object.Object) {
		switch x := o.(type) {
		case *object.Stream:
			walk(&x.Dict)
		case *object.Dictionary:
			for key, val := range x.All() {
				raise(keyVersions[key])
				switch key {
				case "Subtype":
					if n, ok := val.(object.Name); ok {
						raise(subtypeVersions[n])
					}
				case "Filter":
					switch f := val.(type) {
					case object.Name:
						raise(filterVersions[f])
					case object.Array:
						for _, e := range f {
							if n, ok := e.(object.Name); ok {
								raise(filterVersions[n])
							}
						}
					}
				case "FunctionType":
					if n, ok := val.(object.Integer); ok {
						raise(functionVersions[int64(n)])
					}
				}
				walk(val)
			}
		case object.Array:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(o)
	return v
}

// versionAllocator is an Allocator that passes every object on to doc and
// keeps the latest version any of them needs.
type versionAllocator struct {
	doc     Allocator
	version pdfVersion
}

func (a *versionAllocator) Add(o object.Object) object.IndirectRef {
	if v := objectVersion(o); v > a.version {
		a.version = v
	}
	return a.doc.Add(o)
}
