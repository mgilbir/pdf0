package fonttest

import "encoding/binary"

// Synthetic ScriptLists, so that script and language selection can be tested
// against a font whose declarations the test states.
//
// A GSUB or GPOS table begins with a ScriptList: which scripts the font covers,
// which language systems each has, and which features each of those selects. A
// reader that ignores it gives every run every rule; a fixture with an empty
// one cannot tell the difference. So the builders here state the list, and the
// simpler builders elsewhere in this package state a default one — a 'DFLT'
// script selecting every feature, which is what a single-script fixture means
// and what makes the reader's selection path, rather than its fallback, the one
// under test.

// NoFeature is what Script.Required and LangSys.Required carry when there is no
// feature that applies unconditionally. It is the value the format itself uses.
const NoFeature = 0xFFFF

// Feature is one entry of a FeatureList: a tag, and the lookup indices it
// names.
//
// Several entries may share a tag — a face declaring a separate 'locl' for each
// language whose letterforms it corrects is ordinary, and Noto Sans has twelve
// — which is why a feature list is a slice here and not a map. Selecting the
// right one of them is most of what script and language selection is for.
type Feature struct {
	Tag     string
	Lookups []int
}

// LangSys is what one language system selects: indices into the feature list,
// and the index of the feature that applies whether or not anything asked for
// it.
type LangSys struct {
	Required int // NoFeature when there is none
	Features []int
}

// Script is what one script selects: its default language system, and any
// language systems it names.
type Script struct {
	Required int // NoFeature when there is none
	Features []int
	Langs    map[string]LangSys

	// NoDefault omits the default language system, which a script is allowed to
	// do. A run that resolves to such a script and names no language it has can
	// take nothing from it, and the reader must move on rather than shape with
	// no rules at all.
	NoDefault bool
}

// AllFeatures is a script selecting every feature of a list of the given
// length, with no required feature — what a single-script fixture declares.
func AllFeatures(n int) Script {
	s := Script{Required: NoFeature, Features: make([]int, n)}
	for i := range s.Features {
		s.Features[i] = i
	}
	return s
}

// defaultScripts is the script list a builder that was given none emits: one
// 'DFLT' script selecting everything. 'DFLT' is the tag a shaper falls back to
// for any script the font does not name, so this selects every feature for
// every run — the same rules as before there was a script list to read, but
// stated rather than absent.
func defaultScripts(featureCount int) map[string]Script {
	return map[string]Script{"DFLT": AllFeatures(featureCount)}
}

// scriptListTable encodes a ScriptList. The records are in tag order, which is
// what the format requires.
func scriptListTable(scripts map[string]Script) []byte {
	tags := make([]string, 0, len(scripts))
	for tag := range scripts {
		tags = append(tags, tag)
	}
	sortStrings(tags)

	out := make([]byte, 2+6*len(tags))
	binary.BigEndian.PutUint16(out[0:], uint16(len(tags)))
	for i, tag := range tags {
		rec := 2 + 6*i
		copy(out[rec:], tag)
		binary.BigEndian.PutUint16(out[rec+4:], uint16(len(out)))
		out = append(out, scriptTable(scripts[tag])...)
	}
	return out
}

// scriptTable encodes one Script: an offset to its default language system,
// then a record per named one. The offsets are from the Script table's own
// start, so it is built self-contained and placed by its caller.
func scriptTable(s Script) []byte {
	langs := make([]string, 0, len(s.Langs))
	for tag := range s.Langs {
		langs = append(langs, tag)
	}
	sortStrings(langs)

	out := make([]byte, 4+6*len(langs))
	binary.BigEndian.PutUint16(out[2:], uint16(len(langs)))
	for i, tag := range langs {
		rec := 4 + 6*i
		copy(out[rec:], tag)
		binary.BigEndian.PutUint16(out[rec+4:], uint16(len(out)))
		ls := s.Langs[tag]
		out = append(out, langSysTable(ls.Required, ls.Features)...)
	}
	if !s.NoDefault {
		binary.BigEndian.PutUint16(out[0:], uint16(len(out)))
		out = append(out, langSysTable(s.Required, s.Features)...)
	}
	return out
}

// langSysTable encodes one LangSys: a null lookup order, the required feature,
// and the features selected.
func langSysTable(required int, features []int) []byte {
	out := make([]byte, 6+2*len(features))
	binary.BigEndian.PutUint16(out[2:], uint16(required))
	binary.BigEndian.PutUint16(out[4:], uint16(len(features)))
	for i, v := range features {
		binary.BigEndian.PutUint16(out[6+2*i:], uint16(v))
	}
	return out
}

// featureListTable encodes a FeatureList: a record per feature, each naming the
// lookups it applies.
func featureListTable(features []Feature) []byte {
	out := make([]byte, 2+6*len(features))
	binary.BigEndian.PutUint16(out[0:], uint16(len(features)))
	for i, f := range features {
		body := make([]byte, 4+2*len(f.Lookups))
		binary.BigEndian.PutUint16(body[2:], uint16(len(f.Lookups)))
		for j, v := range f.Lookups {
			binary.BigEndian.PutUint16(body[4+2*j:], uint16(v))
		}
		rec := 2 + 6*i
		copy(out[rec:], f.Tag)
		binary.BigEndian.PutUint16(out[rec+4:], uint16(len(out)))
		out = append(out, body...)
	}
	return out
}

// GSUBTable builds a GSUB table from an explicit lookup list, feature list and
// script list. It is the general form: everything else here is a shorthand for
// some arrangement of these three.
//
// The script list refers to features by their index in the feature list, which
// is how the format does it, and is what lets a fixture give two scripts a
// different feature carrying the same tag.
func GSUBTable(lookups []Lookup, features []Feature, scripts map[string]Script) []byte {
	return layoutTableFull(lookups, features, scripts)
}

// GPOSTable is the same for a positioning table. The scaffolding is identical —
// the two tables differ only in what their lookup types mean.
func GPOSTable(lookups []Lookup, features []Feature, scripts map[string]Script) []byte {
	return layoutTableFull(lookups, features, scripts)
}

func layoutTableFull(lookups []Lookup, features []Feature, scripts map[string]Script) []byte {
	if scripts == nil {
		scripts = defaultScripts(len(features))
	}
	header := make([]byte, 10)
	binary.BigEndian.PutUint32(header[0:], 0x00010000)
	out := append([]byte(nil), header...)
	binary.BigEndian.PutUint16(out[4:], uint16(len(out)))
	out = append(out, scriptListTable(scripts)...)
	binary.BigEndian.PutUint16(out[6:], uint16(len(out)))
	out = append(out, featureListTable(features)...)
	binary.BigEndian.PutUint16(out[8:], uint16(len(out)))
	out = append(out, lookupListTable(lookups)...)
	return out
}

// lookupListTable encodes a LookupList, each lookup with its subtables laid out
// after its header.
func lookupListTable(lookups []Lookup) []byte {
	out := make([]byte, 2+2*len(lookups))
	binary.BigEndian.PutUint16(out[0:], uint16(len(lookups)))
	for i, lk := range lookups {
		body := make([]byte, 6+2*len(lk.Subtables))
		binary.BigEndian.PutUint16(body[0:], uint16(lk.Type))
		binary.BigEndian.PutUint16(body[2:], uint16(lk.Flag))
		binary.BigEndian.PutUint16(body[4:], uint16(len(lk.Subtables)))
		for j, sub := range lk.Subtables {
			binary.BigEndian.PutUint16(body[6+2*j:], uint16(len(body)))
			body = append(body, sub...)
		}
		binary.BigEndian.PutUint16(out[2+2*i:], uint16(len(out)))
		out = append(out, body...)
	}
	return out
}
