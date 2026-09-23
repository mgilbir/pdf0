package pdf0

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/mgilbir/gopenjpeg"
)

// Hostile inputs for the extractors, built in code.
//
// Each is a whole PDF file, assembled from object bodies written out as text
// and read back through Read, so the parser, the cross-reference table and the
// extractor all see exactly what a hostile file would give them. The same set
// seeds the fuzz corpora (see fuzzSeeds), so the fuzzer starts from every shape
// that has ever broken extraction rather than from well-formed documents only.

// rawObj is one numbered object: a dictionary or array written out verbatim,
// and a stream body when stream is not nil (Length is added).
type rawObj struct {
	dict   string
	stream []byte
}

// buildRawPDF writes objs as objects 1..n with a classic cross-reference table.
// Object 1 must be the catalog.
func buildRawPDF(objs []rawObj) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		if o.stream != nil {
			fmt.Fprintf(&b, "%s/Length %d>>\nstream\n", strings.TrimSuffix(o.dict, ">>"), len(o.stream))
			b.Write(o.stream)
			b.WriteString("\nendstream")
		} else {
			b.WriteString(o.dict)
		}
		b.WriteString("\nendobj\n")
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, x)
	return b.Bytes()
}

// zlibBytes is b Flate-encoded, for building streams outside a test context
// (the fuzz seeds have no *testing.T to hand flateBytes).
func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// rawPage is a one-page document: catalog (1), pages (2), page (3) with the
// given resources, and its Flate content stream (4). Further objects start at 5.
func rawPage(content, resources string) []rawObj {
	return []rawObj{
		{dict: "<</Type/Catalog/Pages 2 0 R>>"},
		{dict: "<</Type/Pages/Kids[3 0 R]/Count 1>>"},
		{dict: "<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]/Contents 4 0 R/Resources " + resources + ">>"},
		{dict: "<</Filter/FlateDecode>>", stream: zlibBytes([]byte(content))},
	}
}

// rawImagePage draws one image XObject, object 5, with the given dictionary
// and data; extra objects follow from 6.
func rawImagePage(imageDict string, data []byte, extra ...rawObj) []byte {
	objs := rawPage("q 100 0 0 100 0 0 cm /Im0 Do Q", "<</XObject<</Im0 5 0 R>>>>")
	objs = append(objs, rawObj{dict: imageDict, stream: data})
	return buildRawPDF(append(objs, extra...))
}

// hugeJPEG is a real JPEG whose frame header claims 65500×65500: image/jpeg
// allocates the frame from the header before it reads a single scan.
func hugeJPEG() []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		panic(err)
	}
	j := buf.Bytes()
	for i := 0; i+8 < len(j); i++ {
		if j[i] == 0xFF && j[i+1] == 0xC0 { // SOF0: length(2) precision(1) height(2) width(2)
			binary.BigEndian.PutUint16(j[i+5:], 65500)
			binary.BigEndian.PutUint16(j[i+7:], 65500)
			return j
		}
	}
	panic("no SOF0 in an encoded JPEG")
}

// hugeJPX is a real JPEG 2000 codestream whose SIZ marker claims a
// 60000×60000 image in one tile.
func hugeJPX() []byte {
	const w, h = 64, 64
	img := gopenjpeg.NewImage(gopenjpeg.ColorSpaceGray, 0, 0, w, h, []gopenjpeg.Component{
		{Dx: 1, Dy: 1, W: w, H: h, Prec: 8, Data: make([]int32, w*h)},
	})
	var buf bytes.Buffer
	if err := gopenjpeg.Encode(img, &buf, gopenjpeg.WithEncodeFormat(gopenjpeg.FormatJ2K), gopenjpeg.WithResolutions(1)); err != nil {
		panic(err)
	}
	c := buf.Bytes()
	for i := 0; i+2 < len(c); i++ {
		if c[i] == 0xFF && c[i+1] == 0x51 { // SIZ: Lsiz Rsiz Xsiz Ysiz XOsiz YOsiz XTsiz YTsiz
			siz := c[i+2:]
			binary.BigEndian.PutUint32(siz[4:], 60000)
			binary.BigEndian.PutUint32(siz[8:], 60000)
			binary.BigEndian.PutUint32(siz[20:], 60000)
			binary.BigEndian.PutUint32(siz[24:], 60000)
			return c
		}
	}
	panic("no SIZ in an encoded codestream")
}

// hostileInput is one file and what it is. The file is built on demand: some
// are tens of megabytes of zeros before compression, and a test that wants one
// input should not pay for all of them inside its memory cap.
type hostileInput struct {
	name  string
	build func() []byte
}

// hostileExtractionInputs are the audit's extraction repros (2026-09-22 §3.B)
// and their siblings, as files.
func hostileExtractionInputs() []hostileInput {
	const (
		sep     = "<</Type/XObject/Subtype/Image/Width 1/Height 1/BitsPerComponent 8/ColorSpace[/Separation/Spot/DeviceGray 6 0 R]>>"
		oneByte = "<</Type/XObject/Subtype/Image/Width 1/Height 1/BitsPerComponent 8/ColorSpace 6 0 R>>"
		tint    = "<</FunctionType 2/Domain[0 1]/C0[0]/C1[1]/N 1>>"
	)
	return []hostileInput{
		// C11: the geometry is 2^60 × 2 and the data four bytes.
		{"image-dim-2^60", func() []byte {
			return rawImagePage("<</Type/XObject/Subtype/Image/Width 1152921504606846976/Height 2/BitsPerComponent 8/ColorSpace/DeviceGray>>", []byte{0, 0, 0, 0})
		}},
		// C11: the data fits the geometry, and the geometry is 484 Mpx — 1.9 GB
		// of NRGBA from a 60 KB file.
		{"image-22000-square", func() []byte {
			return rawImagePage("<</Type/XObject/Subtype/Image/Width 22000/Height 22000/BitsPerComponent 1/ColorSpace/DeviceGray/Filter/FlateDecode>>",
				zlibBytes(make([]byte, 22000/8*22000)))
		}},
		// C11 sibling: a soft mask's own geometry.
		{"smask-22000-square", func() []byte {
			return rawImagePage("<</Type/XObject/Subtype/Image/Width 1/Height 1/BitsPerComponent 8/ColorSpace/DeviceGray/SMask 6 0 R>>", []byte{0},
				rawObj{dict: "<</Type/XObject/Subtype/Image/Width 22000/Height 22000/BitsPerComponent 1/ColorSpace/DeviceGray/Filter/FlateDecode>>", stream: zlibBytes(make([]byte, 22000/8*22000))})
		}},
		// C11 siblings: the codec headers' geometry.
		{"jpeg-header-65500", func() []byte {
			return rawImagePage("<</Type/XObject/Subtype/Image/Width 8/Height 8/BitsPerComponent 8/ColorSpace/DeviceGray/Filter/DCTDecode>>", hugeJPEG())
		}},
		{"jpx-header-60000", func() []byte {
			return rawImagePage("<</Type/XObject/Subtype/Image/Width 8/Height 8/Filter/JPXDecode>>", hugeJPX())
		}},
		// C12: 128 KiB of Group 4 V0 codes, one all-white 2^20-column row each.
		{"ccitt-v0-flood", func() []byte {
			return rawImagePage("<</Type/XObject/Subtype/Image/Width 1048576/Height 1048576/BitsPerComponent 1/ColorSpace/DeviceGray/Filter[/FlateDecode/CCITTFaxDecode]/DecodeParms[null<</K -1/Columns 1048576>>]>>",
				zlibBytes(bytes.Repeat([]byte{0xFF}, 128<<10)))
		}},
		// C13: colour spaces that contain themselves, through each branch.
		{"cs-separation-cycle", func() []byte {
			return rawImagePage(oneByte, []byte{0}, rawObj{dict: "[/Separation/Spot 6 0 R" + tint + "]"})
		}},
		{"cs-devicen-cycle", func() []byte {
			return rawImagePage(oneByte, []byte{0}, rawObj{dict: "[/DeviceN[/A]6 0 R" + tint + "]"})
		}},
		{"cs-iccbased-cycle", func() []byte {
			return rawImagePage(oneByte, []byte{0}, rawObj{dict: "[/ICCBased 7 0 R]"}, rawObj{dict: "<</N 2/Alternate 6 0 R>>", stream: []byte{0}})
		}},
		{"cs-indexed-cycle", func() []byte {
			return rawImagePage(oneByte, []byte{0}, rawObj{dict: "[/Indexed 6 0 R 0 <00>]"})
		}},
		{"cs-mutual-cycle", func() []byte {
			return rawImagePage(oneByte, []byte{0},
				rawObj{dict: "[/Indexed 7 0 R 0 <00>]"},
				rawObj{dict: "[/Separation/Spot 6 0 R" + tint + "]"})
		}},
		// C14: three million nested procedures, 6.6 KB once compressed.
		{"ps-brace-nest", func() []byte {
			return rawImagePage(sep, []byte{0xFF},
				rawObj{dict: "<</FunctionType 4/Domain[0 1]/Range[0 1]/Filter/FlateDecode>>", stream: zlibBytes([]byte(strings.Repeat("{", 3_000_000) + strings.Repeat("}", 3_000_000)))})
		}},
		// C15: /Size whose product wraps.
		{"type0-size-2^62", func() []byte {
			return rawImagePage(sep, []byte{0xFF},
				rawObj{dict: "<</FunctionType 0/Domain[0 1]/Range[0 1]/Size[4611686018427387904]/BitsPerSample 32>>", stream: []byte{1, 2, 3, 4}})
		}},
		// C16: an inline image whose /L is MaxInt64.
		{"inline-image-L-maxint", func() []byte {
			return buildRawPDF(rawPage("BT /F1 12 Tf (hi) Tj ET\nBI /W 1 /H 1 /BPC 8 /CS /G /L 9223372036854775807 ID \x00 EI\n", "<<>>"))
		}},
	}
}

// hostileInputNamed builds one input by name.
func hostileInputNamed(t *testing.T, name string) []byte {
	t.Helper()
	for _, in := range hostileExtractionInputs() {
		if in.name == name {
			return in.build()
		}
	}
	t.Fatalf("no hostile input %q", name)
	return nil
}

// readHostile reads a hostile input, which must parse: these files attack
// extraction, not the reader.
func readHostile(t *testing.T, name string, opts ...Option) *Document {
	t.Helper()
	data := hostileInputNamed(t, name)
	doc, err := Read(bytes.NewReader(data), int64(len(data)), opts...)
	if err != nil {
		t.Fatalf("%s: Read: %v", name, err)
	}
	return doc
}
