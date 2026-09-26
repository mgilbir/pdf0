package core

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// viewOf is a run's view of objs: every producer records into rec.
func viewOf(objs map[int]*object.IndirectObject, lim Limits) (View, *Recorder) {
	rec := &Recorder{}
	return View{Objects: objs, Limits: lim, Run: NewRun(rec)}, rec
}

// TestProducersRecordTheirTrips is the producer half of the T2 contract: a
// declined outcome is recorded by the producer that met it, once per
// (object, reason), however many consumers ask; a malformed one is not — it is
// a fact about the file, for the rules about malformation to report.
func TestProducersRecordTheirTrips(t *testing.T) {
	big := zlibOf(bytes.Repeat([]byte("0 0 1 1 re f\n"), 1000)) // 13 000 bytes decoded
	cases := []struct {
		name   string
		st     *object.Stream
		lim    func(*Limits)
		locked bool
		want   Reason
		guard  string // "" when no trip may be recorded
	}{
		{"ok", streamWith(zlibOf([]byte("q Q")), object.Entry{Key: "Filter", Value: object.Name("FlateDecode")}), nil, false, ReasonOK, ""},
		{"decode limit", streamWith(big, object.Entry{Key: "Filter", Value: object.Name("FlateDecode")}), func(l *Limits) { l.DecodedStreamBytes = 1000 }, false, ReasonLimit, GuardDecodedStream},
		{"scanning limit", streamWith(big, object.Entry{Key: "Filter", Value: object.Name("FlateDecode")}), func(l *Limits) { l.ContentStreamBytes = 1000 }, false, ReasonLimit, GuardContentStream},
		{"unfiltered over the decode limit", streamWith(bytes.Repeat([]byte{' '}, 2000)), func(l *Limits) { l.DecodedStreamBytes = 1000 }, false, ReasonLimit, GuardDecodedStream},
		{"unsupported", streamWith([]byte("x"), object.Entry{Key: "Filter", Value: object.Name("JBIG2Decode")}), nil, false, ReasonUnsupported, GuardUnsupportedFilter},
		{"locked", streamWith([]byte("q Q")), nil, true, ReasonLocked, GuardLocked},
		{"malformed", streamWith([]byte("not zlib"), object.Entry{Key: "Filter", Value: object.Name("FlateDecode")}), nil, false, ReasonMalformed, ""},
	}
	for _, c := range cases {
		lim := DefaultLimits()
		if c.lim != nil {
			c.lim(&lim)
		}
		v, rec := viewOf(map[int]*object.IndirectObject{9: {Number: 9, Value: c.st}}, lim)
		v.Locked = c.locked
		// Three consumers of the same stream.
		_, r1 := v.Content(c.st)
		_, r2 := v.MetadataContent(c.st)
		_, r3 := v.DecodeLimited(c.st)
		if r1 != c.want || r2 != c.want || r3 != c.want {
			t.Errorf("%s: reasons %v, %v, %v; want %v", c.name, r1, r2, r3, c.want)
		}
		trips := rec.Snapshot()
		switch {
		case c.guard == "" && len(trips) != 0:
			t.Errorf("%s: %d trips recorded, want none: %v", c.name, len(trips), trips)
		case c.guard != "" && len(trips) != 1:
			t.Errorf("%s: %d trips recorded, want exactly one: %v", c.name, len(trips), trips)
		case c.guard != "" && (trips[0].Guard() != c.guard || trips[0].Obj != 9):
			t.Errorf("%s: trip %s on object %d, want %s on object 9", c.name, trips[0].Guard(), trips[0].Obj, c.guard)
		}
	}
}

// TestOneTripPerObjectAndReason: two producers that decline the same stream
// for the same reason under different bounds report it once. The recorder
// alone would keep both, since their details differ.
func TestOneTripPerObjectAndReason(t *testing.T) {
	st := streamWith(bytes.Repeat([]byte{1}, 400), object.Entry{Key: "N", Value: object.Integer(3)})
	lim := DefaultLimits()
	lim.ContentStreamBytes, lim.ICCProfileBytes = 100, 200
	v, rec := viewOf(map[int]*object.IndirectObject{4: {Number: 4, Value: st}}, lim)
	if _, r := v.Content(st); r != ReasonLimit {
		t.Fatalf("Content: %v, want limit", r)
	}
	if _, r := v.ICCProfileData(st); r != ReasonLimit {
		t.Fatalf("ICCProfileData: %v, want limit", r)
	}
	if trips := rec.Snapshot(); len(trips) != 1 {
		t.Errorf("%d trips for one object and one reason, want 1: %v", len(trips), trips)
	}
}

// TestICCProfileLimitIsRecorded: a caller's lowered ICC bound used to remove
// every ICC rule with no finding at all (audit 2026-09-22 C109).
func TestICCProfileLimitIsRecorded(t *testing.T) {
	prof := streamWith(bytes.Repeat([]byte{1}, 400), object.Entry{Key: "N", Value: object.Integer(3)})
	lim := DefaultLimits()
	lim.ICCProfileBytes = 128
	v, rec := viewOf(map[int]*object.IndirectObject{3: {Number: 3, Value: prof}}, lim)
	if data, r := v.ICCProfileData(prof); data != nil || r != ReasonLimit {
		t.Fatalf("ICCProfileData over the bound = (%d bytes, %v), want (nil, limit)", len(data), r)
	}
	trips := rec.Snapshot()
	if len(trips) != 1 || trips[0].Guard() != GuardICCProfile || !strings.Contains(trips[0].Message(), "128 (configured by the caller)") {
		t.Errorf("trips = %v, want one %s naming the caller's bound", trips, GuardICCProfile)
	}
}

// TestStringValueDeclinesCiphertext: in a Locked document a string is present
// and unreadable (audit 2026-09-22 C63).
func TestStringValueDeclinesCiphertext(t *testing.T) {
	objs := map[int]*object.IndirectObject{1: {Number: 1, Value: object.String{Value: []byte("\x8f\x02")}}}
	v := View{Objects: objs}
	if _, r := v.StringValue(object.IndirectRef{Number: 1}); r != ReasonOK {
		t.Errorf("unlocked: %v, want ok", r)
	}
	v.Locked = true
	if _, r := v.StringValue(object.IndirectRef{Number: 1}); r != ReasonLocked {
		t.Errorf("locked: %v, want locked", r)
	}
	if _, r := v.StringValue(object.Name("x")); r != ReasonAbsent {
		t.Errorf("a name: %v, want absent", r)
	}
	if !v.NonEmptyStringOrLocked(object.IndirectRef{Number: 1}) {
		t.Error("a Locked string was judged missing")
	}
}

// TestMetadataInTheClearIsReadInALockedDocument: /EncryptMetadata false leaves
// the catalog's metadata stream in plaintext, and it is read.
func TestMetadataInTheClearIsReadInALockedDocument(t *testing.T) {
	meta := streamWith([]byte("<x/>"))
	cat := object.NewDictionary(object.Entry{Key: "Metadata", Value: object.IndirectRef{Number: 2}})
	enc := object.NewDictionary(object.Entry{Key: "EncryptMetadata", Value: object.Boolean(false)})
	trailer := object.NewDictionary(object.Entry{Key: "Root", Value: object.IndirectRef{Number: 1}}, object.Entry{Key: "Encrypt", Value: enc})
	v, _ := viewOf(map[int]*object.IndirectObject{1: {Number: 1, Value: cat}, 2: {Number: 2, Value: meta}}, DefaultLimits())
	v.Trailer, v.Locked = trailer, true
	if data, r := v.MetadataContent(meta); r != ReasonOK || string(data) != "<x/>" {
		t.Errorf("metadata in the clear: (%q, %v), want (<x/>, ok)", data, r)
	}
	enc.Set("EncryptMetadata", object.Boolean(true))
	other := streamWith([]byte("<y/>"))
	if _, r := v.Decode(other); r != ReasonLocked {
		t.Errorf("another stream of a Locked document: %v, want locked", r)
	}
}
