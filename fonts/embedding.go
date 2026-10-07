package fonts

import (
	"errors"

	"github.com/mgilbir/forme/shape"
)

// What a font's licence lets a document do with it.
//
// A font says so itself, in the fsType field of its OS/2 table (OpenType
// specification, OS/2 table, "fsType"). Four of its bits decide what may be
// embedded and three more how, and a writer that embeds fonts reads them or is
// distributing what the font's owner said not to distribute. forme reads them
// when it loads a program (shape.Face.EmbeddingPermissions), and keeps the
// program (shape.Face.Program), so every face answers both questions however
// it was made.
//
// Three outcomes follow from the bits:
//
//   - Restricted License embedding (0x0002, with neither of the more permissive
//     usage bits beside it): the font must not be embedded at all. Embed
//     refuses, and says so.
//   - Bitmap embedding only (0x0200): only the font's bitmaps may be embedded.
//     A face whose glyphs are only bitmaps, and a face with outlines and
//     colour strikes (CBDT, sbix), are embedded as images of them, in Type 3
//     fonts (type3.go), which is what the bit permits; a glyph of the second
//     whose only ink is its outline is refused. A face with outlines and no
//     strikes forme paints beside them — EBDT, until forme#918 — is refused.
//   - No subsetting (0x0100): the font may be embedded only whole. Embed then
//     writes the program it was loaded from, untouched, and names it without a
//     subset tag, because it is not one. That includes a face from Adopt.
//
// Preview & Print (0x0004) and Editable (0x0008) permit what a PDF does with
// a font, and Installable (no usage bit) permits everything. Where more than
// one usage bit is set the least restrictive applies, as the specification
// says, so 0x0006 is Preview & Print and embeds.

// ErrRestrictedLicense is a font whose licence forbids embedding it.
var ErrRestrictedLicense = errors.New("fonts: the font's licence forbids embedding it " +
	"(OS/2 fsType Restricted License embedding); a document using it cannot carry it")

// ErrBitmapEmbeddingOnly is a font whose licence permits embedding only its
// bitmaps, and which has none this package can write: a face with outlines
// whose strikes are EBDT, or a glyph whose only ink is its outline.
var ErrBitmapEmbeddingOnly = errors.New("fonts: the font's licence permits embedding " +
	"only its bitmaps (OS/2 fsType 0x0200), and this package embeds outlines")

// ErrBitmapNoSubsetting is a face whose glyphs are only bitmaps and whose
// licence forbids subsetting it. Its Type 3 fonts carry images of the glyphs a
// document drew, which is a subset, and they are not written.
var ErrBitmapNoSubsetting = errors.New("fonts: the font's licence forbids subsetting it " +
	"(OS/2 fsType 0x0100), and a face whose glyphs are only bitmaps is embedded as images of the glyphs drawn")

// type3Allowed is embeddingAllowed for a face whose glyphs are only bitmaps,
// which is embedded as images of them: what the bitmap-only bit permits, and
// what no subsetting forbids.
func type3Allowed(fsType shape.FSType, stated bool) error {
	if !stated {
		return nil
	}
	permissive := shape.FSTypePreviewPrint | shape.FSTypeEditable
	if fsType&shape.FSTypeRestricted != 0 && fsType&permissive == 0 {
		return ErrRestrictedLicense
	}
	if fsType&shape.FSTypeNoSubsetting != 0 {
		return ErrBitmapNoSubsetting
	}
	return nil
}

// errNoProgram is a face that must be embedded whole and has no program to
// embed. Only a standard face has none, and a standard face is never
// embedded, so reaching this is a face forme built some other way.
var errNoProgram = errors.New("fonts: the font's licence forbids subsetting it " +
	"(OS/2 fsType 0x0100), and the face has no program to embed whole")

// embeddingAllowed reports whether a font's fsType permits embedding, and
// whether the program must be embedded whole.
//
// The bits are the face's own, read by forme when it loaded the program
// (shape.Face.EmbeddingPermissions), for a face loaded here and for one
// handed to Adopt alike. A font that states none — no OS/2 table, or one too
// short to reach the field — has placed no restriction: the table is
// optional in TrueType, and a font that says nothing has not said no.
func embeddingAllowed(fsType shape.FSType, stated bool) (whole bool, err error) {
	if !stated {
		return false, nil
	}
	permissive := shape.FSTypePreviewPrint | shape.FSTypeEditable
	if fsType&shape.FSTypeRestricted != 0 && fsType&permissive == 0 {
		return false, ErrRestrictedLicense
	}
	if fsType&shape.FSTypeBitmapOnly != 0 {
		return false, ErrBitmapEmbeddingOnly
	}
	return fsType&shape.FSTypeNoSubsetting != 0, nil
}

// programToEmbed is the font program Embed writes and the glyphs it carries,
// with what the licence allows decided first.
//
// A face that must be embedded whole is embedded from the program it was
// loaded from, which forme keeps (shape.Face.Program): every glyph, kept
// listing them all, and subset false. For a face loaded from a WOFF that is
// the sfnt the container held, which is what a font file stream carries.
// Every other face is subsetted to the glyphs it drew.
func (f *Face) programToEmbed() (program []byte, kept []int, subset bool, err error) {
	whole, err := embeddingAllowed(f.EmbeddingPermissions())
	if err != nil {
		return nil, nil, false, err
	}
	if whole {
		program = f.Program()
		if program == nil {
			return nil, nil, false, errNoProgram
		}
		kept = make([]int, f.NumGlyphs())
		for i := range kept {
			kept[i] = i
		}
		return program, kept, false, nil
	}
	program, kept, err = f.SubsetGlyphs()
	if err != nil {
		return nil, nil, false, err
	}
	return program, kept, true, nil
}
