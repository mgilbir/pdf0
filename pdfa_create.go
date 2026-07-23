package pdf0

import (
	"crypto/md5"
	"fmt"
	"time"

	lcms2 "github.com/mgilbir/golittlecms"
)

// NewPDFADocument creates a minimal valid PDF/A document for the given level.
// The document has an empty page tree and passes ValidatePDFA.
func NewPDFADocument(level PDFALevel) *Document {
	return NewPDFADocumentWithInfo(level, "", "")
}

// NewPDFADocumentWithInfo is NewPDFADocument with the document title and
// author embedded in the generated XMP metadata.
func NewPDFADocumentWithInfo(level PDFALevel, title, author string) *Document {
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
	xmpData := GenerateXMPMetadata(level, title, author)
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
	iccData := sRGBProfile(level)
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
			// Control characters other than tab/LF/CR are illegal in XML
			// 1.0 even when escaped; passing them through produced
			// malformed XMP. Drop them.
			if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
				continue
			}
			result = append(result, b)
		}
	}
	return string(result)
}

// DefaultSRGBProfile returns a real sRGB ICC profile (ICC v2.1, valid at every
// PDF/A level) generated by golittlecms, a pure-Go Little CMS port. It is
// exported for callers that assemble their own OutputIntent; NewPDFADocument
// uses sRGBProfile to select a level-appropriate profile version.
func DefaultSRGBProfile() []byte {
	return sRGBProfile(PDFA1b)
}

// sRGBProfile builds an sRGB ICC profile suitable for the given PDF/A level.
// PDF/A-1 targets PDF 1.4, which permits only ICC v2 profiles, so it receives an
// ICC v2.1 profile; PDF/A-2/3/4 target PDF 1.7/2.0 and receive the compact ICC
// v4 profile. Both are genuine profiles emitted by — and re-readable through — a
// real colour-management engine.
func sRGBProfile(level PDFALevel) []byte {
	version := 4.3
	if level == PDFA1b {
		version = 2.1
	}
	data, err := buildSRGBProfile(version)
	if err != nil {
		// Create_sRGBProfile builds from fixed constants and SaveProfileToMem
		// writes to an in-memory buffer, so an error here is a library-internal
		// invariant failure rather than a runtime condition. Fail loudly.
		panic("pdf0: generating sRGB ICC profile: " + err.Error())
	}
	return data
}

// buildSRGBProfile generates an sRGB ICC profile serialized at the requested ICC
// version (e.g. 2.1 or 4.3) using golittlecms.
func buildSRGBProfile(version float64) ([]byte, error) {
	p, err := lcms2.Create_sRGBProfile()
	if err != nil {
		return nil, err
	}
	p.SetProfileVersion(version)
	return p.SaveProfileToMem()
}
