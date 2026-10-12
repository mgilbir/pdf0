package pdfua

import (
	"fmt"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// TestFormFanOutIsWalkedOnce: the real-content (7.1) and language (7.2) walks
// enter the form XObjects a stream paints. Forms painting forms make a DAG,
// and a DAG walked path by path is exponential: here each of depth levels
// holds fan forms, each painting every form of the level below, so there are
// fan^depth paths to the leaves (30^7, 22 billion) through 210 forms. A form
// with resources of its own reads the same wherever it is painted, so each is
// judged once and the walk is linear in the forms.
func TestFormFanOutIsWalkedOnce(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: time.Minute}, func(t *testing.T) {
		const fan, depth = 30, 7
		f := newUAFixture("/F0 Do")
		// No catalog /Lang, so the language walk runs too.
		level := []object.IndirectRef{}
		for l := depth - 1; l >= 0; l-- {
			var next []object.IndirectRef
			for i := 0; i < fan; i++ {
				content := "BT (leaf) Tj ET"
				xobjs := &object.Dictionary{}
				if len(level) > 0 {
					content = ""
					for j, ref := range level {
						name := fmt.Sprintf("F%d", j)
						xobjs.Set(object.Name(name), ref)
						content += "/" + name + " Do "
					}
				}
				form := &object.Stream{Data: []byte(content)}
				form.Dict.Set("Type", object.Name("XObject"))
				form.Dict.Set("Subtype", object.Name("Form"))
				form.Dict.Set("Resources", object.NewDictionary(object.Entry{Key: "XObject", Value: xobjs}))
				next = append(next, f.add(form))
			}
			level = next
		}
		f.res.Set("XObject", object.NewDictionary(object.Entry{Key: "F0", Value: level[0]}))

		done := make(chan []Violation, 1)
		go func() { done <- f.run("1") }()
		var vs []Violation
		select {
		case vs = <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("validation did not finish within 30s on a form DAG of 210 forms")
		}
		for _, v := range vs {
			if finding.IsCheckerFinding(v) {
				t.Errorf("the walk tripped a budget instead of finishing: %v", v)
			}
		}
		if len(findings(vs, "7.1", "neither tagged nor marked")) == 0 {
			t.Error("the leaves' untagged text was not reported")
		}
		if len(findings(vs, "7.2", "natural language of text in page content")) == 0 {
			t.Error("the leaves' text with no language was not reported")
		}
	})
}
