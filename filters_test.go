package pdf0

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// pngEncode applies a PNG filter forward so tests can verify the reversal.
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
