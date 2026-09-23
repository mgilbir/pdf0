package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// Typed producer outcomes (audit 2026-09-22 T2).
//
// A producer — anything that turns a stream in the file into something a check
// reads: decoded bytes, a font program, a CMap, a CIDSet, an ICC profile — used
// to answer failure with nil. The same nil then meant "this file's data is
// broken" and "pdf0 declined to look": a size limit, a filter it does not
// implement, content it could not decrypt. Its ~100 consumers each had to
// guess which, and guessed both ways. Those that skipped on nil made a verdict
// look clean when nothing had been checked (C46, C63, C109); those that
// asserted on nil reported a violation the file does not have (C47).
//
// So a producer returns its data together with a Reason, and the Reason
// carries the contract:
//
//   - ReasonOK: the data is what the file says.
//   - ReasonAbsent: there is nothing to produce — the entry is missing.
//   - ReasonMalformed: the file's data is not what it claims to be. A rule
//     about malformation may assert on this; nothing else may treat it as
//     "empty".
//   - ReasonUnsupported, ReasonLimit, ReasonLocked, ReasonCanceled: pdf0 did
//     not look (Declined). A consumer must decline too, and never assert.
//
// And the producer, not the consumer, records the trip: every declined reason
// except cancellation is noted on the run's recorder by the producer that met
// it, once per (object, reason), so it reaches the report as a "limit" finding
// whether or not the consumer remembers. Cancellation is reported once for the
// whole run by runLimitTrips, from the context itself.
type Reason uint8

const (
	ReasonOK Reason = iota
	ReasonAbsent
	ReasonMalformed
	ReasonUnsupported
	ReasonLimit
	ReasonLocked
	ReasonCanceled
)

func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonAbsent:
		return "absent"
	case ReasonMalformed:
		return "malformed"
	case ReasonUnsupported:
		return "unsupported"
	case ReasonLimit:
		return "limit"
	case ReasonLocked:
		return "locked"
	case ReasonCanceled:
		return "canceled"
	}
	return fmt.Sprintf("Reason(%d)", int(r))
}

// Declined reports whether pdf0 did not produce the data by its own choice or
// inability — a budget, an unimplemented encoding, content it could not
// decrypt, a cancelled operation — as opposed to the file being at fault. A
// consumer that sees a declined reason must not assert anything on the
// strength of the missing data.
func (r Reason) Declined() bool { return r >= ReasonUnsupported }

// Worse returns whichever of r and o a combined result must report. A result
// built from several parts is only as good as its worst part, and a declined
// part outranks a malformed one: a consumer may assert on malformed data, and
// must not when any of it was declined.
func (r Reason) Worse(o Reason) Reason {
	if o > r {
		return o
	}
	return r
}

// ReasonOf classifies a decode error. nil is ReasonOK.
func ReasonOf(err error) Reason {
	switch {
	case err == nil:
		return ReasonOK
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ReasonCanceled
	case errors.Is(err, ErrDecodeLimit):
		return ReasonLimit
	case errors.Is(err, ErrUnsupportedFilter):
		return ReasonUnsupported
	}
	return ReasonMalformed
}

// SkippedObjStm is an object-stream container Read did not unpack by request,
// and why (a declined Reason).
type SkippedObjStm struct {
	Num    int
	Reason Reason
}

// GuardLocked is not a resource guard: the data is ciphertext pdf0 could not
// decrypt — the document is Locked, or this object failed to decrypt under a
// known-good key. It is reported through the recorder for the reason
// GuardUnsupportedFilter is: a check did not run.
const GuardLocked = "not-decrypted" // no bound; see Document.Locked and DecryptFailures

// GuardICCProfile is Limits.ICCProfileBytes, WithMaxICCProfileBytes.
const GuardICCProfile = "icc-profile-size"

// GuardCMapSize is the fixed bound on how many code ranges, and how wide, an
// embedded CMap may declare (maxCMapEntries, maxCMapRangeSpan). It has no
// knob: it bounds a structure no real CMap comes near.
const GuardCMapSize = "cmap-size"

// outcomeMemoKey keys the run's record of which (stream, reason) pairs a
// producer has already reported, so that a stream met by several producers —
// content, a font program, a CMap — is reported once.
type outcomeMemoKey struct{}

type outcomeKey struct {
	source any // the *object.Stream or *object.Dictionary the outcome is about
	reason Reason
}

// noteDeclined records a producer's declined outcome for stream: guard names
// the bound or cause, detail says what was left undone. See noteDeclinedFor.
func (v View) noteDeclined(stream *object.Stream, r Reason, guard, detail string) {
	if !r.Declined() || r == ReasonCanceled || v.Run == nil {
		return
	}
	v.noteDeclinedFor(stream, v.StreamObjNum(stream), r, guard, detail)
}

// noteDeclinedFor records a producer's declined outcome for source (a stream
// or a dictionary; obj is its object number, or 0 or less when it has none).
// It is a no-op for every reason that is not declined, for cancellation
// (reported once per run from the context itself), outside a run, and for a
// (source, reason) pair already reported — which is what makes the trip once
// per object and reason however many producers and consumers meet it.
func (v View) noteDeclinedFor(source any, obj int, r Reason, guard, detail string) {
	if !r.Declined() || r == ReasonCanceled || v.Run == nil {
		return
	}
	seen := Slot[map[outcomeKey]bool](v.Run, outcomeMemoKey{})
	if *seen == nil {
		*seen = map[outcomeKey]bool{}
	}
	k := outcomeKey{source, r}
	if (*seen)[k] {
		return
	}
	(*seen)[k] = true
	if obj < 0 {
		obj = 0
	}
	v.Note(guard, detail, obj)
}

// streamNumMemoKey keys the run's reverse index from a stream to the object
// that holds it.
type streamNumMemoKey struct{}

// StreamObjNum returns the lowest object number whose value is stream, or 0
// when the stream is not an indirect object's value (a stream is always
// indirect in a well-formed file, but a hand-built View need not be). Within a
// run the reverse index is built once.
func (v View) StreamObjNum(stream *object.Stream) int {
	if v.Run == nil {
		best := 0
		for num, iobj := range v.Objects {
			if iobj.Value == object.Object(stream) && (best == 0 || num < best) {
				best = num
			}
		}
		return best
	}
	idx := Slot[map[*object.Stream]int](v.Run, streamNumMemoKey{})
	if *idx == nil {
		*idx = make(map[*object.Stream]int)
		for num, iobj := range v.Objects {
			if s, ok := iobj.Value.(*object.Stream); ok {
				if prev, dup := (*idx)[s]; !dup || num < prev {
					(*idx)[s] = num
				}
			}
		}
	}
	return (*idx)[stream]
}

// decryptFailedMemoKey keys the run's set of streams that failed to decrypt.
type decryptFailedMemoKey struct{}

// isCiphertext reports whether stream's bytes are ciphertext pdf0 could not
// decrypt: the document is Locked (every stream is, except a metadata stream
// the file leaves in the clear), or the stream is one of DecryptFailures.
func (v View) isCiphertext(stream *object.Stream) bool {
	if v.Locked {
		return !v.metadataInClear(stream)
	}
	if len(v.DecryptFailures) == 0 {
		return false
	}
	failed := Slot[map[*object.Stream]bool](v.Run, decryptFailedMemoKey{})
	if *failed == nil {
		*failed = map[*object.Stream]bool{}
		for _, num := range v.DecryptFailures {
			if iobj, ok := v.Objects[num]; ok {
				if s, ok := iobj.Value.(*object.Stream); ok {
					(*failed)[s] = true
				}
			}
		}
	}
	return (*failed)[stream]
}

// metadataInClear reports whether stream is the catalog's metadata stream in
// a file whose /Encrypt says metadata is not encrypted (/EncryptMetadata false,
// ISO 32000-2 Table 27): its bytes are plaintext even when nothing else could
// be decrypted.
func (v View) metadataInClear(stream *object.Stream) bool {
	if v.Trailer == nil {
		return false
	}
	enc := v.ResolveDict(v.Trailer.Get("Encrypt"))
	if enc == nil {
		return false
	}
	if b, ok := v.ResolveBool(enc.Get("EncryptMetadata")); !ok || bool(b) {
		return false
	}
	cat := v.Catalog()
	if cat == nil {
		return false
	}
	s, _ := v.Resolve(cat.Get("Metadata")).(*object.Stream)
	return s == stream
}

// StringValue resolves obj to a string, the producer every string a check
// reads goes through. ReasonAbsent means obj is not a string (or nothing);
// ReasonLocked means it is one, in a document that is encrypted and was not
// decrypted (View.Locked), so its bytes are ciphertext: its value, its length
// and whether it is empty mean nothing, and a check must decline rather than
// compare it (audit 2026-09-22 C63 — PDF/UA reported an invalid /Lang for
// every Locked file, from the ciphertext of a valid one). Whether the string
// is there at all is still known, and a check that only asks that may act on
// ReasonLocked as on ReasonOK.
//
// The run's report says the document was not decrypted, once, up front; this
// records nothing per string.
func (v View) StringValue(obj object.Object) (object.String, Reason) {
	s, ok := v.Resolve(obj).(object.String)
	switch {
	case !ok:
		return object.String{}, ReasonAbsent
	case v.Locked:
		return s, ReasonLocked
	}
	return s, ReasonOK
}

// NonEmptyStringOrLocked reports whether obj is a non-empty string, or a string
// of a Locked document, whose emptiness is unknown (AES encrypts an empty
// string to sixteen bytes and more). It is the reading for a rule that asserts
// a string is missing or empty, which must not fire on ciphertext.
func (v View) NonEmptyStringOrLocked(obj object.Object) bool {
	s, r := v.StringValue(obj)
	return r == ReasonLocked || (r == ReasonOK && len(s.Value) > 0)
}

// TextString is StringValue decoded as a PDF text string (DecodePDFTextString).
func (v View) TextString(obj object.Object) (string, Reason) {
	s, r := v.StringValue(obj)
	if r != ReasonOK {
		return "", r
	}
	return DecodePDFTextString(s.Value), r
}

// Decode returns a stream's decoded bytes through its filter chain, capped at
// Limits.DecodedStreamBytes, and why they are what they are. It is the
// producer every other one decodes through, so it is where a declined decode is
// recorded (see Reason): the caller does not need to note anything.
//
// It is not memoized and charges no content budget; Content is the one to use
// for content-like streams a run may meet many times.
func (v View) Decode(stream *object.Stream) ([]byte, Reason) {
	return v.decodeCapped(stream, v.Limits.DecodedStreamBytes, GuardDecodedStream, DefaultMaxDecodedStreamBytes, "decoded-stream")
}

// DecodeStages is Decode for a stream whose last stages are not reversed here
// — the image codecs (CCITTFaxDecode, JBIG2Decode) that follow a
// general-purpose filter. It applies the first n stages of the filter chain and
// returns their output, and every stage's decode parameters, resolved (parms[i]
// is stage i's). The Reason, and the trips it records, are Decode's.
func (v View) DecodeStages(stream *object.Stream, n int) (data []byte, parms []*object.Dictionary, r Reason) {
	if v.isCiphertext(stream) {
		v.noteDeclined(stream, ReasonLocked, GuardLocked, "a stream's data is ciphertext pdf0 could not decrypt; the checks that read it were skipped")
		return nil, nil, ReasonLocked
	}
	chain, err := FilterChain(stream, v.Resolve)
	if err != nil {
		return nil, nil, ReasonMalformed
	}
	for _, step := range chain {
		parms = append(parms, step.Parms)
	}
	data = stream.Data
	for i := 0; i < n && i < len(chain); i++ {
		if data, err = ApplyFilter(v.Cancel, chain[i].Name, data, chain[i].Parms, v.Limits); err != nil {
			break
		}
	}
	switch r = ReasonOf(err); r {
	case ReasonLimit:
		v.noteDeclined(stream, r, GuardDecodedStream, "a stream decodes to more than the "+LimitBound(int64(v.Limits.DecodedStreamBytes), DefaultMaxDecodedStreamBytes)+"-byte limit, so it was not decoded and the checks that read it were skipped")
	case ReasonUnsupported:
		v.noteDeclined(stream, r, GuardUnsupportedFilter, "a stream is encoded in a way pdf0 does not implement ("+err.Error()+"), so the checks that read it were skipped")
	}
	if r != ReasonOK {
		return nil, parms, r
	}
	return data, parms, r
}

// decodeCapped is Decode with the output held to limit bytes, reported under
// guard (whose default is def) when it trips. what names the kind of stream in
// the trip's detail.
func (v View) decodeCapped(stream *object.Stream, limit int, guard string, def int64, what string) ([]byte, Reason) {
	if v.isCiphertext(stream) {
		v.noteDeclined(stream, ReasonLocked, GuardLocked, "a stream's data is ciphertext pdf0 could not decrypt; the checks that read it were skipped")
		return nil, ReasonLocked
	}
	lim := v.Limits
	lim.DecodedStreamBytes = limit
	data, err := DecodeStreamData(v.Cancel, stream, lim, v.Resolve)
	r := ReasonOf(err)
	// An unfiltered stream is not decoded at all, so the cap is applied here to
	// the bytes as they are: a bound that only held for compressed data would
	// not be a bound.
	if r == ReasonOK && len(data) > limit {
		r = ReasonLimit
	}
	if r != ReasonOK {
		data = nil
	}
	switch r {
	case ReasonLimit:
		v.noteDeclined(stream, r, guard, "a "+what+" stream decodes to more than the "+LimitBound(int64(limit), def)+"-byte limit, so it was not decoded and the checks that read it were skipped")
	case ReasonUnsupported:
		v.noteDeclined(stream, r, GuardUnsupportedFilter, "a stream is encoded in a way pdf0 does not implement ("+err.Error()+"), so the checks that read it were skipped")
	}
	return data, r
}
