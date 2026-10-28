package pdfa

import (
	"errors"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
)

// createDateOf is the xmp:CreateDate of a skeleton, and whether it has one.
func createDateOf(t *testing.T, objs map[int]*object.IndirectObject) (string, bool) {
	t.Helper()
	meta, ok := objs[3].Value.(*object.Stream)
	if !ok {
		t.Fatalf("object 3 is %T, not the metadata stream", objs[3].Value)
	}
	p, err := xmp.Parse(meta.Data)
	if err != nil {
		t.Fatalf("the skeleton's XMP does not parse: %v", err)
	}
	return p.Text(xmp.NSXMP, "CreateDate")
}

// TestAZeroCreatedIsNow: a skeleton built with no creation date is dated by
// the clock — not left undated, and not the zero time or the Unix epoch, the
// two dates an unset value becomes.
func TestAZeroCreatedIsNow(t *testing.T) {
	before := time.Now().Truncate(time.Second)
	objs, _, _, err := SkeletonWith(SkeletonOptions{Level: PDFA2b})
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := createDateOf(t, objs)
	if !ok {
		t.Fatal("a skeleton with no Created has no xmp:CreateDate; it should be dated now")
	}
	when, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("xmp:CreateDate %q is not RFC 3339: %v", got, err)
	}
	if when.Before(before) || when.After(after) {
		t.Errorf("xmp:CreateDate is %s; the skeleton was built between %s and %s", when, before, after)
	}
	for _, unset := range []time.Time{{}, time.Unix(0, 0)} {
		if when.Year() == unset.Year() {
			t.Errorf("xmp:CreateDate is in %d, the year of an unset date", when.Year())
		}
	}

	// And it is the clock it reads, not a date taken some other way.
	fixed := time.Date(2031, 5, 6, 7, 8, 9, 0, time.FixedZone("", 2*3600))
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return fixed }
	objs, _, _, err = SkeletonWith(SkeletonOptions{Level: PDFA4})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := createDateOf(t, objs); got != "2031-05-06T07:08:09+02:00" {
		t.Errorf("xmp:CreateDate is %q under a clock reading %s", got, fixed)
	}
}

// TestAnExplicitCreatedIsKept, with its zone.
func TestAnExplicitCreatedIsKept(t *testing.T) {
	at := time.Date(2019, 2, 3, 4, 5, 6, 0, time.FixedZone("", -5*3600))
	objs, _, _, err := SkeletonWith(SkeletonOptions{Level: PDFA1b, Created: at})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := createDateOf(t, objs); got != "2019-02-03T04:05:06-05:00" {
		t.Errorf("xmp:CreateDate is %q; want the date given", got)
	}
}

// TestTheEpochIsRefused: 1970-01-01T00:00:00Z, in any zone, is refused rather
// than written or replaced by now.
func TestTheEpochIsRefused(t *testing.T) {
	for _, epoch := range []time.Time{time.Unix(0, 0), time.Unix(0, 0).UTC(), time.Unix(0, 0).In(time.FixedZone("", 3600))} {
		_, _, _, err := SkeletonWith(SkeletonOptions{Level: PDFA2b, Created: epoch})
		if !errors.Is(err, ErrEpochCreated) {
			t.Errorf("Created %s: err %v; want ErrEpochCreated", epoch, err)
		}
	}
	// A second after it is a date somebody meant.
	if _, _, _, err := SkeletonWith(SkeletonOptions{Level: PDFA2b, Created: time.Unix(1, 0)}); err != nil {
		t.Errorf("Created one second after the epoch: %v", err)
	}
}

// TestGenerateXMPMetadataIsUndated: the exported packet builder is what it was,
// identification and description, so a caller assembling a document with it
// does not acquire a date it did not choose.
func TestGenerateXMPMetadataIsUndated(t *testing.T) {
	data, err := GenerateXMPMetadata(PDFA2b, "t", "a")
	if err != nil {
		t.Fatal(err)
	}
	p, err := xmp.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := p.Text(xmp.NSXMP, "CreateDate"); ok {
		t.Errorf("GenerateXMPMetadata wrote xmp:CreateDate %q", got)
	}
}

// TestTheSkeletonsLanguageIsTheCatalogsLang: written as /Lang, refused when it
// is not a language tag, and with digits refused at PDF/A-1 alone.
func TestTheSkeletonsLanguageIsTheCatalogsLang(t *testing.T) {
	objs, _, _, err := SkeletonWith(SkeletonOptions{Level: PDFA2b, Language: "nl-BE"})
	if err != nil {
		t.Fatal(err)
	}
	cat := objs[1].Value.(*object.Dictionary)
	if got, _ := cat.Get("Lang").(object.String); string(got.Value) != "nl-BE" {
		t.Errorf("/Lang is %v; want nl-BE", cat.Get("Lang"))
	}
	objs, _, _, err = SkeletonWith(SkeletonOptions{Level: PDFA2b})
	if err != nil {
		t.Fatal(err)
	}
	if l := objs[1].Value.(*object.Dictionary).Get("Lang"); l != nil {
		t.Errorf("no Language, but /Lang is %v", l)
	}
	for _, tc := range []struct {
		level Level
		lang  string
		ok    bool
	}{
		{PDFA2b, "es-419", true}, {PDFA1b, "es-419", false}, {PDFA1b, "en-GB", true},
		{PDFA4, "not a tag", false}, {PDFA3b, "-en", false}, {PDFA2b, "abcdefghi", false},
	} {
		_, _, _, err := SkeletonWith(SkeletonOptions{Level: tc.level, Language: tc.lang})
		if (err == nil) != tc.ok {
			t.Errorf("%s with Language %q: err %v; want ok=%v", tc.level, tc.lang, err, tc.ok)
		}
	}
}
