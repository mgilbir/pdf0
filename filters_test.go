package pdf0

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// pngEncode applies a PNG filter forward so tests can verify the reversal.
func pngFilterRows(rows [][]byte, filterType byte, bpp int) []byte {
	var out []byte
	prev := make([]byte, len(rows[0]))
	for _, row := range rows {
		out = append(out, filterType)
		filtered := make([]byte, len(row))
		for i := range row {
			var left, up, upLeft byte
			if i >= bpp {
				left = row[i-bpp]
				upLeft = prev[i-bpp]
			}
			up = prev[i]
			switch filterType {
			case 0:
				filtered[i] = row[i]
			case 1:
				filtered[i] = row[i] - left
			case 2:
				filtered[i] = row[i] - up
			case 3:
				filtered[i] = row[i] - byte((int(left)+int(up))/2)
			case 4:
				filtered[i] = row[i] - paeth(left, up, upLeft)
			}
		}
		out = append(out, filtered...)
		prev = row
	}
	return out
}

func TestPNGPredictorRoundTrip(t *testing.T) {
	rows := [][]byte{
		{0x01, 0x00, 0x10, 0x01, 0x02, 0x20},
		{0x01, 0x00, 0x35, 0x01, 0x88, 0xFF},
		{0x02, 0x10, 0x00, 0x00, 0x00, 0x42},
	}
	var want []byte
	for _, r := range rows {
		want = append(want, r...)
	}

	for ft := byte(0); ft <= 4; ft++ {
		encoded := pngFilterRows(rows, ft, 1)
		got, err := applyPNGPredictor(encoded, predictorParms{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: 6})
		if err != nil {
			t.Fatalf("filter type %d: %v", ft, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("filter type %d: got %x, want %x", ft, got, want)
		}
	}
}

func TestPNGPredictorMultiBytePixel(t *testing.T) {
	// 2 columns, 3 colors, 8 bpc => bpp 3, row length 6
	rows := [][]byte{
		{10, 20, 30, 40, 50, 60},
		{15, 25, 35, 45, 55, 65},
	}
	var want []byte
	for _, r := range rows {
		want = append(want, r...)
	}
	encoded := pngFilterRows(rows, 4, 3)
	got, err := applyPNGPredictor(encoded, predictorParms{Predictor: 15, Colors: 3, BitsPerComponent: 8, Columns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPNGPredictorTruncated(t *testing.T) {
	_, err := applyPNGPredictor([]byte{2, 0, 0}, predictorParms{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: 6})
	if err == nil {
		t.Error("expected error for truncated data")
	}
}

func TestPNGPredictorInvalidFilterType(t *testing.T) {
	_, err := applyPNGPredictor([]byte{9, 0, 0}, predictorParms{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: 2})
	if err == nil {
		t.Error("expected error for invalid per-row filter type")
	}
}

func TestTIFFPredictor(t *testing.T) {
	// 1 color, 8 bpc, 4 columns: horizontal differences
	// row: 10, +5, +3, -2  => 10, 15, 18, 16
	data := []byte{10, 5, 3, 0xFE}
	got, err := applyTIFFPredictor(data, predictorParms{Predictor: 2, Colors: 1, BitsPerComponent: 8, Columns: 4})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 15, 18, 16}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTIFFPredictorSubByteUnsupported(t *testing.T) {
	_, err := applyTIFFPredictor([]byte{0}, predictorParms{Predictor: 2, Colors: 1, BitsPerComponent: 4, Columns: 2})
	if err == nil {
		t.Error("expected error for sub-byte TIFF predictor")
	}
}

func TestApplyPredictorRejectsUnknown(t *testing.T) {
	_, err := applyPredictor(nil, predictorParms{Predictor: 3, Colors: 1, BitsPerComponent: 8, Columns: 1})
	if err == nil {
		t.Error("expected error for unknown predictor")
	}
}

// TestParseXRefStreamWithPredictor exercises the real-world shape: a
// FlateDecode xref stream with /DecodeParms /Predictor 12 (PNG Up).
func TestParseXRefStreamWithPredictor(t *testing.T) {
	// Three entries, W [1 2 1]: rows of 4 bytes.
	entries := [][]byte{
		{0, 0x00, 0x00, 0xFF}, // obj 0: free
		{1, 0x00, 0x0F, 0x00}, // obj 1: offset 15
		{1, 0x01, 0x00, 0x00}, // obj 2: offset 256
	}
	// PNG Up filter, forward direction.
	var raw []byte
	prev := make([]byte, 4)
	for _, row := range entries {
		raw = append(raw, 2) // Up
		for i := range row {
			raw = append(raw, row[i]-prev[i])
		}
		prev = row
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()

	dict := Dictionary{}
	dict.Set("Type", Name("XRef"))
	dict.Set("Size", Integer(3))
	dict.Set("W", Array{Integer(1), Integer(2), Integer(1)})
	dict.Set("Filter", Name("FlateDecode"))
	parms := &Dictionary{}
	parms.Set("Predictor", Integer(12))
	parms.Set("Columns", Integer(4))
	dict.Set("DecodeParms", parms)

	table, err := ParseXRefStream(&Stream{Dict: dict, Data: buf.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	if !table.Entries[0].Free {
		t.Error("entry 0 should be free")
	}
	if table.Entries[1].Offset != 15 {
		t.Errorf("entry 1 offset: got %d, want 15", table.Entries[1].Offset)
	}
	if table.Entries[2].Offset != 256 {
		t.Errorf("entry 2 offset: got %d, want 256", table.Entries[2].Offset)
	}
}
