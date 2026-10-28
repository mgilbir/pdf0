package pdf0

import (
	"bytes"
	"encoding/csv"
	"flag"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This test uses the Arlington PDF Model (github.com/pdf-association/
// arlington-pdf-model, Apache-2.0) as an EXTERNAL oracle for the shape of PDF
// objects, to check that pdf0's parser and serializer represent objects
// FAITHFULLY: right value types, keys present, structure intact. The existing
// round-trip tests compare pdf0's model against itself (self-consistent, not
// necessarily correct); Arlington is ground truth from outside pdf0, so it
// catches structural mis-parsing a self-referential check cannot — e.g. reading
// a page's /MediaBox array as a string would round-trip cleanly yet violate the
// grammar. The model is not committed; run `make arlington` to fetch it.
//
// Traversal is link-driven from the catalog: each key's Link column resolves the
// referenced object's spec, so variants (page-tree root, font/annotation/action
// subtypes) are validated against the right rules. A grammar is chosen by the
// object's kind as well as by /Type, /Subtype and /S: an array by an array
// grammar, a stream by a stream's, a dictionary by a dictionary's or a map's
// (FontMap, XObjectMap, whose "*" row describes every entry). A name or number
// tree is walked to its values, and a page inherits whatever an ancestor in the
// page tree has. A file is held to the grammar of the version it declares, and
// a null entry counts as no entry. Checks are conservative: only unconditional
// constraints (required=TRUE, literal type/PossibleValues) are enforced, and
// predicate-gated rules are skipped, so a finding means a genuine structural
// discrepancy. Where the model itself contradicts ISO 32000, the row is
// excused in arlModelDeviations.

// --- Arlington model ---

type arlRow struct {
	key, types, required, inheritable, possible, links string
}

type arlModel struct {
	dir      string
	cache    map[string][]arlRow
	versions map[string]*arlModel
}

func loadArlModel(dir string) *arlModel { return &arlModel{dir: dir, cache: map[string][]arlRow{}} }

func (m *arlModel) spec(name string) []arlRow {
	if v, ok := m.cache[name]; ok {
		return v
	}
	var rows []arlRow
	if f, err := os.Open(filepath.Join(m.dir, name+".tsv")); err == nil {
		r := csv.NewReader(f)
		r.Comma = '\t'
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		recs, _ := r.ReadAll()
		f.Close()
		for i, c := range recs {
			if i == 0 || len(c) < 11 {
				continue
			}
			rows = append(rows, arlRow{key: c[0], types: c[1], required: c[4], inheritable: c[6], possible: c[8], links: c[10]})
		}
	}
	m.cache[name] = rows
	return rows
}

// arlModelDeviations are rows where the model says something the standard
// does not, each with the text it contradicts. A finding the model would raise
// from one of them is the model's, not the file's, so it is not raised.
// TestArlingtonModelDeviationsStillDeviate fails when the model changes and
// an entry stops being one.
var arlModelDeviations = map[string]string{
	// The model: Required, at every version from 1.2 to 2.0.
	"XObjectFormType1/Resources": "ISO 32000-1:2008 Table 95 and ISO 32000-2:2020 Table 93 both give a " +
		"form XObject's Resources as \"(Optional but strongly recommended; PDF 1.2)\"",
}

// --- validator ---

type arlValidator struct {
	doc      *Document
	m        *arlModel
	findings []string
	visited  map[*object.Dictionary]bool
	// strict also reports what the model does not have: a key no row of the
	// object's grammar names, and a link to an object no candidate grammar
	// describes. Over a version's directory (tsv/1.7) that is what the version
	// lacks, which is how arlVersionFindings asks whether a file fits it. Off
	// for the faithfulness tests, where an unknown key is a private extension
	// and not a defect.
	strict bool
}

const arlMaxDepth = 50

func (v *arlValidator) add(path, msg string) { v.findings = append(v.findings, path+": "+msg) }

func arlTypeName(o object.Object) string {
	switch o.(type) {
	case object.Name:
		return "name"
	case *object.Dictionary:
		return "dictionary"
	case object.Array:
		return "array"
	case object.Integer:
		return "integer"
	case object.Real:
		return "number"
	case object.String:
		return "string"
	case *object.Stream:
		return "stream"
	case object.Boolean:
		return "boolean"
	case object.Null:
		return "null"
	}
	return "?"
}

func arlTypeMatch(o object.Object, types string) bool {
	for _, t := range strings.Split(types, ";") {
		switch strings.TrimSpace(t) {
		case "name":
			if _, k := o.(object.Name); k {
				return true
			}
		case "dictionary", "name-tree", "number-tree":
			if _, k := o.(*object.Dictionary); k {
				return true
			}
		case "array", "rectangle", "matrix":
			if _, k := o.(object.Array); k {
				return true
			}
		case "integer", "bitmask":
			if _, k := o.(object.Integer); k {
				return true
			}
		case "number":
			if _, k := o.(object.Integer); k {
				return true
			}
			if _, k := o.(object.Real); k {
				return true
			}
		case "string-text", "string", "string-byte", "string-ascii", "date":
			if _, k := o.(object.String); k {
				return true
			}
		case "stream":
			if _, k := o.(*object.Stream); k {
				return true
			}
		case "boolean":
			if _, k := o.(object.Boolean); k {
				return true
			}
		case "null":
			if _, k := o.(object.Null); k {
				return true
			}
		}
	}
	return false
}

func arlLitSet(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "fn:") {
		return nil
	}
	set := map[string]bool{}
	for _, g := range strings.Split(s, ";") {
		g = strings.TrimSpace(g)
		if g == "" || g == "[]" {
			continue
		}
		if !strings.HasPrefix(g, "[") || !strings.HasSuffix(g, "]") {
			return nil
		}
		for _, val := range strings.Split(g[1:len(g)-1], ",") {
			if val = strings.TrimSpace(val); val != "" {
				set[val] = true
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func arlLinkSpecs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune("[],;() ", r) }) {
		if t == "" || strings.HasPrefix(t, "fn:") || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// arlIsArrayGrammar is whether a grammar describes an array rather than a
// dictionary. A numeric key is an array's. "*" is both an array's every
// element and a map's every entry, and the model says which by name: its
// arrays are ArrayOf..., ...Array and _UniversalArray, and every other "*"
// grammar (FontMap, ClassMap, RoleMap, ...Dict) is a dictionary.
func arlIsArrayGrammar(name string, spec []arlRow) bool {
	for _, r := range spec {
		if _, err := strconv.Atoi(r.key); err == nil {
			return true
		}
	}
	return strings.HasPrefix(name, "ArrayOf") || strings.HasSuffix(name, "Array")
}

// arlIsStreamGrammar is whether a grammar describes a stream: the model gives
// every stream a required /Length, and no dictionary one.
func arlIsStreamGrammar(spec []arlRow) bool {
	for _, r := range spec {
		if r.key == "Length" && r.required == "TRUE" {
			return true
		}
	}
	return false
}

func (v *arlValidator) dictOf(o object.Object) *object.Dictionary {
	switch t := o.(type) {
	case *object.Dictionary:
		return t
	case *object.Stream:
		return &t.Dict
	}
	return nil
}

func (v *arlValidator) walk(o object.Object, specName, path string, depth int, inh map[string]bool) {
	if depth > arlMaxDepth {
		return
	}
	spec := v.m.spec(specName)
	if spec == nil {
		return
	}
	o = v.doc.Resolve(o)

	// An array is walked by an array grammar and anything else by a
	// dictionary's, and a grammar of the other kind describes some other
	// object: "*" is both an array's every element and a map's every entry
	// (FontMap, XObjectMap, ...), so the grammar's kind comes from its name
	// (arlIsArrayGrammar), not from the key.
	arr, isArr := o.(object.Array)
	if isArr != arlIsArrayGrammar(specName, spec) {
		return
	}
	if isArr {
		var wild *arlRow
		for i := range spec {
			if spec[i].key == "*" {
				wild = &spec[i]
			}
		}
		for i, e := range arr {
			er := v.doc.Resolve(e)
			r := wild
			for j := range spec {
				if spec[j].key == strconv.Itoa(i) {
					r = &spec[j]
				}
			}
			if r == nil {
				continue
			}
			if r.types != "" && !strings.Contains(r.types, "fn:") && !arlTypeMatch(er, r.types) {
				v.add(path, "["+strconv.Itoa(i)+"] type "+arlTypeName(er)+", want "+r.types)
			}
			if r.links != "" {
				v.followRow(er, r.types, r.links, path+"["+strconv.Itoa(i)+"]", depth+1, inh)
			}
		}
		return
	}

	d := v.dictOf(o)
	if d == nil {
		return
	}
	if v.visited[d] {
		return
	}
	v.visited[d] = true

	// Inheritable keys satisfied at or above this node, passed to children.
	child := map[string]bool{}
	for k := range inh {
		child[k] = true
	}
	// Every key this node has is there for a descendant's inheritable row
	// (ISO 32000-2 7.7.3.4): a page inherits /MediaBox and /Resources from
	// the /Pages node above it, whose own grammar does not list them, so
	// reading only the keys this grammar marks inheritable lost them.
	// A key whose value is null is no key, here as everywhere (7.3.9).
	for k, val := range d.All() {
		if _, null := v.doc.Resolve(val).(object.Null); !null {
			child[string(k)] = true
		}
	}

	named := map[string]bool{}
	var wild *arlRow
	for i, r := range spec {
		named[r.key] = true
		if r.key == "*" {
			wild = &spec[i]
		}
	}
	for k, raw := range d.All() {
		if named[string(k)] {
			continue
		}
		if wild == nil {
			if v.strict {
				v.add(path, "/"+string(k)+" is not a key of "+specName)
			}
			continue
		}
		res := v.doc.Resolve(raw)
		if wild.types != "" && !strings.Contains(wild.types, "fn:") && !arlTypeMatch(res, wild.types) {
			v.add(path, "/"+string(k)+" type "+arlTypeName(res)+", want "+wild.types)
		}
		if wild.links != "" {
			v.followRow(res, wild.types, wild.links, path+"/"+string(k), depth+1, child)
		}
	}
	for _, r := range spec {
		raw := d.Get(object.Name(r.key))
		if _, null := v.doc.Resolve(raw).(object.Null); null {
			// An entry whose value is null is no entry (ISO 32000-2 7.3.9).
			raw = nil
		}
		if raw == nil {
			if _, deviates := arlModelDeviations[specName+"/"+r.key]; deviates {
				continue
			}
			if r.required == "TRUE" && !(r.inheritable == "TRUE" && child[r.key]) {
				v.add(path, "missing required /"+r.key)
			}
			continue
		}
		res := v.doc.Resolve(raw)
		if r.types != "" && !strings.Contains(r.types, "fn:") && !arlTypeMatch(res, r.types) {
			v.add(path, "/"+r.key+" type "+arlTypeName(res)+", want "+r.types)
		}
		if ls := arlLitSet(r.possible); ls != nil {
			if n, ok := res.(object.Name); ok && !ls[string(n)] {
				v.add(path, "/"+r.key+" value /"+string(n)+" not in "+r.possible)
			}
		}
		if r.links != "" {
			v.followRow(res, r.types, r.links, path+"/"+r.key, depth+1, child)
		}
	}
}

// followRow follows a row's value to the grammars its Link names. A value the
// row types as a name or number tree is not one of those objects but a tree of
// them (ISO 32000-2 7.9.6, 7.9.7): its nodes are walked, through /Kids to the
// leaves, and each value in a leaf's /Names or /Nums is followed instead. The
// tree's own root is not a destination or a structure element, and walking it
// as one finds what it lacks as one.
func (v *arlValidator) followRow(o object.Object, types, links, path string, depth int, inh map[string]bool) {
	if d, ok := v.doc.Resolve(o).(*object.Dictionary); ok {
		for _, t := range strings.Split(types, ";") {
			switch strings.TrimSpace(t) {
			case "name-tree":
				v.tree(d, "Names", links, path, depth, inh)
				return
			case "number-tree":
				v.tree(d, "Nums", links, path, depth, inh)
				return
			}
		}
	}
	v.follow(o, links, path, depth, inh)
}

// tree walks one node of a name tree (leaves under key "Names") or a number
// tree ("Nums"), following every value to links.
func (v *arlValidator) tree(d *object.Dictionary, key object.Name, links, path string, depth int, inh map[string]bool) {
	if depth > arlMaxDepth || v.visited[d] {
		return
	}
	v.visited[d] = true
	if leaf, ok := v.doc.Resolve(d.Get(key)).(object.Array); ok {
		for i := 1; i < len(leaf); i += 2 {
			v.follow(leaf[i], links, path+"/"+string(key)+"["+strconv.Itoa(i)+"]", depth+1, inh)
		}
	}
	if kids, ok := v.doc.Resolve(d.Get("Kids")).(object.Array); ok {
		for i, k := range kids {
			if kd, ok := v.doc.Resolve(k).(*object.Dictionary); ok {
				v.tree(kd, key, links, path+"/Kids["+strconv.Itoa(i)+"]", depth+1, inh)
			}
		}
	}
}

func (v *arlValidator) follow(o object.Object, links, path string, depth int, inh map[string]bool) {
	cands := arlLinkSpecs(links)
	if len(cands) == 0 {
		return
	}
	o = v.doc.Resolve(o)
	if v.strict {
		known := false
		for _, c := range cands {
			if v.m.spec(c) != nil {
				known = true
			}
		}
		if !known {
			v.add(path, "an object of a kind the model does not have ("+strings.Join(cands, ", ")+")")
			return
		}
	}
	if sn := v.pick(cands, o); sn != "" {
		v.walk(o, sn, path, depth, inh)
	}
}

// pick returns the unique candidate spec that matches the object, or "" if none
// or several do — it never guesses, so an unresolved variant is skipped rather
// than validated against the wrong rules.
func (v *arlValidator) pick(cands []string, o object.Object) string {
	// Only the grammars of the object's own kind are candidates: a dictionary
	// is never an array's, nor an array a dictionary's, and a stream's grammar
	// (the ones that require /Length) describes a stream and not a bare
	// dictionary. Among arrays nothing else tells two grammars apart, so a
	// choice of two is no choice.
	_, isArr := o.(object.Array)
	_, isStream := o.(*object.Stream)
	var kind []string
	for _, c := range cands {
		spec := v.m.spec(c)
		if spec == nil || arlIsArrayGrammar(c, spec) != isArr {
			continue
		}
		if !isArr && arlIsStreamGrammar(spec) != isStream {
			continue
		}
		kind = append(kind, c)
	}
	cands = kind
	if len(cands) == 0 || (isArr && len(cands) > 1) {
		return ""
	}
	if len(cands) == 1 {
		return cands[0]
	}
	d := v.dictOf(o)
	if d == nil {
		return ""
	}
	typ, _ := d.Get("Type").(object.Name)
	sub, _ := d.Get("Subtype").(object.Name)
	sAct, _ := d.Get("S").(object.Name)
	accepts := func(spec []arlRow, key string, val object.Name) bool {
		for _, r := range spec {
			if r.key == key {
				ls := arlLitSet(r.possible)
				if ls == nil {
					return true
				}
				return val != "" && ls[string(val)]
			}
		}
		return true
	}
	var match []string
	for _, c := range cands {
		spec := v.m.spec(c)
		if spec == nil {
			continue
		}
		if accepts(spec, "Type", typ) && accepts(spec, "Subtype", sub) && accepts(spec, "S", sAct) {
			match = append(match, c)
		}
	}
	if len(match) == 1 {
		return match[0]
	}
	return ""
}

// arlValidate walks a document from the catalog and returns its structural
// findings against the grammar.
// arlVersionFindings walks a file against the Arlington grammar of one PDF
// version, strictly: every key and object the file uses that the version does
// not have is a finding. version names a directory of the model ("1.7").
func arlVersionFindings(t *testing.T, data []byte, version string) []string {
	t.Helper()
	m := loadArlModel(filepath.Join(filepath.Dir(arlModelDir(t)), version))
	if m.spec("Catalog") == nil {
		t.Fatalf("the Arlington model has no PDF %s grammar", version)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	v := &arlValidator{doc: doc, m: m, visited: map[*object.Dictionary]bool{}, strict: true}
	v.walk(doc.Trailer.Get("Root"), "Catalog", "Catalog", 0, map[string]bool{})
	return v.findings
}

// forVersion is the model of one PDF version: the directory beside m's
// named for it, or m itself when the model has no such directory. Each is
// loaded once and kept on m.
func (m *arlModel) forVersion(version string) *arlModel {
	if m.versions == nil {
		m.versions = map[string]*arlModel{}
	}
	if vm, ok := m.versions[version]; ok {
		return vm
	}
	vm := m
	dir := filepath.Join(filepath.Dir(m.dir), version)
	if _, err := os.Stat(filepath.Join(dir, "Catalog.tsv")); err == nil && version != "" {
		vm = loadArlModel(dir)
	}
	m.versions[version] = vm
	return vm
}

func arlValidate(m *arlModel, data []byte) (findings []string, readErr error) {
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	// The grammar of the version the file declares, not of PDF 2.0: a key
	// 2.0 made required (a form XObject's /Resources) is optional in a 1.x
	// file, and judging one by 2.0 is the walker's error, not the file's.
	v := &arlValidator{doc: doc, m: m.forVersion(doc.declaredVersion()), visited: map[*object.Dictionary]bool{}}
	v.walk(doc.Trailer.Get("Root"), "Catalog", "Catalog", 0, map[string]bool{})
	return v.findings, nil
}

// arlModelDir finds the Arlington grammar, and distinguishes "nobody fetched
// it" from "somebody pointed at the wrong place".
//
// The difference matters more than it looks. This test is a ratchet: it asserts
// a count of findings over a thousand documents. A skip is indistinguishable
// from a pass in the output, so a mistyped path turns the whole oracle off
// silently — which is exactly what happened when it was run with
// ARLINGTON_MODEL set to the repository root instead of the tsv/2.0 directory
// inside it. The run looked green and had checked nothing.
//
// So: unset and absent is a skip, because the model is fetched on demand and a
// developer without it should still be able to run the suite. Set and wrong is
// a failure, because somebody meant to run this and did not.
//
// The resolution is testfiles.Arlington's: ARLINGTON_MODEL may name the
// tsv/2.0 directory or the checkout above it (the usual mistake, and
// unambiguous), the checkout must carry the .ok stamp `make arlington` writes,
// and Catalog.tsv, which every version of the model has, must be there.
func arlModelDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(testfiles.Arlington.File(t, "Catalog.tsv"))
}

// TestArlingtonParserFaithful checks that pdf0 represents known-conforming
// documents faithfully: the objects its builders emit, and the reference PDF 2.0
// files after a Read->Write round-trip, must all conform to the grammar. A
// finding here is a parser or serializer bug (the inputs are conformant).
func TestArlingtonParserFaithful(t *testing.T) {
	m := loadArlModel(arlModelDir(t))

	check := func(label string, data []byte) {
		findings, err := arlValidate(m, data)
		if err != nil {
			t.Errorf("%s: read-back failed: %v", label, err)
			return
		}
		for _, f := range findings {
			t.Errorf("%s: %s", label, f)
		}
	}

	// Builder output.
	for _, lv := range []struct {
		name string
		l    pdfa.Level
	}{{"PDFA1b", pdfa.PDFA1b}, {"PDFA2b", pdfa.PDFA2b}, {"PDFA3b", pdfa.PDFA3b}, {"PDFA4", pdfa.PDFA4}} {
		var buf bytes.Buffer
		if err := mustPDFADocWithInfo(t, lv.l, "Title", "Author").Write(&buf); err != nil {
			t.Errorf("generate %s: %v", lv.name, err)
			continue
		}
		check("generated "+lv.name, buf.Bytes())
	}

	// Reference PDFs: parse-check and Read->Write round-trip-check, when they
	// are present; present and empty fails.
	var refs []string
	if _, ok, why := testfiles.PDF20Examples.Lookup(t); ok {
		refs = testfiles.PDF20Examples.Glob(t, "*.pdf")
	} else {
		t.Logf("checking builder output only: %s", why)
	}
	for _, f := range refs {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		check("parse "+filepath.Base(f), data)
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Errorf("read %s: %v", filepath.Base(f), err)
			continue
		}
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Errorf("write %s: %v", filepath.Base(f), err)
			continue
		}
		check("round-trip "+filepath.Base(f), buf.Bytes())
	}
}

// arlCorpusExpectedFile lists every finding the oracle makes on a conformant
// (-pass-) veraPDF corpus file, one "file<TAB>finding" a line. Each is a fact
// about the file, checked against its raw bytes, not pdf0 misreading it. The
// header of the file says what they are.
//
// It is a list and not a count. A count lets a parser that newly mangles one
// file hide behind a finding that went away in another; a list fails on both,
// the new finding as a regression and the missing one as a list gone stale.
// After a change that is meant to move it, regenerate with
//
//	go test -run TestArlingtonCorpusParserFaithful -update-arlington .
//
// and read the diff: every added line is a finding to trace to the input
// before it is accepted.
const arlCorpusExpectedFile = "testdata/arlington-corpus-expected.tsv"

var updateArlington = flag.Bool("update-arlington", false, "rewrite "+arlCorpusExpectedFile+" with the findings the oracle makes now")

// TestArlingtonCorpusParserFaithful runs the structural oracle over the whole
// veraPDF corpus. On the conformant (-pass-) files every finding must be one
// arlCorpusExpectedFile lists, and every one it lists must still be found: a
// parser regression adds a line, and a list gone stale loses one. Findings on
// -fail- files are expected and reported for information: the oracle
// independently detects the malformations those files inject.
func TestArlingtonCorpusParserFaithful(t *testing.T) {
	m := loadArlModel(arlModelDir(t))
	corpus := corpusRoot(t)

	var passTotal, failWith int
	got := map[string]bool{}
	// Listed through testfiles, which fails on an empty or symlink-hidden
	// corpus: filepath.Walk from a symlinked root visits nothing, and this
	// test used to report "conformant files=0" and pass (C104).
	for _, p := range testfiles.VeraPDFCorpus.PDFs(t, "") {
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		findings, verr := arlValidate(m, data)
		if verr != nil {
			continue
		}
		if !strings.Contains(filepath.Base(p), "-pass-") {
			if len(findings) > 0 {
				failWith++
			}
			continue
		}
		passTotal++
		rel, _ := filepath.Rel(corpus, p)
		for _, f := range findings {
			got[filepath.ToSlash(rel)+"\t"+f] = true
		}
	}
	if passTotal == 0 {
		t.Fatal("no conformant (-pass-) corpus file reached the oracle; the ratchet checked nothing")
	}

	header, want := readArlExpected(t)
	if *updateArlington {
		lines := make([]string, 0, len(got))
		for l := range got {
			lines = append(lines, l)
		}
		sort.Strings(lines)
		out := header + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(arlCorpusExpectedFile, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d findings to %s", len(lines), arlCorpusExpectedFile)
		return
	}
	var added, gone []string
	for l := range got {
		if !want[l] {
			added = append(added, l)
		}
	}
	for l := range want {
		if !got[l] {
			gone = append(gone, l)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)
	t.Logf("conformant files=%d, findings on them=%d (%s lists %d); malformed files with finding=%d (expected)",
		passTotal, len(got), arlCorpusExpectedFile, len(want), failWith)
	if len(added) > 0 {
		t.Errorf("%d findings on conformant files that %s does not list — a parser faithfulness regression "+
			"unless each is traced to the file's own bytes:\n  %s", len(added), arlCorpusExpectedFile, strings.Join(added, "\n  "))
	}
	if len(gone) > 0 {
		t.Errorf("%d findings %s lists are no longer made; if the change meant that, regenerate the list:\n  %s",
			len(gone), arlCorpusExpectedFile, strings.Join(gone, "\n  "))
	}
}

// readArlExpected reads arlCorpusExpectedFile: its comment header, kept as it
// is when the list is rewritten, and its findings.
func readArlExpected(t *testing.T) (header string, findings map[string]bool) {
	t.Helper()
	data, err := os.ReadFile(arlCorpusExpectedFile)
	if err != nil {
		t.Fatal(err)
	}
	findings = map[string]bool{}
	var h strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "#") || line == "":
			h.WriteString(line + "\n")
		default:
			findings[line] = true
		}
	}
	return h.String(), findings
}

// TestArlingtonOracleHasTeeth confirms the oracle detects structural corruption,
// so a clean TestArlingtonParserFaithful means "faithful", not "checked nothing".
func TestArlingtonOracleHasTeeth(t *testing.T) {
	m := loadArlModel(arlModelDir(t))
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Delete("Pages")                    // required-key check
	cat.Set("Type", object.Name("Bogus"))  // enum check: /Type not in [Catalog]
	cat.Set("Metadata", object.Name("no")) // type check: /Metadata must be a stream, not a name
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	findings, err := arlValidate(m, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"missing required /Pages", "/Type value /Bogus not in [Catalog]", "/Metadata type name"} {
		found := false
		for _, f := range findings {
			if strings.Contains(f, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("oracle did not flag %q; findings=%v", want, findings)
		}
	}
}

// TestArlingtonOracleReachesWhatItUsedToMiss is TestArlingtonOracleHasTeeth
// for the parts of a file the walker did not reach before: the entries of a
// resource map, the values of a number tree, and a page's inherited
// attributes. It also checks that a file is held to the grammar of the version
// it declares, and that a null entry counts as no entry.
func TestArlingtonOracleReachesWhatItUsedToMiss(t *testing.T) {
	m := loadArlModel(arlModelDir(t))
	// build is a one-page document whose page names image Im0, with a
	// structure tree whose number tree holds, under a /Kids node, an element
	// nothing else leads to, and a name tree of destinations; edit changes it
	// before it is written. img is the image stream's own dictionary.
	build := func(version string, edit func(doc *Document, cat, pagesRoot, page, img, elem *object.Dictionary)) []string {
		t.Helper()
		doc := NewDocument()
		doc.Version = version
		imgStream := &object.Stream{Data: []byte{0}}
		imgStream.Dict.Set("Type", object.Name("XObject"))
		imgStream.Dict.Set("Subtype", object.Name("Image"))
		imgStream.Dict.Set("Width", object.Integer(1))
		imgStream.Dict.Set("Height", object.Integer(1))
		imgStream.Dict.Set("ColorSpace", object.Name("DeviceGray"))
		imgStream.Dict.Set("BitsPerComponent", object.Integer(8))
		imgRef := doc.Add(imgStream)
		var b content.Builder
		b.Rect(0, 0, 1, 1).Fill()
		pageRef, err := doc.AddPage(Page{Width: 10, Height: 10, Content: &b,
			XObjects: map[object.Name]object.Object{"Im0": imgRef}})
		if err != nil {
			t.Fatal(err)
		}
		cat := doc.ResolveDict(doc.Trailer.Get("Root"))
		page := doc.ResolveDict(pageRef)
		structElem := func() (*object.Dictionary, object.IndirectRef) {
			e := &object.Dictionary{}
			e.Set("Type", object.Name("StructElem"))
			e.Set("S", object.Name("P"))
			e.Set("Pg", pageRef)
			return e, doc.Add(e)
		}
		top, topRef := structElem()
		elem, elemRef := structElem()
		leaf := &object.Dictionary{}
		leaf.Set("Limits", object.Array{object.Integer(0), object.Integer(0)})
		leaf.Set("Nums", object.Array{object.Integer(0), object.Array{elemRef}})
		parentTree := &object.Dictionary{}
		parentTree.Set("Kids", object.Array{doc.Add(leaf)})
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", topRef)
		root.Set("ParentTree", doc.Add(parentTree))
		rootRef := doc.Add(root)
		top.Set("P", rootRef)
		elem.Set("P", topRef)
		cat.Set("StructTreeRoot", rootRef)
		cat.Set("MarkInfo", object.NewDictionary(object.Entry{Key: "Marked", Value: object.Boolean(true)}))
		dests := &object.Dictionary{}
		dests.Set("Names", object.Array{object.String{Value: []byte("top")}, object.Array{pageRef, object.Name("Fit")}})
		names := &object.Dictionary{}
		names.Set("Dests", doc.Add(dests))
		cat.Set("Names", names)
		pagesRoot := doc.ResolveDict(cat.Get("Pages"))
		if edit != nil {
			edit(doc, cat, pagesRoot, page, &imgStream.Dict, elem)
		}
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Fatal(err)
		}
		findings, err := arlValidate(m, buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return findings
	}
	has := func(findings []string, want string) bool {
		for _, f := range findings {
			if strings.Contains(f, want) {
				return true
			}
		}
		return false
	}

	// The document as built: a destination name tree whose root is not a
	// destination, a structure element reached only through a number tree,
	// an image reached only through a resource map. Nothing to find.
	if f := build("2.0", nil); len(f) != 0 {
		t.Errorf("a well-formed document has findings: %v", f)
	}
	for _, tc := range []struct {
		name, version, want string
		edit                func(doc *Document, cat, pagesRoot, page, img, elem *object.Dictionary)
	}{
		{"an entry of a resource map", "2.0", "Resources/XObject/Im0: missing required /Width",
			func(_ *Document, _, _, _, img, _ *object.Dictionary) { img.Delete("Width") }},
		{"a value of a number tree", "2.0", "ParentTree/Kids[0]/Nums[1][0]: missing required /S",
			func(_ *Document, _, _, _, _, elem *object.Dictionary) { elem.Delete("S") }},
		{"the version the file declares", "1.4", "/Filter value /JPXDecode not in",
			func(_ *Document, _, _, _, img, _ *object.Dictionary) { img.Set("Filter", object.Name("JPXDecode")) }},
		{"an attribute inherited from nowhere", "2.0", "missing required /MediaBox",
			func(_ *Document, _, pagesRoot, page, _, _ *object.Dictionary) {
				page.Delete("MediaBox")
				pagesRoot.Set("MediaBox", object.Null{})
			}},
		// The page is reached first by whichever path the walk takes, through
		// the page tree or a structure element's /Pg; the finding is the same.
		{"a required entry that is null", "2.0", ": missing required /Type",
			func(_ *Document, _, _, page, _, _ *object.Dictionary) { page.Set("Type", object.Null{}) }},
	} {
		if f := build(tc.version, tc.edit); !has(f, tc.want) {
			t.Errorf("%s: no finding %q; findings=%v", tc.name, tc.want, f)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(doc *Document, cat, pagesRoot, page, img, elem *object.Dictionary)
	}{
		{"a value a later version allows, at that version", func(_ *Document, _, _, _, img, _ *object.Dictionary) {
			img.Set("Filter", object.Name("JPXDecode"))
		}},
		{"an attribute inherited from the page tree", func(_ *Document, _, pagesRoot, page, _, _ *object.Dictionary) {
			pagesRoot.Set("MediaBox", page.Get("MediaBox"))
			page.Delete("MediaBox")
		}},
		{"an optional entry that is null", func(_ *Document, cat, _, _, _, _ *object.Dictionary) {
			cat.Set("Lang", object.Null{})
		}},
	} {
		if f := build("2.0", tc.edit); len(f) != 0 {
			t.Errorf("%s: findings %v; want none", tc.name, f)
		}
	}
}

// TestArlingtonModelDeviationsStillDeviate: every row arlModelDeviations
// excuses is still one the model gets wrong. When the model is corrected, the
// entry is dead weight that would excuse a real finding, and this says so.
func TestArlingtonModelDeviationsStillDeviate(t *testing.T) {
	m := loadArlModel(arlModelDir(t))
	for entry := range arlModelDeviations {
		grammar, key, _ := strings.Cut(entry, "/")
		var row *arlRow
		for _, r := range m.spec(grammar) {
			if r.key == key {
				row = &r
			}
		}
		switch {
		case row == nil:
			t.Errorf("%s: the model has no such row any more; remove the entry", entry)
		case row.required != "TRUE":
			t.Errorf("%s: the model now says Required %q, not TRUE; remove the entry", entry, row.required)
		}
	}
}
