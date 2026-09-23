package core

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

func zlibOf(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func streamWith(data []byte, entries ...object.Entry) *object.Stream {
	return object.NewStream(object.NewDictionary(entries...), data)
}

// TestASCII85Decode: the filter every PDF/A part permits and Ghostscript writes
// in front of Flate ([/ASCII85Decode /FlateDecode]) was not implemented, so a
// content stream using it decoded to nothing and every content rule skipped it
// with no finding (audit 2026-09-22 C46).
func TestASCII85Decode(t *testing.T) {
	for _, plain := range [][]byte{
		nil, []byte("a"), []byte("ab"), []byte("abc"), []byte("abcd"), []byte("abcde"),
		[]byte("1 0 0 rg 0 0 10 10 re f"), {0, 0, 0, 0, 0, 0, 0, 0, 1}, bytes.Repeat([]byte{0xff}, 13),
	} {
		enc := make([]byte, ascii85.MaxEncodedLen(len(plain)))
		enc = append(enc[:ascii85.Encode(enc, plain)], '~', '>')
		// White space anywhere is ignored.
		spaced := bytes.Join(bytes.SplitAfter(enc, []byte("")), []byte(" \n"))
		for _, in := range [][]byte{enc, spaced} {
			got, err := ascii85Decode(in, 1<<20)
			if err != nil || !bytes.Equal(got, plain) {
				t.Errorf("ascii85Decode(%q) = (%q, %v), want %q", in, got, err, plain)
			}
		}
	}
	// 'z' is four zero bytes and may not appear inside a group.
	if got, err := ascii85Decode([]byte("z~>"), 100); err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Errorf("z: (%v, %v)", got, err)
	}
	for _, bad := range []string{"!z~>", "!~>", "v~>", "s8W-\"~>", "abc~x"} {
		if _, err := ascii85Decode([]byte(bad), 100); err == nil || errors.Is(err, ErrDecodeLimit) {
			t.Errorf("ascii85Decode(%q) = %v, want a malformed-data error", bad, err)
		}
	}
	// The cap holds against 'z' expansion (one byte of input, four of output).
	if _, err := ascii85Decode(bytes.Repeat([]byte("z"), 100), 399); !errors.Is(err, ErrDecodeLimit) {
		t.Errorf("400 bytes of z under a 399-byte cap: %v, want ErrDecodeLimit", err)
	}
}

// TestRunLengthDecode covers the three kinds of length byte, and that the
// 128-fold expansion is bounded before anything is allocated.
func TestRunLengthDecode(t *testing.T) {
	in := []byte{2, 'a', 'b', 'c', 255, 'x', 254, 'y', 128, 9, 9}
	want := []byte("abcxxyyy")
	if got, err := runLengthDecode(in, 100); err != nil || !bytes.Equal(got, want) {
		t.Errorf("runLengthDecode = (%q, %v), want %q", got, err, want)
	}
	// No end-of-data byte: the data still ends.
	if got, err := runLengthDecode([]byte{0, 'q'}, 100); err != nil || string(got) != "q" {
		t.Errorf("no EOD: (%q, %v)", got, err)
	}
	for _, bad := range [][]byte{{3, 'a'}, {200}} {
		if _, err := runLengthDecode(bad, 100); err == nil || errors.Is(err, ErrDecodeLimit) {
			t.Errorf("runLengthDecode(%v) = %v, want malformed", bad, err)
		}
	}
	// 2 bytes asking for 128: refused against the cap, not allocated.
	if _, err := runLengthDecode([]byte{129, 'x'}, 127); !errors.Is(err, ErrDecodeLimit) {
		t.Errorf("128-byte run under a 127-byte cap: %v, want ErrDecodeLimit", err)
	}
}

// TestRunLengthExpansionIsBoundedBeforeAllocating is the hostile form: a
// megabyte of "129 x" pairs asks for 64 MB; under a 1 MB cap the decode must
// fail without ever holding the output.
func TestRunLengthExpansionIsBoundedBeforeAllocating(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 64 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
		in := bytes.Repeat([]byte{129, 'x'}, 1<<19)
		if _, err := runLengthDecode(in, 1<<20); !errors.Is(err, ErrDecodeLimit) {
			t.Fatalf("err = %v, want ErrDecodeLimit", err)
		}
	})
}

// TestFlateTruncatedChecksumIsData: a deflate stream that reached its final
// block is the content whether its Adler-32 is there or not (FlateDecode). One
// cut short of the final block is malformed.
func TestFlateTruncatedChecksumIsData(t *testing.T) {
	plain := []byte("1 0 0 rg 0 0 10 10 re f")
	full := zlibOf(plain)
	for name, in := range map[string][]byte{
		"complete":         full,
		"no checksum":      full[:len(full)-4],
		"partial checksum": full[:len(full)-2],
		"wrong checksum":   append(append([]byte(nil), full[:len(full)-4]...), 1, 2, 3, 4),
	} {
		got, err := FlateDecode(Canceler{}, in, DefaultLimits())
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("%s: (%q, %v), want %q", name, got, err, plain)
		}
	}
	big := zlibOf(bytes.Repeat([]byte("0 0 1 1 re f\n"), 4096))
	if _, err := FlateDecode(Canceler{}, big[:len(big)/2], DefaultLimits()); err == nil || ReasonOf(err) != ReasonMalformed {
		t.Errorf("deflate cut before its final block: %v, want malformed", err)
	}
}

// TestDecodeCapHonoursMaxInt: a cap of math.MaxInt reads everything (the old
// int64(max)+1 wrapped negative and read nothing, audit 2026-09-22 C48).
func TestDecodeCapHonoursMaxInt(t *testing.T) {
	lim := DefaultLimits()
	lim.DecodedStreamBytes = math.MaxInt
	plain := []byte("BT (Hello) Tj ET")
	if got, err := FlateDecode(Canceler{}, zlibOf(plain), lim); err != nil || !bytes.Equal(got, plain) {
		t.Errorf("FlateDecode under MaxInt = (%q, %v)", got, err)
	}
	if got, err := readCapped(bytes.NewReader(plain), math.MaxInt, "t"); err != nil || !bytes.Equal(got, plain) {
		t.Errorf("readCapped under MaxInt = (%q, %v)", got, err)
	}
	if _, err := readCapped(bytes.NewReader(plain), len(plain)-1, "t"); !errors.Is(err, ErrDecodeLimit) {
		t.Errorf("readCapped one byte over: %v, want ErrDecodeLimit", err)
	}
}

// TestPNGPredictorAllocatesNothingForEmptyData: /Colors 64 /BitsPerComponent
// 16 /Columns 2^24 is a 2 GiB row, and empty data passed "0 mod (rowLen+1) ==
// 0" into make([]byte, rowLen). Eight such object streams in a 1.6 KB file
// were an OOM in Read (audit 2026-09-22 C8).
func TestPNGPredictorAllocatesNothingForEmptyData(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 64 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
		p := PredictorParms{Predictor: 12, Colors: 64, BitsPerComponent: 16, Columns: 1 << 24}
		for i := 0; i < 8; i++ {
			got, err := ApplyPredictor(nil, p)
			if err != nil || len(got) != 0 {
				t.Fatalf("empty data: (%d bytes, %v), want no rows and no error", len(got), err)
			}
		}
		// Data shorter than one row is malformed, and is refused before the
		// row buffer is made.
		if _, err := ApplyPredictor([]byte{0, 1, 2}, p); err == nil {
			t.Fatal("three bytes against a 2 GiB row were accepted")
		}
	})
}

// TestIndirectDecodeParmsAreResolved: an indirect /DecodeParms is legal, and
// was skipped — the predictor never ran and the content decoded to garbage,
// while the stream was reported as supported (audit 2026-09-22 C151).
func TestIndirectDecodeParmsAreResolved(t *testing.T) {
	// PNG "Up" rows: row 2 is stored as its difference from row 1.
	rows := []byte{2, 1, 2, 3, 2, 1, 1, 1}
	want := []byte{1, 2, 3, 2, 3, 4}
	parms := object.NewDictionary(object.Entry{Key: "Predictor", Value: object.IndirectRef{Number: 7}}, object.Entry{Key: "Columns", Value: object.Integer(3)})
	objs := map[int]*object.IndirectObject{
		5: {Number: 5, Value: parms},
		6: {Number: 6, Value: object.Name("FlateDecode")},
		7: {Number: 7, Value: object.Integer(12)},
	}
	v := View{Objects: objs, Limits: DefaultLimits()}
	for name, st := range map[string]*object.Stream{
		"indirect dictionary": streamWith(zlibOf(rows), object.Entry{Key: "Filter", Value: object.Name("FlateDecode")}, object.Entry{Key: "DecodeParms", Value: object.IndirectRef{Number: 5}}),
		"indirect filter and array element": streamWith(zlibOf(rows),
			object.Entry{Key: "Filter", Value: object.Array{object.IndirectRef{Number: 6}}},
			object.Entry{Key: "DecodeParms", Value: object.Array{object.IndirectRef{Number: 5}}}),
	} {
		got, r := v.Decode(st)
		if r != ReasonOK || !bytes.Equal(got, want) {
			t.Errorf("%s: Decode = (%v, %v), want (%v, ok)", name, got, r, want)
		}
		// With nothing to resolve through, the reference is an error, never a
		// silently skipped predictor.
		if got, err := DecodeStreamData(Canceler{}, st, DefaultLimits(), nil); err == nil {
			t.Errorf("%s: decoded to %v with no resolver; want an error", name, got)
		}
	}
}

// TestFilterSupportAgreesWithDecode: the one table FilterSupported reads is the
// one ApplyFilter dispatches on, so a stage it accepts never fails as
// unsupported and one it refuses is never attempted.
func TestFilterSupportAgreesWithDecode(t *testing.T) {
	sub := object.NewDictionary(object.Entry{Key: "Predictor", Value: object.Integer(2)}, object.Entry{Key: "BitsPerComponent", Value: object.Integer(4)})
	for _, c := range []struct {
		name  object.Name
		parms *object.Dictionary
	}{
		{"FlateDecode", nil}, {"LZWDecode", nil}, {"ASCIIHexDecode", nil}, {"ASCII85Decode", nil},
		{"RunLengthDecode", nil}, {"Crypt", nil}, {"DCTDecode", nil}, {"JPXDecode", nil},
		{"CCITTFaxDecode", nil}, {"JBIG2Decode", nil}, {"Bogus", nil}, {"FlateDecode", sub}, {"LZWDecode", sub},
	} {
		supported := FilterSupported(c.name, c.parms) == nil
		_, err := ApplyFilter(Canceler{}, c.name, []byte{}, c.parms, DefaultLimits())
		if unsupported := errors.Is(err, ErrUnsupportedFilter); unsupported == supported {
			t.Errorf("%s (parms %v): FilterSupported says %v, ApplyFilter says unsupported=%v (%v)", c.name, c.parms != nil, supported, unsupported, err)
		}
	}
}
