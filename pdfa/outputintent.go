package pdfa

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Bringing your own output-intent profile.
//
// A generated PDF/A document needs an output intent, and pdf0 embeds an sRGB
// profile so that the common case needs no colour management from the caller.
// That is a default, not a policy: a caller with a press profile, a house sRGB
// variant, or a requirement that the bytes be exactly the ones they audited
// should be able to supply it and get a document with theirs in it and nothing
// of pdf0's.
//
// What cannot be a default is what the intent says *about* the profile. /N has
// to be the profile's own component count, and claiming "sRGB IEC61966-2.1" of
// a CMYK press profile would be a false statement in the file. So the component
// count is read from the profile and the identifier is the caller's to give.

// The ICC header fields this reads (ICC.1:2010, table 13). The header is 128
// bytes; a tag table follows, so a profile shorter than that is not one.
const (
	iccHeaderSize     = 128
	iccSizeAt         = 0  // uInt32Number: the profile's own declared size
	iccVersionAt      = 8  // the first byte is the major version
	iccColourSpaceAt  = 16 // dataColourSpace signature, e.g. "RGB "
	iccColourSpaceEnd = 20
)

// ErrNotAProfile is returned for bytes that are not an ICC profile at all, as
// distinct from a profile pdf0 will not use.
var ErrNotAProfile = errors.New("pdfa: not an ICC profile")

// ICCComponents is the number of colour components the profile's data colour
// space has, which is what an ICCBased colour space and an output intent's
// profile stream record as /N.
//
// Only the three PDF/A permits are recognised. A profile in Lab, or one of the
// many-channel spaces, is a profile pdf0 has nothing correct to write for.
func ICCComponents(profile []byte) (int, error) {
	if len(profile) < iccHeaderSize {
		return 0, fmt.Errorf("%w: %d bytes, shorter than the 128-byte header",
			ErrNotAProfile, len(profile))
	}
	if declared := binary.BigEndian.Uint32(profile[iccSizeAt:]); int(declared) != len(profile) {
		// A profile whose header disagrees with its own length has been
		// truncated or spliced, and embedding it would put that disagreement
		// in the document for a reader to trip over.
		return 0, fmt.Errorf("%w: the header declares %d bytes and there are %d",
			ErrNotAProfile, declared, len(profile))
	}
	switch space := string(profile[iccColourSpaceAt:iccColourSpaceEnd]); space {
	case "GRAY":
		return 1, nil
	case "RGB ":
		return 3, nil
	case "CMYK":
		return 4, nil
	default:
		return 0, fmt.Errorf("pdfa: ICC profile data colour space %q is not one of "+
			"GRAY, RGB or CMYK, which are the spaces a PDF/A output intent may use", space)
	}
}

// iccMajorVersion is the profile's major ICC version, which decides whether a
// level can use it: PDF/A-1 is based on PDF 1.4, and PDF 1.4 permits only ICC
// v2.
func iccMajorVersion(profile []byte) int {
	if len(profile) < iccHeaderSize {
		return 0
	}
	return int(profile[iccVersionAt])
}

// OutputIntentSpec is the output intent a generated document carries.
//
// The zero value asks for pdf0's embedded sRGB profile for the level, described
// as sRGB, which is what NewPDFADocument produces.
type OutputIntentSpec struct {
	// ICCProfile is the destination profile to embed. Nil asks for pdf0's
	// embedded sRGB for the level.
	//
	// The bytes are used as given and are not copied, so a caller that reuses
	// the slice afterwards should pass a copy.
	ICCProfile []byte

	// OutputConditionIdentifier names the intended output condition — a
	// characterisation from the ICC registry, or any string the producer and
	// consumer agree on. Required with ICCProfile, because pdf0 has nothing
	// true to say about a profile it did not make.
	OutputConditionIdentifier string

	// RegistryName and Info are optional. RegistryName defaults to the ICC
	// registry URL; Info defaults to the identifier.
	RegistryName string
	Info         string
}

// resolve returns the profile bytes, its component count, and the three
// strings, filling in pdf0's defaults for a zero spec.
func (s OutputIntentSpec) resolve(level Level) (profile []byte, n int, id, registry, info string, err error) {
	if s.ICCProfile == nil {
		if s.OutputConditionIdentifier != "" || s.Info != "" || s.RegistryName != "" {
			// Describing pdf0's profile as something else would make the
			// document say what is not so.
			return nil, 0, "", "", "", errors.New("pdfa: an output-intent " +
				"identifier was given with no ICCProfile; supply the profile the " +
				"identifier describes, or leave both unset for pdf0's sRGB")
		}
		const sRGB = "sRGB IEC61966-2.1"
		return sRGBProfile(level), 3, sRGB, "http://www.color.org", sRGB, nil
	}

	if s.OutputConditionIdentifier == "" {
		return nil, 0, "", "", "", errors.New("pdfa: an ICCProfile was given with " +
			"no OutputConditionIdentifier; the intent has to name the output " +
			"condition the profile characterises, and pdf0 cannot name it for you")
	}
	n, err = ICCComponents(s.ICCProfile)
	if err != nil {
		return nil, 0, "", "", "", err
	}
	if level.BaseB() == PDFA1b && iccMajorVersion(s.ICCProfile) > 2 {
		return nil, 0, "", "", "", fmt.Errorf("pdfa: the profile is ICC v%d and %s "+
			"is based on PDF 1.4, which permits only ICC v2",
			iccMajorVersion(s.ICCProfile), level)
	}

	registry, info = s.RegistryName, s.Info
	if registry == "" {
		registry = "http://www.color.org"
	}
	if info == "" {
		info = s.OutputConditionIdentifier
	}
	return s.ICCProfile, n, s.OutputConditionIdentifier, registry, info, nil
}
