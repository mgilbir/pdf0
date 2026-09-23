package pdf0

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// formPage is a page drawing content, with form XObjects X0..Xn-1 as objects
// 5.. whose content streams are forms[i]; every form's resources name every
// form, so any may draw any other.
func formPage(content string, forms ...string) []byte {
	var names strings.Builder
	for i := range forms {
		fmt.Fprintf(&names, "/X%d %d 0 R", i, 5+i)
	}
	res := "<</XObject<<" + names.String() + ">>>>"
	objs := rawPage(content, res)
	for _, f := range forms {
		objs = append(objs, rawObj{dict: "<</Type/XObject/Subtype/Form/BBox[0 0 1 1]/Resources" + res + ">>", stream: []byte(f)})
	}
	return buildRawPDF(objs)
}

// TestExtractTextFormDrawnNTimes is audit 2026-09-22 C87. The guard against a
// form that draws itself was a set that was filled and never cleared, so a
// form drawn three times on a page extracted once. It is now the set of forms
// on the current path, which still stops a form that draws itself.
func TestExtractTextFormDrawnNTimes(t *testing.T) {
	for _, c := range []struct {
		name, content string
		forms         []string
		want          string
	}{
		{"three times on the page", "/X0 Do /X0 Do /X0 Do", []string{"BT (A) Tj ET"}, "AAA"},
		{"twice inside a form drawn twice", "/X0 Do /X0 Do", []string{"/X1 Do /X1 Do", "BT (B) Tj ET"}, "BBBB"},
		{"a form that draws itself", "/X0 Do", []string{"BT (C) Tj ET /X0 Do"}, "C"},
		{"two forms that draw each other", "/X0 Do", []string{"BT (D) Tj ET /X1 Do", "BT (E) Tj ET /X0 Do"}, "DE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := extractTextOf(t, formPage(c.content, c.forms...)); got != c.want {
				t.Errorf("ExtractText = %q, want %q", got, c.want)
			}
		})
	}
}

// fanOut is n forms, each drawing the next twice, the last showing "A": 2^n
// extractions of the last form's content from one page.
func fanOut(n int) []byte {
	forms := make([]string, n+1)
	for i := 0; i < n; i++ {
		forms[i] = fmt.Sprintf("/X%d Do /X%d Do", i+1, i+1)
	}
	forms[n] = "BT (A) Tj ET"
	return formPage("/X0 Do", forms...)
}

// Once forms extract every time they are drawn, a fan-out is exponential:
// thirty forms that each draw the next twice ask for 2^30 extractions. The
// run's content budget is what bounds it, and running out is an error naming
// the page, with the budget's own message.
func TestExtractTextFormFanOutIsBudgeted(t *testing.T) {
	check := func(t *testing.T, opts ...Option) {
		pdf := fanOut(30)
		doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)), opts...)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		text, err := doc.ExtractText()
		t.Logf("refused after %v", time.Since(start))
		var pe *PageTextError
		if !errors.As(err, &pe) || pe.Page != 1 || !strings.Contains(pe.Error(), "resource limit reached (decoded-content-total)") {
			t.Fatalf("err = %v, want page 1 left out at the content budget", err)
		}
		if text != "" {
			t.Errorf("the page's partial text was returned: %d bytes", len(text))
		}
	}
	t.Run("a configured budget", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
			check(t, WithMaxDecodedContentBytes(1<<20))
		})
	})
	// About five seconds: the default budget is 512 MB of content, which is
	// what tokenizing takes that long, and each of the eleven-byte streams is
	// charged at minContentCharge. Charged at their length, the same fan-out
	// ran for half a minute, which the timeout catches.
	t.Run("the default budget", func(t *testing.T) {
		hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 20 * time.Second}, func(t *testing.T) {
			check(t)
		})
	})
}

// TestExtractTextOnePanickingPageDoesNotAbortTheRest is audit 2026-09-22 C54
// for text: a panic in one page's extraction is recovered at the page, the page
// is left out and reported, and the other pages are extracted. The fault is
// planted, because every crash extraction is known to have had is also fixed
// where it happened.
func TestExtractTextOnePanickingPageDoesNotAbortTheRest(t *testing.T) {
	objs := []rawObj{
		{dict: "<</Type/Catalog/Pages 2 0 R>>"},
		{dict: "<</Type/Pages/Kids[3 0 R 4 0 R 5 0 R]/Count 3>>"},
	}
	for i := range 3 {
		objs = append(objs, rawObj{dict: fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]/Contents %d 0 R/Resources<<>>>>", 6+i)})
	}
	for _, s := range []string{"one", "two", "three"} {
		objs = append(objs, rawObj{dict: "<<>>", stream: []byte("BT (" + s + ") Tj ET")})
	}
	pdf := buildRawPDF(objs)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	pages := doc.PageList()
	textPageHook = func(page *object.Dictionary) {
		if page == pages[1] {
			panic("planted fault")
		}
	}
	defer func() { textPageHook = nil }()

	text, err := doc.ExtractText()
	var pe *PageTextError
	if !errors.As(err, &pe) || pe.Page != 2 || !strings.Contains(err.Error(), "internal error") || !strings.Contains(err.Error(), "planted fault") {
		t.Fatalf("err = %v, want page 2's internal error", err)
	}
	if got := strings.Split(text, "\f"); len(got) != 3 || !strings.Contains(got[0], "one") || got[1] != "" || !strings.Contains(got[2], "three") {
		t.Errorf("text = %q, want pages one and three, and an empty second page between them", text)
	}

	if _, err := doc.ExtractPageText(pages[1]); !errors.As(err, &pe) || pe.Page != 0 {
		t.Errorf("ExtractPageText of the failing page: %v", err)
	}
	if got, err := doc.ExtractPageText(pages[2]); err != nil || !strings.Contains(got, "three") {
		t.Errorf("ExtractPageText of another page = %q, %v", got, err)
	}
}
