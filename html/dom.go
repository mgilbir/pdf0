// Package html reads the subset of HTML this engine lays out.
//
// # A subset, refused at the edges
//
// This is not an HTML5 parser. HTML5's parsing algorithm is defined to accept
// *every* byte sequence and produce a tree from it, with several hundred
// recovery rules that reconstruct what an author probably meant. That
// definition exists because browsers must render the whole web, including two
// decades of documents nobody will ever fix.
//
// A document generator is not in that position. Its input is a template its
// caller wrote, and an unclosed tag there is a bug the caller wants to hear
// about — not something to be silently repaired into a tree that renders
// almost right. So this reads a declared subset and *refuses* what falls
// outside it, which is the decision recorded in §2.3 of the rendering
// proposal. The cost is real and worth restating: markup a browser accepts,
// this will reject.
//
// Refusing is not the same as failing. Every refusal names what was wrong and
// where, and the ones that are correct-HTML-we-do-not-implement are marked
// apart from the ones that are malformed, because those send an author to
// different places.
//
// # What is deliberately absent
//
// No scripting, at any point, under any option. <script>, <iframe>, <object>
// and <embed> are dropped and reported, per §4.1 — they are the whole of the
// remote-content and code-execution surface, and a renderer that quietly
// ignored them would still be one that read them.
//
// No network and no filesystem. Nothing here resolves a URL; an <img src> is
// recorded as written and left for the caller's resolver to decide about.
package html

import "strings"

// NodeType says what a node is. There are only three, because a renderer needs
// only three: the document, its elements, and the text in them.
//
// Comments are not among them. They are dropped rather than recorded, which
// costs nothing — nothing in layout, painting or the tagged-PDF structure has
// any use for one — and saves every consumer from walking past them.
type NodeType uint8

const (
	// DocumentNode is the root. It has exactly one element child, <html>.
	DocumentNode NodeType = iota
	// ElementNode is a tag.
	ElementNode
	// TextNode is character data. Adjacent runs are merged, so no element ever
	// has two text children in a row.
	TextNode
)

// Attribute is one attribute of an element.
type Attribute struct {
	// Name is lowercased, because HTML attribute names are case-insensitive and
	// leaving the case as written would mean every consumer folding it again.
	Name string
	// Value has its character references resolved.
	Value string
}

// Node is one node of the document tree.
type Node struct {
	Type NodeType

	// Name is the element's tag name, lowercased. It is empty for the other two
	// kinds.
	Name string

	// Attrs is in source order, with duplicates already refused.
	Attrs []Attribute

	// Text is the character data of a TextNode, with references resolved.
	Text string

	// Parent is nil for the document node.
	Parent *Node

	// Children is in source order.
	Children []*Node

	// Offset is the byte offset in the source at which the node begins, so a
	// finding from layout can point back at the markup that caused it. That is
	// what §6 of the rendering proposal needs to say *where* a guardrail fired,
	// and it cannot be recovered later.
	Offset int
}

// Attr returns the value of an attribute and whether it was present. The name
// is matched lowercased, as HTML matches it.
func (n *Node) Attr(name string) (string, bool) {
	if n == nil {
		return "", false
	}
	name = strings.ToLower(name)
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// HasAttr reports whether an attribute is present, whatever its value. It is
// the question a boolean attribute such as "hidden" asks.
func (n *Node) HasAttr(name string) bool {
	_, ok := n.Attr(name)
	return ok
}

// Text returns the concatenated text of a node and everything inside it.
//
// This is what an alt-less <a> contributes to a tagged PDF's text, and what a
// heading contributes to an outline. It walks iteratively, because the tree
// came from untrusted input and a recursive walk over a deep one would need the
// stack the parser's depth cap exists to protect.
func (n *Node) TextContent() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	stack := []*Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Type == TextNode {
			b.WriteString(cur.Text)
			continue
		}
		// Children are pushed in reverse so they come off in source order.
		for i := len(cur.Children) - 1; i >= 0; i-- {
			stack = append(stack, cur.Children[i])
		}
	}
	return b.String()
}

// Walk calls fn for the node and every node under it, in document order.
//
// It stops descending into a node for which fn returns false, which is what
// skipping a subtree needs — and it is iterative for the same reason
// TextContent is.
func (n *Node) Walk(fn func(*Node) bool) {
	if n == nil {
		return
	}
	stack := []*Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !fn(cur) {
			continue
		}
		for i := len(cur.Children) - 1; i >= 0; i-- {
			stack = append(stack, cur.Children[i])
		}
	}
}

// Element finds the first element with the given name, in document order.
func (n *Node) Element(name string) *Node {
	name = strings.ToLower(name)
	var found *Node
	n.Walk(func(c *Node) bool {
		if found != nil {
			return false
		}
		if c.Type == ElementNode && c.Name == name {
			found = c
			return false
		}
		return true
	})
	return found
}

// appendChild adds a child and sets its parent, which are always done together.
func (n *Node) appendChild(c *Node) {
	c.Parent = n
	n.Children = append(n.Children, c)
}
