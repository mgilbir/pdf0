package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/core"
	"strings"
	"testing"
)

// TestObjNumForDictDelegatesToDictObjNum pins the C34 consolidation: the two
// reverse-dictionary-lookup helpers agree, with objNumForDict keeping its
// 0-on-miss convention (for ValidationError.Object) while dictObjNum reports -1.
func TestObjNumForDictParity(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	font := &Dictionary{}
	font.Set("Type", Name("Font"))
	doc.Objects[7] = &IndirectObject{Number: 7, Value: font}

	if got := objNumForDict(doc, font); got != 7 {
		t.Fatalf("objNumForDict(font) = %d, want 7", got)
	}
	if got := doc.view().DictObjNum(font); got != 7 {
		t.Fatalf("dictObjNum(font) = %d, want 7", got)
	}

	orphan := &Dictionary{} // never installed as an indirect object
	if got := objNumForDict(doc, orphan); got != 0 {
		t.Fatalf("objNumForDict(orphan) = %d, want 0 (unknown-object sentinel)", got)
	}
	if got := doc.view().DictObjNum(orphan); got != -1 {
		t.Fatalf("dictObjNum(orphan) = %d, want -1", got)
	}
}

// TestSkipContentInlineImageHonorsLength pins the C35 unification: the text
// extractor's inline-image skipper now honors a declared /L, so binary sample
// data containing a whitespace-delimited "EI" does not truncate the image early.
func TestSkipContentInlineImageHonorsLength(t *testing.T) {
	// BI ... /L 5 ID <5 bytes: 'a',' ','E','I',' '> EI Q
	// The bytes at offsets 2..3 form a whitespace-delimited "EI" INSIDE the
	// declared 5-byte sample data; the real terminator is the "EI" that follows.
	var b bytes.Buffer
	b.WriteString("BI /W 8 /H 1 /BPC 8 /L 5 ID ")
	b.Write([]byte{'a', ' ', 'E', 'I', ' '}) // 5 bytes of "sample data"
	b.WriteString("EI Q")
	data := b.Bytes()

	// Called with i just past the "BI" operator, matching the tokenizer.
	end := core.SkipContentInlineImage(data, 2)

	rest := strings.TrimSpace(string(data[end:]))
	if rest != "Q" {
		t.Fatalf("skip ended at %d leaving %q; a /L-honoring skip must stop at the real EI leaving %q "+
			"(a whitespace-EI search would stop early inside the sample data)", end, rest, "Q")
	}
}
