package fonts

import (
	"encoding/binary"
	"errors"

	"github.com/mgilbir/forme/font"
)

// What a font's licence lets a document do with it.
//
// A font says so itself, in the fsType field of its OS/2 table (OpenType
// specification, OS/2 table, "fsType"). Four of its bits decide what may be
// embedded and three more how, and a writer that embeds fonts reads them or is
// distributing what the font's owner said not to distribute. Neither this
// package nor forme read them before, so a Restricted License font was
// subsetted into a document without a word.
//
// Three outcomes follow from the bits:
//
//   - Restricted License embedding (0x0002, with neither of the more permissive
//     usage bits beside it): the font must not be embedded at all. Embed
//     refuses, and says so.
//   - Bitmap embedding only (0x0200): only the font's bitmaps may be embedded.
//     This package embeds outlines and nothing else, so it refuses those too.
//   - No subsetting (0x0100): the font may be embedded only whole. Embed then
//     writes the program it was loaded from, untouched, and names it without a
//     subset tag, because it is not one.
//
// Preview & Print (0x0004) and Editable (0x0008) permit what a PDF does with
// a font, and Installable (no usage bit) permits everything. Where more than
// one usage bit is set the least restrictive applies, as the specification
// says, so 0x0006 is Preview & Print and embeds.

const (
	fsTypeRestricted = 0x0002
	fsTypePreview    = 0x0004
	fsTypeEditable   = 0x0008
	fsTypeNoSubset   = 0x0100
	fsTypeBitmapOnly = 0x0200
)

// embeddingRights is a face's fsType, when it was read.
type embeddingRights struct {
	known  bool
	fsType uint16
}

// ErrRestrictedLicense is a font whose licence forbids embedding it.
var ErrRestrictedLicense = errors.New("fonts: the font's licence forbids embedding it " +
	"(OS/2 fsType Restricted License embedding); a document using it cannot carry it")

// ErrBitmapEmbeddingOnly is a font whose licence permits embedding only its
// bitmaps, which this package does not write.
var ErrBitmapEmbeddingOnly = errors.New("fonts: the font's licence permits embedding " +
	"only its bitmaps (OS/2 fsType 0x0200), and this package embeds outlines")

// errNoSubsetUnavailable is a face whose licence requires it to be embedded
// whole, when the whole program is not at hand: a face from Adopt, which is
// handed a shaping face and never its bytes.
var errNoSubsetUnavailable = errors.New("fonts: the font's licence forbids subsetting it " +
	"(OS/2 fsType 0x0100), and this face was not loaded here, so the whole program is " +
	"not available to embed; load it with Load or LoadSimple")

// readFSType reads fsType out of an sfnt's OS/2 table: a uint16 at offset 8.
// A font with no OS/2 table, or one too short to hold the field, states no
// restriction — the table is optional in TrueType, and a font that says
// nothing has not said no.
func readFSType(program []byte) (uint16, bool) {
	os2 := font.SFNTTables(program)["OS/2"]
	if len(os2) < 10 {
		return 0, false
	}
	return binary.BigEndian.Uint16(os2[8:10]), true
}

// readEmbedding records a loaded face's rights, and keeps the program when the
// rights say it has to be embedded whole.
func (f *Face) readEmbedding(data []byte) {
	fsType, ok := readFSType(data)
	f.embedding = embeddingRights{known: true, fsType: fsType}
	if ok && fsType&fsTypeNoSubset != 0 {
		// A copy: the caller's buffer is theirs to reuse, and this is what the
		// document will carry.
		f.program = append([]byte(nil), data...)
	}
}

// check reports whether the rights permit embedding, and whether the program
// must be embedded whole.
func (r embeddingRights) check() (whole bool, err error) {
	usage := r.fsType & 0x000F
	if usage&fsTypeRestricted != 0 && usage&(fsTypePreview|fsTypeEditable) == 0 {
		return false, ErrRestrictedLicense
	}
	if r.fsType&fsTypeBitmapOnly != 0 {
		return false, ErrBitmapEmbeddingOnly
	}
	return r.fsType&fsTypeNoSubset != 0, nil
}

// programToEmbed is the font program Embed writes and the glyphs it carries,
// with what the licence allows decided first.
//
// The rights come from the program the face was loaded from where there is
// one, and otherwise from the subset — which carries the original OS/2 table
// through unchanged, so a face from Adopt is held to its licence too. A face
// that must be embedded whole is embedded from the bytes it was loaded from:
// every glyph, and whole is false only when those bytes are not at hand.
func (f *Face) programToEmbed() (program []byte, kept []int, subset bool, err error) {
	rights := f.embedding
	if !rights.known {
		program, kept, err = f.SubsetGlyphs()
		if err != nil {
			return nil, nil, false, err
		}
		fsType, _ := readFSType(program)
		rights = embeddingRights{known: true, fsType: fsType}
	}
	whole, err := rights.check()
	if err != nil {
		return nil, nil, false, err
	}
	if whole {
		if f.program == nil {
			return nil, nil, false, errNoSubsetUnavailable
		}
		kept = make([]int, f.NumGlyphs())
		for i := range kept {
			kept[i] = i
		}
		return f.program, kept, false, nil
	}
	if program == nil {
		program, kept, err = f.SubsetGlyphs()
		if err != nil {
			return nil, nil, false, err
		}
	}
	return program, kept, true, nil
}
