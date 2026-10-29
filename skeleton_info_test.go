package pdf0

import (
	"bytes"
	"regexp"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestTheSkeletonDatesAndLanguagesItsDocumentAsSetDocumentInfoDoes: a document
// built with SkeletonOptions.Created and Language carries the same
// xmp:CreateDate and /Lang as one built without them and described with
// SetDocumentInfo, and passes its level, at every level. The two paths are
// two pieces of code, and this is what keeps them one rule.
func TestTheSkeletonDatesAndLanguagesItsDocumentAsSetDocumentInfoDoes(t *testing.T) {
	at := time.Date(2026, 10, 27, 9, 30, 0, 0, time.FixedZone("", 3600))
	description := regexp.MustCompile(`(?s)<dc:description>.*?</dc:description>`)
	described := func(d *Document) (date, lang, subject, keywords string) {
		t.Helper()
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		meta, _ := d.Resolve(cat.Get("Metadata")).(*object.Stream)
		p, err := xmp.Parse(meta.Data)
		if err != nil {
			t.Fatal(err)
		}
		date, _ = p.Text(xmp.NSXMP, "CreateDate")
		keywords, _ = p.Text(xmp.NSPDF, "Keywords")
		subject = string(description.Find(meta.Data))
		l, _ := cat.Get("Lang").(object.String)
		return date, string(l.Value), subject, keywords
	}
	for _, level := range pdfa.Levels() {
		built, err := NewPDFADocumentWith(pdfa.SkeletonOptions{Level: level, Created: at, Language: "nl",
			Subject: "Facturen & <kwijtingen>", Keywords: "factuur, 2026"})
		if err != nil {
			t.Fatalf("%s: %v", level, err)
		}
		other, err := NewPDFADocumentWith(pdfa.SkeletonOptions{Level: level, Created: at.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if err := other.SetDocumentInfo(DocumentInfo{Created: at, Language: "nl",
			Subject: "Facturen & <kwijtingen>", Keywords: "factuur, 2026"}); err != nil {
			t.Fatalf("%s: SetDocumentInfo: %v", level, err)
		}
		bd, bl, bs, bk := described(built)
		od, ol, os, ok := described(other)
		if bd != od || bl != ol || bs != os || bk != ok {
			t.Errorf("%s: the skeleton writes CreateDate %q, /Lang %q, %s and Keywords %q;\nSetDocumentInfo writes %q, %q, %s and %q",
				level, bd, bl, bs, bk, od, ol, os, ok)
		}
		if bs == "" || bk == "" {
			t.Errorf("%s: the skeleton wrote no dc:description (%q) or pdf:Keywords (%q)", level, bs, bk)
		}

		var buf bytes.Buffer
		if err := built.Write(&buf); err != nil {
			t.Fatal(err)
		}
		back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range ValidatePDFA(back, level) {
			t.Errorf("%s: %v", level, v)
		}
	}
}
