package html

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Fuzzing the HTML reader.
//
// Markup is the other half of the untrusted surface the rendering proposal's
// §4.3 asks be fuzzed from the first milestone. What is checked is not whether a
// tree is *right* — the tests next door are that — but the properties that must
// hold for every input, and each of them is a real failure mode:
//
//   - Totality. Reading never panics and always terminates.
//   - Structural soundness. The tree is a tree: every child points back at its
//     parent, nothing is its own ancestor, and the frame is always there. A
//     consumer walking a malformed tree is where a parser bug becomes a hang.
//   - Boundedness. Neither the depth nor the node count passes its cap, so
//     nothing downstream that walks recursively can be driven off the stack.

func FuzzParse(f *testing.F) {
	seeds := []string{
		"<p>hello</p>",
		"<!DOCTYPE html><html><head><title>t</title></head><body><p>x</p></body></html>",
		"<ul><li>a<li>b</ul>",
		"<table><tr><td>a<td>b</table>",
		`<a href="x?a=1&amp;b=2" class="c">t</a>`,
		"<style>a{b:c}</style>",
		"<script>var x = 1 < 2;</script>",
		"<p>&amp;&#65;&#x41;&nbsp;</p>",

		// One seed per refusal, because those are the paths tests written from
		// the specification reach least often.
		"<", ">", "</", "<>", "</>", "<!", "<!--", "<!---", "<?", "<![CDATA[",
		"&", "&#", "&#x", "&;", "&#;", "&notit;", "&nosuch;",
		"<p", "<p ", "<p a", "<p a=", "<p a='", `<p a="`, "<p/", "<div/>",
		"<p class=a class=b>", "<a href=a=b>", "</br>", "<blink>x</blink>",
		"<b><i>x</b></i>", "<div>x", "</div>",
		"<style>", "<title>", "<script>", "<textarea>",
		"\x00", "<p>\x00</p>",
		strings.Repeat("<div>", 400),
		strings.Repeat("<li>", 400),
		strings.Repeat("&amp;", 400),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		doc, errs, ok := Parse(src)

		if doc == nil {
			// Only the whole-document size bound returns no tree, and no fuzz
			// input reaches it.
			if len(src) <= maxInputBytes {
				t.Fatal("produced no document at all")
			}
			return
		}

		// The frame is unconditional: everything downstream indexes off it.
		if doc.Type != DocumentNode {
			t.Fatalf("the root is a %v, not the document", doc.Type)
		}
		for _, name := range []string{"html", "head", "body"} {
			if doc.Element(name) == nil {
				t.Fatalf("the document has no <%s>", name)
			}
		}

		if ok && len(errs) != 0 {
			t.Fatalf("a document read without refusal still reported %v", errs)
		}
		if len(errs) > maxErrors+1 {
			t.Fatalf("reported %d problems, past the bound of %d", len(errs), maxErrors)
		}
		for _, e := range errs {
			if e.Offset < 0 || e.Offset > len(src) {
				t.Fatalf("a problem reported at offset %d, outside the input of %d bytes",
					e.Offset, len(src))
			}
			if !utf8.ValidString(e.Message) {
				t.Fatalf("a problem reported with a message that is not text: %q", e.Message)
			}
			if line, col := Position(src, e.Offset); line < 1 || col < 1 {
				t.Fatalf("offset %d is at %d:%d, which is not a place", e.Offset, line, col)
			}
		}

		checkTree(t, doc, len(src))

		// Every walk has to terminate, over any tree the parser can build.
		doc.TextContent()
		doc.Walk(func(*Node) bool { return true })
	})
}

// checkTree walks iteratively — it is checking a depth bound, so it must not
// need the stack that bound protects.
func checkTree(t *testing.T, doc *Node, srcLen int) {
	t.Helper()

	type frame struct {
		n *Node
		d int
	}
	stack := []frame{{doc, 1}}
	seen := map[*Node]bool{doc: true}
	nodes := 0

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++

		if nodes > maxNodes+16 {
			t.Fatalf("the tree holds more than the cap of %d nodes", maxNodes)
		}
		// The frame is html > head, body, so the cap can be exceeded by the
		// small constant the skeleton costs and no more.
		if f.d > maxDepth+8 {
			t.Fatalf("the tree is %d deep, past the cap of %d", f.d, maxDepth)
		}

		switch f.n.Type {
		case ElementNode:
			if f.n.Name == "" {
				t.Fatal("an element with no name")
			}
			if f.n.Name != strings.ToLower(f.n.Name) {
				t.Fatalf("the element name %q was not folded", f.n.Name)
			}
			if f.n.Text != "" {
				t.Fatalf("the element <%s> carries text of its own", f.n.Name)
			}
			names := map[string]bool{}
			for _, a := range f.n.Attrs {
				if a.Name == "" {
					t.Fatalf("<%s> has an attribute with no name", f.n.Name)
				}
				if a.Name != strings.ToLower(a.Name) {
					t.Fatalf("the attribute name %q was not folded", a.Name)
				}
				if names[a.Name] {
					t.Fatalf("<%s> kept two %q attributes", f.n.Name, a.Name)
				}
				names[a.Name] = true
			}
		case TextNode:
			if len(f.n.Children) != 0 {
				t.Fatal("a text node with children")
			}
			// Adjacent runs are merged, so a text node is never empty and never
			// follows another.
			if f.n.Text == "" {
				t.Fatal("an empty text node")
			}
		}

		if f.n.Offset < 0 || f.n.Offset > srcLen {
			t.Fatalf("a node at offset %d, outside the input of %d bytes", f.n.Offset, srcLen)
		}

		var lastWasText bool
		for _, c := range f.n.Children {
			if c == nil {
				t.Fatal("a nil child")
			}
			if c.Parent != f.n {
				t.Fatal("a child that does not point back at its parent")
			}
			if seen[c] {
				t.Fatal("a node reachable twice, so the tree is not one")
			}
			seen[c] = true
			if c.Type == TextNode && lastWasText {
				t.Fatal("two text nodes in a row, which merging should have joined")
			}
			lastWasText = c.Type == TextNode
			stack = append(stack, frame{c, f.d + 1})
		}
	}
}
