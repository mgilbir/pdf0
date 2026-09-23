package core

import (
	"bytes"
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// Name trees (ISO 32000-2 7.9.6): the sorted string-keyed maps behind
// /EmbeddedFiles, /Dests, /JavaScript and the rest of a catalog's /Names.
//
// A tree is a root holding either the pairs themselves (/Names) or children
// (/Kids), each child an intermediate node or a leaf carrying the /Limits of
// the keys under it. Writers used to replace a whole tree to add one entry —
// EmbedFacturX set a fresh /EmbeddedFiles holding only the invoice, deleting
// every attachment the document already had (audit C44). They insert here
// instead.

// maxNameTreeDepth bounds the descent. Real trees are two or three levels; the
// file controls the structure, so the bound is also the cycle guard's backstop.
const maxNameTreeDepth = 32

// NameTreeEntry is one key/value pair of a name tree.
type NameTreeEntry struct {
	Key   []byte
	Value object.Object
}

// NameTreeEntries returns every pair of the tree rooted at root, in tree order.
// A /Kids cycle or a tree deeper than maxNameTreeDepth is not followed further;
// complete is false when anything was skipped, so a caller can tell "these are
// all the entries" from "these are the entries pdf0 could reach".
func (v View) NameTreeEntries(root object.Object) (entries []NameTreeEntry, complete bool) {
	complete = true
	seen := map[*object.Dictionary]bool{}
	var walk func(n object.Object, depth int)
	walk = func(n object.Object, depth int) {
		node := v.ResolveDict(n)
		if node == nil {
			return
		}
		if seen[node] || depth > maxNameTreeDepth {
			complete = false
			return
		}
		seen[node] = true
		if names, ok := v.Resolve(node.Get("Names")).(object.Array); ok {
			for i := 0; i+1 < len(names); i += 2 {
				if k, ok := v.Resolve(names[i]).(object.String); ok {
					entries = append(entries, NameTreeEntry{Key: k.Value, Value: names[i+1]})
				}
			}
		}
		if kids, ok := v.Resolve(node.Get("Kids")).(object.Array); ok {
			for _, k := range kids {
				walk(k, depth+1)
			}
		}
	}
	walk(root, 0)
	return entries, complete
}

// NameTreeInsert puts key → value into the tree rooted at root, keeping the
// keys sorted and every /Limits on the way down true. An existing entry with
// the same key is replaced. root is edited in place; a leaf's /Names array
// held in an indirect object is updated in that object.
//
// The leaf is found the way a reader looks a key up: at each intermediate
// node, the first child whose /Limits upper bound is not below the key, or the
// last child when the key sorts after everything. A tree whose structure does
// not permit that — a child without /Limits, a /Kids cycle, a descent deeper
// than maxNameTreeDepth — is refused with an error rather than guessed at.
func (v View) NameTreeInsert(root *object.Dictionary, key []byte, value object.Object) error {
	var path []*object.Dictionary
	seen := map[*object.Dictionary]bool{}
	node := root
	for depth := 0; ; depth++ {
		if depth > maxNameTreeDepth || seen[node] {
			return fmt.Errorf("name tree is cyclic or deeper than %d levels", maxNameTreeDepth)
		}
		seen[node] = true
		kids, hasKids := v.Resolve(node.Get("Kids")).(object.Array)
		if !hasKids || len(kids) == 0 {
			break
		}
		if node.Get("Names") != nil {
			return fmt.Errorf("name tree node has both /Kids and /Names")
		}
		path = append(path, node)
		var next *object.Dictionary
		for i, k := range kids {
			kid := v.ResolveDict(k)
			if kid == nil {
				return fmt.Errorf("name tree /Kids entry %d is not a dictionary", i)
			}
			_, hi, ok := v.nameTreeLimits(kid)
			if !ok {
				return fmt.Errorf("name tree child %d has no usable /Limits", i)
			}
			next = kid
			if bytes.Compare(key, hi) <= 0 {
				break
			}
		}
		node = next
	}

	// node is the leaf (or a root that has no children yet).
	var names object.Array
	namesRef := object.RefNum(node.Get("Names"))
	if arr, ok := v.Resolve(node.Get("Names")).(object.Array); ok {
		names = append(object.Array(nil), arr...)
	}
	if len(names)%2 != 0 {
		return fmt.Errorf("name tree /Names array has an odd number of elements")
	}
	pos, replace := len(names), false
	for i := 0; i+1 < len(names); i += 2 {
		k, ok := v.Resolve(names[i]).(object.String)
		if !ok {
			return fmt.Errorf("name tree key %d is not a string", i/2)
		}
		if c := bytes.Compare(key, k.Value); c <= 0 {
			pos, replace = i, c == 0
			break
		}
	}
	entry := object.String{Value: append([]byte(nil), key...)}
	if replace {
		names[pos+1] = value
	} else {
		names = append(names[:pos:pos], append(object.Array{entry, value}, names[pos:]...)...)
	}
	if iobj := v.Objects[namesRef]; namesRef != 0 && iobj != nil {
		iobj.Value = names
	} else {
		node.Set("Names", names)
	}

	// Widen /Limits on every node below the root on the way down, and on the
	// leaf itself when it is not the root. The root has no /Limits.
	path = append(path, node)
	for _, n := range path[1:] {
		lo, hi, ok := v.nameTreeLimits(n)
		if !ok {
			lo, hi = key, key
		}
		if bytes.Compare(key, lo) < 0 {
			lo = key
		}
		if bytes.Compare(key, hi) > 0 {
			hi = key
		}
		n.Set("Limits", object.Array{object.String{Value: append([]byte(nil), lo...)}, object.String{Value: append([]byte(nil), hi...)}})
	}
	return nil
}

// NameTreeRemove deletes every entry of the tree for which drop returns true.
// It leaves /Limits as they were: a bound that is wider than the keys beneath
// it still finds every key, and narrowing it is an optimisation a writer is
// not obliged to make.
func (v View) NameTreeRemove(root object.Object, drop func(key []byte, value object.Object) bool) (removed int) {
	seen := map[*object.Dictionary]bool{}
	var walk func(n object.Object, depth int)
	walk = func(n object.Object, depth int) {
		node := v.ResolveDict(n)
		if node == nil || seen[node] || depth > maxNameTreeDepth {
			return
		}
		seen[node] = true
		if names, ok := v.Resolve(node.Get("Names")).(object.Array); ok {
			kept := make(object.Array, 0, len(names))
			for i := 0; i+1 < len(names); i += 2 {
				k, isStr := v.Resolve(names[i]).(object.String)
				if isStr && drop(k.Value, names[i+1]) {
					removed++
					continue
				}
				kept = append(kept, names[i], names[i+1])
			}
			if len(names)%2 == 1 {
				kept = append(kept, names[len(names)-1]) // not ours to repair
			}
			if len(kept) != len(names) {
				if ref := object.RefNum(node.Get("Names")); ref != 0 && v.Objects[ref] != nil {
					v.Objects[ref].Value = kept
				} else {
					node.Set("Names", kept)
				}
			}
		}
		if kids, ok := v.Resolve(node.Get("Kids")).(object.Array); ok {
			for _, k := range kids {
				walk(k, depth+1)
			}
		}
	}
	walk(root, 0)
	return removed
}

func (v View) nameTreeLimits(n *object.Dictionary) (lo, hi []byte, ok bool) {
	lim, isArr := v.Resolve(n.Get("Limits")).(object.Array)
	if !isArr || len(lim) != 2 {
		return nil, nil, false
	}
	l, ok1 := v.Resolve(lim[0]).(object.String)
	h, ok2 := v.Resolve(lim[1]).(object.String)
	if !ok1 || !ok2 {
		return nil, nil, false
	}
	return l.Value, h.Value, true
}
