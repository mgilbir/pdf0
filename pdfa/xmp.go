package pdfa

import (
	"bytes"
	"encoding/xml"
	"io"

	"github.com/mgilbir/pdf0/internal/xmp"
)

// This file connects PDF/A metadata validation (ISO 19005-1, 6.7.9
// "Properties"; ISO 19005-2/-3, 6.6.2.3 "Schemas") to pdf0's one XMP model,
// internal/xmp. Properties in predefined schemas must exist in the schema and
// carry the schema's value form (simple/Bag/Seq/LangAlt/structure, plus
// simple-value syntax like Integer or Rational); properties in any other
// namespace must be declared by an embedded PDF/A extension schema.
//
// The packet is parsed by the model, not here: this package used to carry a
// parser of its own, and every identification reader beside it scraped the text
// instead, so the same packet was read three ways. The names below are the
// model's, kept under this package's spelling so the rule tables read as they
// always have.

// RDF and XMP container namespaces.
const (
	nsRDF = xmp.NSRDF
	nsXML = xmp.NSXML

	nsPDFAExtension = xmp.NSPDFAExtension
	nsPDFASchema    = xmp.NSPDFASchema
	nsPDFAProperty  = xmp.NSPDFAProperty
	nsPDFAType      = xmp.NSPDFAType
	nsPDFAField     = xmp.NSPDFAField
)

// The structural form of an XMP property value, and the parsed value types.
type (
	xmpKind     = xmp.Kind
	xmpValue    = xmp.Value
	xmpField    = xmp.Field
	xmpProperty = xmp.Property
)

const (
	xmpSimple = xmp.Simple
	xmpStruct = xmp.Struct
	xmpBag    = xmp.Bag
	xmpSeq    = xmp.Seq
	xmpAlt    = xmp.Alt
)

// The bound on an XMP packet that the model will build a tree for is
// Limits.XMPPacketBytes (core.DefaultMaxXMPPacketBytes; WithMaxXMPPacketBytes
// changes it). Above it core.View.DocumentXMPPacket declines to parse and notes
// a trip, which reaches the report as a "limit" finding: the property checks
// did not run, and the report says so rather than looking clean.
//
// The default is 4 MiB. The conformance corpus is no guide to the real-world
// tail: its largest packet is 66 KB, while a 978-file Common Crawl sample had
// one of 1,639,865 bytes. A pathological packet has only its property checks
// skipped; well-formedness is still validated by streaming (see xmpWellFormed).

// xmpWellFormed streams an XMP packet and reports whether it is well-formed XML
// and whether it contains a properly namespaced rdf:RDF element, WITHOUT
// building a node tree. Well-formedness needs no tree — the streaming decoder
// already validates tag structure as it goes — so this stays O(n) and needs no
// size cap, which is why the well-formedness rule still runs on a packet too
// large to model. wellFormed is false for an empty or non-element packet,
// matching the model's "no XML content" error.
func xmpWellFormed(data []byte) (wellFormed, hasRDF bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	sawElement := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return sawElement, hasRDF
		}
		if err != nil {
			return false, hasRDF
		}
		if se, ok := tok.(xml.StartElement); ok {
			sawElement = true
			if se.Name.Space == nsRDF && se.Name.Local == "RDF" {
				hasRDF = true
			}
		}
	}
}
