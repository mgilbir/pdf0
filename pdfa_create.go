package pdf0

import (
	"crypto/md5"
	"fmt"
	"time"
)

// NewPDFADocument creates a minimal valid PDF/A document for the given level.
// The document has an empty page tree and passes ValidatePDFA.
func NewPDFADocument(level PDFALevel) *Document {
	version := pdfaVersion(level)

	// Generate file ID
	now := time.Now().Format(time.RFC3339Nano)
	hash := md5.Sum([]byte("pdf0-pdfa-" + now))
	fileID := String{Value: hash[:], IsHex: true}

	// Object 1: Catalog
	catalog := &Dictionary{}
	catalog.Set("Type", Name("Catalog"))
	catalog.Set("Pages", IndirectRef{Number: 2})
	catalog.Set("Metadata", IndirectRef{Number: 3})
	catalog.Set("OutputIntents", Array{IndirectRef{Number: 4}})

	// Object 2: Pages (empty page tree)
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{})
	pages.Set("Count", Integer(0))

	// Object 3: Metadata stream (XMP, unfiltered)
	xmpData := GenerateXMPMetadata(level, "", "")
	metaStream := &Stream{
		Dict: Dictionary{},
		Data: xmpData,
	}
	metaStream.Dict.Set("Type", Name("Metadata"))
	metaStream.Dict.Set("Subtype", Name("XML"))
	metaStream.Dict.Set("Length", Integer(len(xmpData)))

	// Object 4: OutputIntent dictionary
	outputIntent := &Dictionary{}
	outputIntent.Set("Type", Name("OutputIntent"))
	outputIntent.Set("S", Name("GTS_PDFA1"))
	outputIntent.Set("OutputConditionIdentifier", String{Value: []byte("sRGB IEC61966-2.1")})
	outputIntent.Set("RegistryName", String{Value: []byte("http://www.color.org")})
	outputIntent.Set("Info", String{Value: []byte("sRGB IEC61966-2.1")})
	outputIntent.Set("DestOutputProfile", IndirectRef{Number: 5})

	// Object 5: ICC profile stream
	iccData := DefaultSRGBProfile()
	iccStream := &Stream{
		Dict: Dictionary{},
		Data: iccData,
	}
	iccStream.Dict.Set("N", Integer(3)) // 3-component (RGB)
	iccStream.Dict.Set("Length", Integer(len(iccData)))

	doc := &Document{
		Version: version,
		Objects: map[int]*IndirectObject{
			1: {Number: 1, Generation: 0, Value: catalog},
			2: {Number: 2, Generation: 0, Value: pages},
			3: {Number: 3, Generation: 0, Value: metaStream},
			4: {Number: 4, Generation: 0, Value: outputIntent},
			5: {Number: 5, Generation: 0, Value: iccStream},
		},
		Trailer: Dictionary{
			Keys: []Name{"Root", "ID"},
			Values: []Object{
				IndirectRef{Number: 1},
				Array{fileID, fileID},
			},
		},
	}

	return doc
}

func pdfaVersion(level PDFALevel) string {
	switch level {
	case PDFA1b:
		return "1.4"
	case PDFA2b, PDFA3b:
		return "1.7"
	case PDFA4:
		return "2.0"
	default:
		return "2.0"
	}
}

func pdfaPart(level PDFALevel) int {
	switch level {
	case PDFA1b:
		return 1
	case PDFA2b:
		return 2
	case PDFA3b:
		return 3
	case PDFA4:
		return 4
	default:
		return 4
	}
}

func pdfaConformance(level PDFALevel) string {
	switch level {
	case PDFA1b, PDFA2b, PDFA3b:
		return "B"
	case PDFA4:
		return "" // PDF/A-4 has no conformance level
	default:
		return ""
	}
}

// GenerateXMPMetadata creates XMP metadata bytes for the given PDF/A level.
func GenerateXMPMetadata(level PDFALevel, title, author string) []byte {
	part := pdfaPart(level)
	conformance := pdfaConformance(level)

	titleXMP := ""
	if title != "" {
		titleXMP = fmt.Sprintf(`
      <dc:title>
        <rdf:Alt>
          <rdf:li xml:lang="x-default">%s</rdf:li>
        </rdf:Alt>
      </dc:title>`, xmlEscape(title))
	}

	authorXMP := ""
	if author != "" {
		authorXMP = fmt.Sprintf(`
      <dc:creator>
        <rdf:Seq>
          <rdf:li>%s</rdf:li>
        </rdf:Seq>
      </dc:creator>`, xmlEscape(author))
	}

	conformanceXMP := ""
	if conformance != "" {
		conformanceXMP = fmt.Sprintf(`
      <pdfaid:conformance>%s</pdfaid:conformance>`, conformance)
	}

	revXMP := ""
	if level == PDFA4 {
		revXMP = `
      <pdfaid:rev>2020</pdfaid:rev>`
	}

	xmp := fmt.Sprintf(`<?xpacket begin="%s" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about=""
      xmlns:dc="http://purl.org/dc/elements/1.1/"
      xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"
      xmlns:xmp="http://ns.adobe.com/xap/1.0/">
      <pdfaid:part>%d</pdfaid:part>%s%s%s%s
      <xmp:CreatorTool>pdf0</xmp:CreatorTool>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`, "\xEF\xBB\xBF", part, conformanceXMP, revXMP, titleXMP, authorXMP)

	return []byte(xmp)
}

func xmlEscape(s string) string {
	var result []byte
	for _, b := range []byte(s) {
		switch b {
		case '<':
			result = append(result, []byte("&lt;")...)
		case '>':
			result = append(result, []byte("&gt;")...)
		case '&':
			result = append(result, []byte("&amp;")...)
		case '"':
			result = append(result, []byte("&quot;")...)
		case '\'':
			result = append(result, []byte("&apos;")...)
		default:
			result = append(result, b)
		}
	}
	return string(result)
}

// DefaultSRGBProfile returns a minimal sRGB ICC profile for use in OutputIntent.
// This is a minimal valid ICC profile header (128 bytes) with sRGB colorspace tag.
// It satisfies PDF/A validators that require an embedded ICC profile.
func DefaultSRGBProfile() []byte {
	// Minimal ICC profile: header (128 bytes) + tag table (4 bytes count + tag entries) + data
	// This is the minimum structure needed for a valid ICC profile.
	profile := make([]byte, 0, 400)

	// ICC profile header (128 bytes)
	header := make([]byte, 128)

	// Profile size (will be filled at the end)
	// header[0:4] = size (big-endian uint32)

	// Preferred CMM Type
	copy(header[4:8], []byte("none"))

	// Profile version: 2.1.0
	header[8] = 2  // major
	header[9] = 16 // minor.bugfix (0x10 = 1.0)

	// Profile/Device class: 'mntr' (Monitor)
	copy(header[12:16], []byte("mntr"))

	// Color space: 'RGB '
	copy(header[16:20], []byte("RGB "))

	// Profile Connection Space: 'XYZ '
	copy(header[20:24], []byte("XYZ "))

	// Date and time: 2024-01-01 00:00:00
	header[24] = 0x07
	header[25] = 0xE8 // year = 2024
	header[26] = 0x00
	header[27] = 0x01 // month = 1
	header[28] = 0x00
	header[29] = 0x01 // day = 1

	// Profile file signature: 'acsp'
	copy(header[36:40], []byte("acsp"))

	// Primary platform: 'APPL'
	copy(header[40:44], []byte("APPL"))

	// Rendering intent: perceptual (0)
	// header[64:68] = 0 (already zero)

	// PCS illuminant (D50): X=0.9505, Y=1.0000, Z=1.0890
	// Stored as s15Fixed16Number
	putS15Fixed16(header[68:72], 0.9505)
	putS15Fixed16(header[72:76], 1.0000)
	putS15Fixed16(header[76:80], 1.0890)

	// Profile ID (MD5) - can be zero
	// header[84:100] = 0

	profile = append(profile, header...)

	// Tag table
	// We need minimum required tags for an sRGB monitor profile:
	// desc, wtpt, rXYZ, gXYZ, bXYZ, rTRC, gTRC, bTRC, cprt
	numTags := uint32(9)
	profile = append(profile, byte(numTags>>24), byte(numTags>>16), byte(numTags>>8), byte(numTags))

	// Tag entries are: signature(4) + offset(4) + size(4) = 12 bytes each
	// Data starts after header(128) + tag count(4) + tag entries(9*12=108) = 240
	dataOffset := uint32(128 + 4 + numTags*12)

	// Prepare tag data
	type tagData struct {
		sig  string
		data []byte
	}

	// Description tag
	descText := []byte("sRGB IEC61966-2.1")
	descData := makeDescTag(descText)

	// White point tag (D50)
	wtptData := makeXYZTag(0.9505, 1.0000, 1.0890)

	// Red matrix column
	rXYZData := makeXYZTag(0.4360, 0.2225, 0.0139)

	// Green matrix column
	gXYZData := makeXYZTag(0.3851, 0.7169, 0.0971)

	// Blue matrix column
	bXYZData := makeXYZTag(0.1431, 0.0606, 0.7141)

	// TRC (tone reproduction curve) - gamma 2.2 approximation
	// Use a simple parametric curve or a curv with gamma
	trcData := makeCurvTag(2.2)

	// Copyright
	cprtText := []byte("No copyright, use freely")
	cprtData := makeTextTag(cprtText)

	tags := []tagData{
		{"desc", descData},
		{"wtpt", wtptData},
		{"rXYZ", rXYZData},
		{"gXYZ", gXYZData},
		{"bXYZ", bXYZData},
		{"rTRC", trcData},
		{"gTRC", trcData},
		{"bTRC", trcData},
		{"cprt", cprtData},
	}

	// Write tag directory entries
	currentOffset := dataOffset
	offsets := make([]uint32, len(tags))
	for i, tag := range tags {
		offsets[i] = currentOffset
		profile = append(profile, []byte(tag.sig)...)
		profile = append(profile, byte(currentOffset>>24), byte(currentOffset>>16), byte(currentOffset>>8), byte(currentOffset))
		size := uint32(len(tag.data))
		profile = append(profile, byte(size>>24), byte(size>>16), byte(size>>8), byte(size))
		// Align to 4 bytes
		padded := (size + 3) & ^uint32(3)
		currentOffset += padded
	}

	// Write tag data
	for _, tag := range tags {
		profile = append(profile, tag.data...)
		// Pad to 4-byte boundary
		pad := (4 - len(tag.data)%4) % 4
		for p := 0; p < pad; p++ {
			profile = append(profile, 0)
		}
	}

	// Fill in profile size
	size := uint32(len(profile))
	profile[0] = byte(size >> 24)
	profile[1] = byte(size >> 16)
	profile[2] = byte(size >> 8)
	profile[3] = byte(size)

	return profile
}

func putS15Fixed16(dst []byte, val float64) {
	v := int32(val * 65536.0)
	dst[0] = byte(v >> 24)
	dst[1] = byte(v >> 16)
	dst[2] = byte(v >> 8)
	dst[3] = byte(v)
}

func makeXYZTag(x, y, z float64) []byte {
	// 'XYZ ' type: sig(4) + reserved(4) + XYZ values(12) = 20 bytes
	data := make([]byte, 20)
	copy(data[0:4], []byte("XYZ "))
	// reserved[4:8] = 0
	putS15Fixed16(data[8:12], x)
	putS15Fixed16(data[12:16], y)
	putS15Fixed16(data[16:20], z)
	return data
}

func makeDescTag(text []byte) []byte {
	// 'desc' type: sig(4) + reserved(4) + ASCII count(4) + ASCII string + null
	// + unicode count(4) + unicode code(4) + scriptcode count(1) + scriptcode(67)
	asciiLen := uint32(len(text) + 1) // including null terminator
	data := make([]byte, 0, 12+asciiLen+4+4+1+67)
	data = append(data, []byte("desc")...)
	data = append(data, 0, 0, 0, 0) // reserved
	data = append(data, byte(asciiLen>>24), byte(asciiLen>>16), byte(asciiLen>>8), byte(asciiLen))
	data = append(data, text...)
	data = append(data, 0) // null terminator
	// Unicode localized count
	data = append(data, 0, 0, 0, 0) // count = 0
	// Unicode language code
	data = append(data, 0, 0, 0, 0)
	// Scriptcode count
	data = append(data, 0)
	// Scriptcode data (67 bytes)
	padding := make([]byte, 67)
	data = append(data, padding...)
	return data
}

func makeTextTag(text []byte) []byte {
	// 'text' type: sig(4) + reserved(4) + string + null
	data := make([]byte, 0, 8+len(text)+1)
	data = append(data, []byte("text")...)
	data = append(data, 0, 0, 0, 0) // reserved
	data = append(data, text...)
	data = append(data, 0) // null terminator
	return data
}

func makeCurvTag(gamma float64) []byte {
	// 'curv' type: sig(4) + reserved(4) + count(4) + entries
	// count=1 means a single gamma value stored as u8Fixed8Number
	data := make([]byte, 0, 14)
	data = append(data, []byte("curv")...)
	data = append(data, 0, 0, 0, 0) // reserved
	data = append(data, 0, 0, 0, 1) // count = 1 (gamma)
	// Gamma as u8Fixed8Number (uint16): integer.fraction * 256
	g := uint16(gamma * 256.0)
	data = append(data, byte(g>>8), byte(g))
	return data
}
