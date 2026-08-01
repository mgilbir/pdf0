package core

import (
	"bytes"
	"testing"
)

// Predictor tests, moved here with the filters they exercise.

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
		got, err := applyPNGPredictor(encoded, PredictorParms{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: 6})
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
	got, err := applyPNGPredictor(encoded, PredictorParms{Predictor: 15, Colors: 3, BitsPerComponent: 8, Columns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPNGPredictorTruncated(t *testing.T) {
	_, err := applyPNGPredictor([]byte{2, 0, 0}, PredictorParms{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: 6})
	if err == nil {
		t.Error("expected error for truncated data")
	}
}

func TestPNGPredictorInvalidFilterType(t *testing.T) {
	_, err := applyPNGPredictor([]byte{9, 0, 0}, PredictorParms{Predictor: 12, Colors: 1, BitsPerComponent: 8, Columns: 2})
	if err == nil {
		t.Error("expected error for invalid per-row filter type")
	}
}

func TestTIFFPredictor(t *testing.T) {
	// 1 color, 8 bpc, 4 columns: horizontal differences
	// row: 10, +5, +3, -2  => 10, 15, 18, 16
	data := []byte{10, 5, 3, 0xFE}
	got, err := applyTIFFPredictor(data, PredictorParms{Predictor: 2, Colors: 1, BitsPerComponent: 8, Columns: 4})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 15, 18, 16}
	if !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTIFFPredictorSubByteUnsupported(t *testing.T) {
	_, err := applyTIFFPredictor([]byte{0}, PredictorParms{Predictor: 2, Colors: 1, BitsPerComponent: 4, Columns: 2})
	if err == nil {
		t.Error("expected error for sub-byte TIFF predictor")
	}
}

func TestApplyPredictorRejectsUnknown(t *testing.T) {
	_, err := ApplyPredictor(nil, PredictorParms{Predictor: 3, Colors: 1, BitsPerComponent: 8, Columns: 1})
	if err == nil {
		t.Error("expected error for unknown predictor")
	}
}

// TestParseXRefStreamWithPredictor exercises the real-world shape: a
// FlateDecode xref stream with /DecodeParms /Predictor 12 (PNG Up).
