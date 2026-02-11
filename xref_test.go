package pdf0

import (
	"testing"
)

func TestParseXRefTable(t *testing.T) {
	// Standard xref table format
	xrefData := "0 4\r\n" +
		"0000000000 65535 f \r\n" +
		"0000000009 00000 n \r\n" +
		"0000000074 00000 n \r\n" +
		"0000000120 00000 n \r\n" +
		"trailer\n"

	table, err := ParseXRefTable([]byte(xrefData), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(table.Entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(table.Entries))
	}

	// Object 0 should be free
	if !table.Entries[0].Free {
		t.Error("object 0 should be free")
	}
	if table.Entries[0].Generation != 65535 {
		t.Errorf("object 0 generation: expected 65535, got %d", table.Entries[0].Generation)
	}

	// Object 1
	if table.Entries[1].Free {
		t.Error("object 1 should not be free")
	}
	if table.Entries[1].Offset != 9 {
		t.Errorf("object 1 offset: expected 9, got %d", table.Entries[1].Offset)
	}

	// Object 2
	if table.Entries[2].Offset != 74 {
		t.Errorf("object 2 offset: expected 74, got %d", table.Entries[2].Offset)
	}

	// Object 3
	if table.Entries[3].Offset != 120 {
		t.Errorf("object 3 offset: expected 120, got %d", table.Entries[3].Offset)
	}
}

func TestParseXRefTableMultipleSubsections(t *testing.T) {
	xrefData := "0 2\r\n" +
		"0000000000 65535 f \r\n" +
		"0000000009 00000 n \r\n" +
		"5 1\r\n" +
		"0000000200 00000 n \r\n" +
		"trailer\n"

	table, err := ParseXRefTable([]byte(xrefData), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(table.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(table.Entries))
	}

	if table.Entries[1].Offset != 9 {
		t.Errorf("object 1 offset: expected 9, got %d", table.Entries[1].Offset)
	}
	if table.Entries[5].Offset != 200 {
		t.Errorf("object 5 offset: expected 200, got %d", table.Entries[5].Offset)
	}
}

func TestSplitFields(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"0 4", []string{"0", "4"}},
		{"  0  4  ", []string{"0", "4"}},
		{"hello world", []string{"hello", "world"}},
		{"", nil},
		{"   ", nil},
	}
	for _, tt := range tests {
		got := splitFields(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitFields(%q): expected %v, got %v", tt.input, tt.want, got)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitFields(%q)[%d]: expected %q, got %q", tt.input, i, tt.want[i], got[i])
			}
		}
	}
}
