package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
)

// TestCorpusContentLexerDifferential compares core.ContentLexer, the one
// content tokenizer, with the four it replaced (content_lexer_oracle_test.go)
// over every content stream in the veraPDF corpus: page contents, form
// XObjects, tiling patterns and Type 3 glyph procedures.
//
// Each old tokenizer is compared in its own projection — what it reported,
// the way it reported it — and every difference must fall into a class this
// test names and explains. A difference it cannot classify fails the test
// with the stream, the position and both tokens, so a change to the lexer
// cannot slip a new meaning past the corpus. The per-class counts are logged.
//
// The classes (all are the new lexer being right where an old one was not):
//
//   - string-eol: an end-of-line marker inside a literal string, unescaped, is
//     one LF whatever it was written as (ISO 32000-2 7.3.4.2); the old content
//     decoders kept CR and CR LF as written.
//   - string-continuation: a backslash before an end-of-line marker continues
//     the line and contributes nothing; TokenizeContent kept the end of line.
//   - hex-non-digit: a byte in a hex string that is neither a digit nor white
//     space is skipped; TokenizeContent read it as a zero nibble.
//   - number-run: a run of regular bytes that begins like a number is one
//     token; TokenizeContent split "12abc" into 12 and abc.
//   - long-number: a number longer than MaxContentTokenLen is still a number;
//     ForEachContentToken dropped it with the binary runs.
//   - long-keyword: a non-number run longer than MaxContentTokenLen is binary
//     and dropped; TokenizeContent reported it as an operator.
//   - name-escape: a name's #xx escapes are expanded (7.3.5), so /F#31 is the
//     resource F1; TokenizeContent reported the name as written.
//   - dict-bytes: a dictionary operand ends at its matching ">>" as the lexer
//     reads it, so a ">>" inside a comment or an inline image in the
//     dictionary does not end it; ScanContentDict did not skip comments.
//   - device-ops: ScanStreamForDeviceOps differs only where its tokens did.
func TestCorpusContentLexerDifferential(t *testing.T) {
	root := testfiles.VeraPDFCorpus.Path(t)
	files := testfiles.VeraPDFCorpus.PDFs(t, "")
	classes := map[string]int{}
	streams := 0
	var unexplained []string
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		for _, content := range corpusContentStreams(doc) {
			streams++
			for _, d := range lexerDifferences(content) {
				if d.class == "" {
					if len(unexplained) < 20 {
						unexplained = append(unexplained, fmt.Sprintf("%s: %s", rel, d.detail))
					}
					classes["UNEXPLAINED"]++
					continue
				}
				classes[d.class]++
			}
		}
	}
	if streams == 0 {
		t.Fatal("no content streams were compared")
	}
	var names []string
	for c := range classes {
		names = append(names, c)
	}
	sort.Strings(names)
	for _, c := range names {
		t.Logf("%s: %d", c, classes[c])
	}
	t.Logf("%d content streams in %d files compared", streams, len(files))
	for _, u := range unexplained {
		t.Errorf("unexplained difference: %s", u)
	}
}

// corpusContentStreams returns the decoded bytes of every content stream in a
// document.
func corpusContentStreams(doc *Document) [][]byte {
	v := doc.view()
	v.Run = core.NewRun(nil)
	var out [][]byte
	seen := map[*object.Stream]bool{}
	add := func(s *object.Stream) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		if c, r := v.Content(s); r == core.ReasonOK && c != nil {
			out = append(out, c)
		}
	}
	if cat := v.Catalog(); cat != nil {
		for _, pg := range v.Pages(cat.Get("Pages")) {
			switch c := v.Resolve(pg.Dict.Get("Contents")).(type) {
			case *object.Stream:
				add(c)
			case object.Array:
				for _, e := range c {
					s, _ := v.Resolve(e).(*object.Stream)
					add(s)
				}
			}
		}
	}
	for _, num := range v.SortedObjectNums() {
		switch o := doc.Objects[num].Value.(type) {
		case *object.Stream:
			if st, _ := v.ResolveName(o.Dict.Get("Subtype")); st == "Form" {
				add(o)
			} else if pt, _ := v.ResolveInt(o.Dict.Get("PatternType")); pt == 1 {
				add(o)
			}
		case *object.Dictionary:
			if st, _ := v.ResolveName(o.Get("Subtype")); st == "Type3" {
				if cps := v.ResolveDict(o.Get("CharProcs")); cps != nil {
					for cp := range cps.Values() {
						s, _ := v.Resolve(cp).(*object.Stream)
						add(s)
					}
				}
			}
		}
	}
	return out
}

type lexerDiff struct {
	class  string // "" when unexplained
	detail string
}

// ptok is one token in a projection: its kind, its value, and the source
// bytes the new lexer read it from (for classifying a difference).
type ptok struct {
	kind, val string
	raw       []byte
}

func (p ptok) String() string { return p.kind + ":" + fmt.Sprintf("%q", p.val) }

// lexerDifferences compares the new lexer with each old tokenizer over one
// stream, and ScanStreamForDeviceOps's answer with the old one's.
func lexerDifferences(content []byte) []lexerDiff {
	var out []lexerDiff
	cmp := func(name string, old, new []ptok) {
		n := min(len(old), len(new))
		for i := 0; i < n; i++ {
			o, w := old[i], new[i]
			if o.kind == w.kind && o.val == w.val {
				continue
			}
			class := classifyTokenDiff(o, w)
			out = append(out, lexerDiff{class, fmt.Sprintf("%s: token %d: old %v, new %v (source %q)", name, i, o, w, clip(w.raw))})
			if o.kind != w.kind || !valueOnlyClass[class] {
				return // the sequences are out of step from here
			}
		}
		if len(old) != len(new) && (len(out) == 0 || valueOnlyClass[out[len(out)-1].class]) {
			// Same prefix, different length: classify by what is left over.
			var rest []ptok
			if len(old) > n {
				rest = old[n:]
			} else {
				rest = new[n:]
			}
			class := classifyTail(rest)
			out = append(out, lexerDiff{class, fmt.Sprintf("%s: %d old tokens, %d new; first extra %v", name, len(old), len(new), rest[0])})
		}
	}
	cmp("ForEachContentItem", projectOldItems(content), projectNewItems(content))
	cmp("ForEachContentToken", projectOldTokens(content), projectNewTokens(content))
	cmp("TokenizeContent", projectOldTokenize(content), projectNewTokenize(content))

	or, oc, og := oldScanStreamForDeviceOps(core.Canceler{}, content)
	nr, nc, ng := core.ScanStreamForDeviceOps(core.Canceler{}, content)
	if or != nr || oc != nc || og != ng {
		// Explained only when the token streams it reads differ too.
		class := ""
		for _, d := range out {
			if d.class != "" {
				class = "device-ops"
			}
		}
		out = append(out, lexerDiff{class, fmt.Sprintf("ScanStreamForDeviceOps: old (R%v C%v G%v), new (R%v C%v G%v)", or, oc, og, nr, nc, ng)})
	}
	return out
}

func clip(b []byte) []byte {
	if len(b) > 80 {
		return b[:80]
	}
	return b
}

// valueOnlyClass is the classes in which one token's value differs and the
// sequences stay in step, so the comparison can go on past it.
var valueOnlyClass = map[string]bool{
	"string-eol": true, "string-continuation": true, "hex-non-digit": true,
	"name-escape": true, "dict-bytes": true,
}

// classifyTokenDiff names the class of one differing token pair, or "".
func classifyTokenDiff(o, w ptok) string {
	if o.kind == "str" && w.kind == "str" {
		oldv, newv := o.val, w.val
		if w.raw[0] == '<' {
			if bytes.ContainsFunc(bytes.TrimSuffix(w.raw[1:], []byte(">")), func(r rune) bool {
				return !strings.ContainsRune("0123456789abcdefABCDEF \t\r\n\f\x00", r)
			}) {
				return "hex-non-digit"
			}
			return ""
		}
		if strings.ReplaceAll(strings.ReplaceAll(oldv, "\r\n", "\n"), "\r", "\n") == newv {
			return "string-eol"
		}
		if bytes.Contains(w.raw, []byte("\\\n")) || bytes.Contains(w.raw, []byte("\\\r")) {
			return "string-continuation"
		}
		return ""
	}
	if o.kind == "name" && w.kind == "name" && bytes.IndexByte(w.raw, '#') >= 0 && string(w.raw) == o.val {
		return "name-escape"
	}
	if o.kind == "num" && w.kind == "num" && strings.HasPrefix(w.val, o.val) {
		return "number-run"
	}
	if o.kind == "dict" && w.kind == "dict" {
		return "dict-bytes"
	}
	if len(w.val) > core.MaxContentTokenLen && w.kind == "tok" {
		return "long-number"
	}
	if len(o.val) > core.MaxContentTokenLen && (o.kind == "op" || o.kind == "tok") {
		return "long-keyword"
	}
	return ""
}

func classifyTail(rest []ptok) string {
	for _, p := range rest {
		if len(p.val) > core.MaxContentTokenLen {
			if p.kind == "tok" {
				return "long-number"
			}
			return "long-keyword"
		}
	}
	return ""
}

// --- the projections ---

func projectOldItems(content []byte) []ptok {
	var out []ptok
	oldForEachContentItem(core.Canceler{}, content, func(kind oldItemKind, payload []byte) {
		switch kind {
		case oldItemOperator:
			out = append(out, ptok{kind: "op", val: string(payload)})
		case oldItemName:
			out = append(out, ptok{kind: "name", val: string(payload)})
		case oldItemString:
			out = append(out, ptok{kind: "str", val: string(payload)})
		case oldItemNumber:
			out = append(out, ptok{kind: "num", val: string(payload)})
		case oldItemDict:
			out = append(out, ptok{kind: "dict", val: string(payload)})
		}
	})
	return out
}

func projectNewItems(content []byte) []ptok {
	var out []ptok
	lx := core.NewContentLexer(core.Canceler{}, content)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentOperator:
			out = append(out, ptok{"op", string(t.Raw), t.Raw})
		case core.ContentName:
			out = append(out, ptok{"name", string(t.Raw), t.Raw})
		case core.ContentString, core.ContentHexString:
			out = append(out, ptok{"str", string(t.Bytes()), t.Raw})
		case core.ContentNumber:
			out = append(out, ptok{"num", string(t.Raw), t.Raw})
		case core.ContentDictStart:
			d := lx.SkipDict(&t)
			out = append(out, ptok{"dict", string(d), d})
		}
	}
	return out
}

func projectOldTokens(content []byte) []ptok {
	var out []ptok
	oldForEachContentToken(core.Canceler{}, content, func(tok []byte, isName bool) {
		if isName {
			out = append(out, ptok{kind: "name", val: string(tok)})
		} else {
			out = append(out, ptok{kind: "tok", val: string(tok)})
		}
	})
	return out
}

func projectNewTokens(content []byte) []ptok {
	var out []ptok
	lx := core.NewContentLexer(core.Canceler{}, content)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentName:
			out = append(out, ptok{"name", string(t.Raw), t.Raw})
		case core.ContentOperator, core.ContentNumber:
			out = append(out, ptok{"tok", string(t.Raw), t.Raw})
		}
	}
	return out
}

func projectOldTokenize(content []byte) []ptok {
	var out []ptok
	for tk := range oldTokenizeContent(core.Canceler{}, content) {
		out = append(out, tokenizeProjection(tk, nil))
	}
	return out
}

func projectNewTokenize(content []byte) []ptok {
	var out []ptok
	// The adapter is what TokenizeContent's callers see; the lexer is read in
	// step with it only to recover each token's source bytes for classifying.
	lx := core.NewContentLexer(core.Canceler{}, content)
	var t core.ContentTok
	for tk := range core.TokenizeContent(core.Canceler{}, content) {
		var raw []byte
		for lx.Next(&t) {
			if t.Kind != core.ContentDictStart && t.Kind != core.ContentDictEnd && t.Kind != core.ContentInlineImage {
				raw = t.Raw
				break
			}
		}
		out = append(out, tokenizeProjection(tk, raw))
	}
	return out
}

func tokenizeProjection(tk core.ContentToken, raw []byte) ptok {
	switch tk.Kind {
	case core.KindOp:
		return ptok{"op", tk.Op, raw}
	case core.KindNumber:
		return ptok{"num", string(tk.Raw), raw}
	case core.KindString:
		return ptok{"str", string(tk.Str), raw}
	case core.KindName:
		return ptok{"name", tk.Name, raw}
	case core.KindArrayStart:
		return ptok{"[", "", raw}
	case core.KindArrayEnd:
		return ptok{"]", "", raw}
	}
	return ptok{"?", "", raw}
}
