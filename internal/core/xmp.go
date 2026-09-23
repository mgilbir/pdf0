package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// Guard identifiers for the XMP model. The packet-size guard is the caller's
// knob; the depth bound is fixed (xmp.MaxDepth) and reported the same way.
const (
	GuardXMPPacket = "xmp-packet-size" // Limits.XMPPacketBytes, WithMaxXMPPacketBytes
	GuardXMPDepth  = "xmp-depth"       // no knob: xmp.MaxDepth
)

// XMPStatus says what DocumentXMPPacket found.
type XMPStatus int

const (
	// XMPAbsent: no /Metadata stream, or one that decodes to nothing.
	XMPAbsent XMPStatus = iota
	// XMPParsed: the packet was modelled; the *xmp.Packet is non-nil.
	XMPParsed
	// XMPMalformed: the packet is not well-formed XML. That is a fact about
	// the document, which the well-formedness rule reports; a reader of a
	// property has nothing to read.
	XMPMalformed
	// XMPLimit: pdf0 declined to model the packet — larger than
	// Limits.XMPPacketBytes, or nested deeper than xmp.MaxDepth. The trip has
	// been noted on the run, so it reaches the report as a "limit" finding. A
	// reader must neither guess a value nor report one as missing.
	XMPLimit
)

type xmpMemoKey struct{}

type xmpMemoEntry struct {
	packet *xmp.Packet
	status XMPStatus
}

// DocumentXMPPacket returns the document's metadata packet through the one XMP
// model, memoised for the run: every identification reader in every validator
// asks here, so the packet is decoded and parsed once per operation and a trip
// is noted once.
func (doc View) DocumentXMPPacket() (*xmp.Packet, XMPStatus) {
	cat := doc.Catalog()
	if cat == nil {
		return nil, XMPAbsent
	}
	stream, ok := doc.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		return nil, XMPAbsent
	}
	return doc.XMPPacketOf(stream, object.RefNum(cat.Get("Metadata")))
}

// XMPPacketOf models the packet in a metadata stream; objNum anchors a limit
// trip. See DocumentXMPPacket.
func (doc View) XMPPacketOf(stream *object.Stream, objNum int) (*xmp.Packet, XMPStatus) {
	memo := Slot[map[*object.Stream]xmpMemoEntry](doc.Run, xmpMemoKey{})
	if *memo == nil {
		*memo = map[*object.Stream]xmpMemoEntry{}
	}
	if e, ok := (*memo)[stream]; ok {
		return e.packet, e.status
	}
	p, status := doc.parseXMP(stream, objNum)
	(*memo)[stream] = xmpMemoEntry{p, status}
	return p, status
}

func (doc View) parseXMP(stream *object.Stream, objNum int) (*xmp.Packet, XMPStatus) {
	text := doc.XMPText(stream)
	if text == "" {
		return nil, XMPAbsent
	}
	if len(text) > doc.Limits.XMPPacketBytes {
		doc.Note(GuardXMPPacket, fmt.Sprintf("the XMP packet is %d bytes, above the %d-byte limit, so its properties were not read", len(text), doc.Limits.XMPPacketBytes), objNum)
		return nil, XMPLimit
	}
	p, err := xmp.Parse([]byte(text))
	switch {
	case err == nil:
		return p, XMPParsed
	case errors.Is(err, xmp.ErrLimit):
		doc.Note(GuardXMPDepth, "the XMP packet nests elements more deeply than pdf0 models, so its properties were not read", objNum)
		return nil, XMPLimit
	default:
		return nil, XMPMalformed
	}
}

// EditableXMP returns the packet in a metadata stream parsed for editing, or a
// new empty packet when there is no stream or it holds nothing.
//
// Every metadata writer starts here, so none of them can fall back to
// regenerating a packet it could not read — which is how SetDocumentInfo and
// EmbedFacturX used to destroy every property some other writer had put there
// (audit C32, C44). A packet that cannot be decoded, is not well-formed, has no
// rdf:RDF or is larger than lim.XMPPacketBytes is an error: replacing it would
// lose whatever it says, and that is the caller's decision to make.
func EditableXMP(cancel Canceler, stream *object.Stream, lim Limits) (*xmp.Packet, error) {
	if stream == nil {
		return xmp.New(), nil
	}
	raw, err := DecodeStreamData(cancel, stream, lim)
	if err != nil {
		return nil, fmt.Errorf("the existing XMP metadata cannot be decoded, so it cannot be edited: %w", err)
	}
	text := DecodeXMPToUTF8(raw)
	if strings.TrimSpace(text) == "" {
		return xmp.New(), nil
	}
	if len(text) > lim.XMPPacketBytes {
		return nil, fmt.Errorf("the existing XMP metadata is %d bytes, above the %d-byte XMP packet limit (WithMaxXMPPacketBytes), so it cannot be edited", len(text), lim.XMPPacketBytes)
	}
	p, err := xmp.Parse([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("the existing XMP metadata cannot be edited: %w", err)
	}
	if !p.HasRDF() {
		return nil, fmt.Errorf("the existing XMP metadata cannot be edited: %w", xmp.ErrNoRDF)
	}
	return p, nil
}

// MetadataStream builds the stream a metadata writer stores a packet in.
// Deliberately unfiltered: a metadata stream is meant to be findable by a tool
// scanning the bytes without parsing the file (ISO 32000-2 14.3.2).
func MetadataStream(packet []byte) *object.Stream {
	return object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Metadata")},
		object.Entry{Key: "Subtype", Value: object.Name("XML")},
		object.Entry{Key: "Length", Value: object.Integer(len(packet))},
	), packet)
}
