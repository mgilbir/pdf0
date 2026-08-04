package fonttest

import "encoding/binary"

// Synthetic FeatureVariations tables, so that reading them can be tested against
// a font whose design space the test states.
//
// FeatureVariations is what gives a feature *different* lookups at different
// points in a variable font's design space, and the only thing that decides
// which record applies is a set of conditions on the axis coordinates. A fixture
// therefore has to be able to state a record that covers the default instance
// and one that does not: without both, a reader that ignored the conditions
// entirely would pass.

// Condition is one condition of a record: a range on one axis, in the
// normalized coordinates the format uses, where -1 is the axis minimum, 0 the
// default and 1 the maximum.
type Condition struct {
	Axis     int
	Min, Max float64

	// Format, when not zero, is written in place of 1 — the axis-range format
	// this otherwise emits. It exists so a fixture can state a condition a
	// reader cannot evaluate, which the specification's later formats and a
	// corrupt font both produce.
	Format int
}

// FeatureVariation is one FeatureVariationRecord: the conditions that must all
// hold, and the lookup list it puts in place of each named feature's own.
//
// A feature mapped to an empty list is not the same as one left out: it says the
// record gives that feature no lookups here, which silences it.
type FeatureVariation struct {
	Conditions []Condition
	Substitute map[int][]int

	// NoConditionSet writes a null condition-set offset, which the format says
	// matches everywhere.
	NoConditionSet bool
}

// GSUBTableVarying is GSUBTable with a FeatureVariations table, which makes the
// header version 1.1.
func GSUBTableVarying(lookups []Lookup, features []Feature, scripts map[string]Script, records []FeatureVariation) []byte {
	// The 1.0 scaffolding first, then the 32-bit offset the 1.1 header carries
	// and the table it points at.
	base := layoutTableFull(lookups, features, scripts)
	out := make([]byte, 0, len(base)+4)
	out = append(out, base[:10]...)
	out = append(out, 0, 0, 0, 0) // featureVariationsOffset, filled in below
	out = append(out, base[10:]...)
	binary.BigEndian.PutUint16(out[2:], 1) // minorVersion 1
	// Every offset in the 1.0 part is from the start of the table, so they all
	// move by the four bytes just inserted.
	for _, at := range []int{4, 6, 8} {
		binary.BigEndian.PutUint16(out[at:], binary.BigEndian.Uint16(out[at:])+4)
	}
	binary.BigEndian.PutUint32(out[10:], uint32(len(out)))
	return append(out, featureVariationsTable(records)...)
}

// featureVariationsTable encodes the table: a header, a record per variation,
// and each record's condition set and substitutions laid out after them.
func featureVariationsTable(records []FeatureVariation) []byte {
	body := make([]byte, 8+8*len(records))
	binary.BigEndian.PutUint16(body[0:], 1) // majorVersion
	binary.BigEndian.PutUint16(body[2:], 0) // minorVersion
	binary.BigEndian.PutUint32(body[4:], uint32(len(records)))
	for i, rec := range records {
		at := 8 + 8*i
		if !rec.NoConditionSet {
			binary.BigEndian.PutUint32(body[at:], uint32(len(body)))
			body = append(body, conditionSetTable(rec.Conditions)...)
		}
		binary.BigEndian.PutUint32(body[at+4:], uint32(len(body)))
		body = append(body, featureTableSubstitutionTable(rec.Substitute)...)
	}
	return body
}

func conditionSetTable(conds []Condition) []byte {
	out := make([]byte, 2+4*len(conds))
	binary.BigEndian.PutUint16(out[0:], uint16(len(conds)))
	for i, c := range conds {
		binary.BigEndian.PutUint32(out[2+4*i:], uint32(len(out)))
		cond := make([]byte, 8)
		format := c.Format
		if format == 0 {
			format = 1
		}
		binary.BigEndian.PutUint16(cond[0:], uint16(format))
		binary.BigEndian.PutUint16(cond[2:], uint16(c.Axis))
		binary.BigEndian.PutUint16(cond[4:], uint16(f2Dot14(c.Min)))
		binary.BigEndian.PutUint16(cond[6:], uint16(f2Dot14(c.Max)))
		out = append(out, cond...)
	}
	return out
}

func featureTableSubstitutionTable(subs map[int][]int) []byte {
	indices := make([]int, 0, len(subs))
	for i := range subs {
		indices = append(indices, i)
	}
	sortInts(indices)

	out := make([]byte, 6+6*len(indices))
	binary.BigEndian.PutUint16(out[0:], 1) // majorVersion
	binary.BigEndian.PutUint16(out[2:], 0) // minorVersion
	binary.BigEndian.PutUint16(out[4:], uint16(len(indices)))
	for i, index := range indices {
		rec := 6 + 6*i
		binary.BigEndian.PutUint16(out[rec:], uint16(index))
		binary.BigEndian.PutUint32(out[rec+2:], uint32(len(out)))
		// An alternate Feature table: a null featureParams offset, then the
		// lookup indices.
		lookups := subs[index]
		alt := make([]byte, 4+2*len(lookups))
		binary.BigEndian.PutUint16(alt[2:], uint16(len(lookups)))
		for j, l := range lookups {
			binary.BigEndian.PutUint16(alt[4+2*j:], uint16(l))
		}
		out = append(out, alt...)
	}
	return out
}

// f2Dot14 encodes a normalized coordinate in the format's fixed-point form: a
// signed value with fourteen fractional bits, so 1.0 is 0x4000.
func f2Dot14(v float64) int16 {
	n := int(v*16384 + 0.5)
	if v < 0 {
		n = int(v*16384 - 0.5)
	}
	if n > 0x7FFF {
		n = 0x7FFF
	}
	if n < -0x8000 {
		n = -0x8000
	}
	return int16(n)
}
