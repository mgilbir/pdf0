package lint

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// Other people's work carries their notices.
//
// The repository keeps material it did not write — a font, RelaxNG schemas,
// tables from an RFC, from Adobe's CMaps and glyph lists, examples from the
// PDF specification — and the module zip ships all of it, testdata included.
// Nothing listed it. docs/fonts.md said the repository held no typeface while
// one was tracked (docs audit 2026-10-11 D27); the RelaxNG schemas were kept
// under MIT without the MIT text; the RFC 3454 tables did not carry the notice
// the RFC asks of a derivative work. THIRD_PARTY_NOTICES is the list, and these
// keep it true:
//
//   - every file an entry names exists, and every table it names is declared
//     in its file — an entry for something deleted is a claim about nothing;
//   - every committed file that could be someone else's — anything under a
//     testdata directory, any generated file, any binary file — is named by an
//     entry, or is on ownFiles below, this repository's own;
//   - a notice quoted from a file this repository keeps is that file's text,
//     letter for letter, white space and comment marks aside;
//   - what an entry names under "Local:" — made on a developer's machine from
//     someone else's work that may not be redistributed — is ignored by git
//     and committed nowhere.
//
// A table transcribed by hand into a source file cannot be told apart from
// code by looking; those are found by reading, and once listed are held to
// their file by the table's name.

const noticesFile = "THIRD_PARTY_NOTICES"

// ownFiles are committed files under a testdata directory that this
// repository wrote: scripts and manifests that fetch data sets, the notes
// beside them, and programs that make a fixture. Each glob must match
// something, so the list cannot outlive what it excuses.
var ownFiles = []struct{ glob, why string }{
	{"testdata/*/download.sh", "fetches a data set; the data is not committed"},
	{"testdata/*/sources.tsv", "a fetch manifest: URLs, pins and names, no data"},
	{"testdata/*/README.md", "says what the directory holds and how to use it"},
	{"testdata/cc/sweep.sh", "streams the Common Crawl sweep; nothing is kept"},
	{"testdata/colrv1/skia_render.py", "draws the font's glyphs through Skia for the comparison"},
	{"testdata/colrv1/requirements.txt", "the Python packages skia_render.py needs"},
	{"testdata/fontinstance/oracle.py", "writes oracle.txt"},
	{"testdata/verapdf/verapdf-docker.sh", "runs veraPDF's docker image"},
	{"testdata/verapdf-verdicts/generate.sh", "runs veraPDF over the corpus; its reports are not committed"},
	{"testdata/arlington-corpus-expected.tsv", "the findings pdf0's Arlington test makes on conformant corpus files: file names and pdf0's messages, none of the files' content"},
	{"cmd/pdf0/testdata/signpdf/main.go", "signs a PDF with a key made on the spot, for the CLI's signed-input transcripts"},
}

// noticeEntry is one entry of the file: a title, the files it covers (or,
// under local, the files made from it that must never be committed), where
// they came from, their licence, and the quotations under it.
type noticeEntry struct {
	title   string
	line    int
	files   []noticeFile
	local   []noticeFile
	source  string
	licence string
	quotes  []noticeQuote
}

// noticeFile is one line of an entry's Files: a path or a glob, and, for a
// file that is mostly this repository's own code, the table in it that is not.
type noticeFile struct {
	glob, table string
	line        int
}

// noticeQuote is one "Quoted from <source>:" block.
type noticeQuote struct {
	from string
	text string
	line int
}

var (
	noticeRule      = regexp.MustCompile(`^=+$`)
	noticeQuoteHead = regexp.MustCompile(`^Quoted from (.+):$`)
	noticeFileLine  = regexp.MustCompile(`^(\S+)(?: \(([A-Za-z_][A-Za-z0-9_]*)\))?$`)
	generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)
)

// parseNotices reads the file's entries. Text before the first entry is the
// preamble and is not read.
func parseNotices(data string) []noticeEntry {
	ls := strings.Split(data, "\n")
	var out []noticeEntry
	var cur *noticeEntry
	field := ""
	for i := 0; i < len(ls); i++ {
		l := ls[i]
		switch {
		case noticeRule.MatchString(l) && i+2 < len(ls) && noticeRule.MatchString(ls[i+2]):
			out = append(out, noticeEntry{title: ls[i+1], line: i + 2})
			cur = &out[len(out)-1]
			i += 2
			field = ""
		case cur == nil:
		case strings.HasPrefix(l, "Files:"):
			field = "files"
			cur.files = append(cur.files, noticeFile{glob: strings.TrimSpace(strings.TrimPrefix(l, "Files:")), line: i + 1})
		case strings.HasPrefix(l, "Local:"):
			field = "local"
			cur.local = append(cur.local, noticeFile{glob: strings.TrimSpace(strings.TrimPrefix(l, "Local:")), line: i + 1})
		case strings.HasPrefix(l, "Source:"):
			field = "source"
			cur.source += strings.TrimSpace(strings.TrimPrefix(l, "Source:")) + "\n"
		case strings.HasPrefix(l, "Licence:"):
			field = "licence"
			cur.licence += strings.TrimSpace(strings.TrimPrefix(l, "Licence:")) + "\n"
		case strings.HasPrefix(l, "         ") && strings.TrimSpace(l) != "" && field != "":
			switch field {
			case "files":
				cur.files = append(cur.files, noticeFile{glob: strings.TrimSpace(l), line: i + 1})
			case "local":
				cur.local = append(cur.local, noticeFile{glob: strings.TrimSpace(l), line: i + 1})
			case "source":
				cur.source += strings.TrimSpace(l) + "\n"
			case "licence":
				cur.licence += strings.TrimSpace(l) + "\n"
			}
		default:
			field = ""
			m := noticeQuoteHead.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			q := noticeQuote{from: m[1], line: i + 1}
			// The quotation is the indented block after it, blank lines and
			// all, up to the first line that is neither.
			var body []string
			for i+1 < len(ls) && (strings.HasPrefix(ls[i+1], "    ") || strings.TrimSpace(ls[i+1]) == "") {
				i++
				body = append(body, ls[i])
			}
			q.text = strings.Join(body, "\n")
			cur.quotes = append(cur.quotes, q)
		}
	}
	// A file line names a table in brackets after the path.
	for e := range out {
		for f := range out[e].files {
			if m := noticeFileLine.FindStringSubmatch(out[e].files[f].glob); m != nil {
				out[e].files[f].glob, out[e].files[f].table = m[1], m[2]
			}
		}
	}
	return out
}

// readNotices reads THIRD_PARTY_NOTICES from the repository root, and fails
// when the reading has visibly stopped working: an entry the parser missed is
// an entry nothing checks.
func readNotices(t *testing.T, root string) []noticeEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, noticesFile))
	if err != nil {
		t.Fatalf("reading %s: %v", noticesFile, err)
	}
	data := string(b)
	entries := parseNotices(data)
	if rules := strings.Count(data, "\n"+strings.Repeat("=", 76)+"\n"); rules != 2*len(entries) {
		t.Fatalf("%s has %d rules, which is %d entries, and %d were read", noticesFile, rules, rules/2, len(entries))
	}
	quotes := 0
	for _, e := range entries {
		quotes += len(e.quotes)
		if len(e.files)+len(e.local) == 0 || e.source == "" || e.licence == "" {
			t.Errorf("%s:%d: the entry %q has no Files or Local, or no Source or Licence, this could read", noticesFile, e.line, e.title)
		}
		for _, f := range append(slices.Clip(e.files), e.local...) {
			if !noticeFileLine.MatchString(f.glob) || strings.ContainsAny(f.glob, " ()") {
				t.Errorf("%s:%d: %q is not a path or glob, optionally followed by a table's name in brackets", noticesFile, f.line, f.glob)
			}
		}
	}
	if n := strings.Count(data, "\nQuoted from "); n != quotes {
		t.Fatalf("%s has %d quotations and %d were read", noticesFile, n, quotes)
	}
	if len(entries) < 8 {
		t.Fatalf("%s has %d entries this could read; it has stopped reading them", noticesFile, len(entries))
	}
	return entries
}

// committedFiles is what git tracks, by slash-separated path from the root.
func committedFiles(t *testing.T, root string) []string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required to list the committed files: %v", err)
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) < 100 {
		t.Fatalf("git ls-files listed %d files; it is looking in the wrong place", len(files))
	}
	return files
}

// mayBeThirdParty reports whether a committed file is one that could be
// someone else's, and why: test data, a generated file, or a binary file.
// Content is read the way git decides a file is binary: a NUL in its first
// 8000 bytes.
func mayBeThirdParty(root, f string) (string, error) {
	if strings.HasPrefix(f, "testdata/") || strings.Contains(f, "/testdata/") {
		return "it is test data", nil
	}
	fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f)))
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return "it is a binary file", nil
	}
	if strings.HasSuffix(f, ".go") {
		head := b
		if i := bytes.Index(head, []byte("\npackage ")); i >= 0 {
			head = head[:i]
		}
		if generatedHeader.Match(head) {
			return "it is generated", nil
		}
	}
	return "", nil
}

// TestEveryThirdPartyFileHasANotice.
func TestEveryThirdPartyFileHasANotice(t *testing.T) {
	root := testfiles.Root(t)
	entries := readNotices(t, root)
	files := committedFiles(t, root)

	matches := func(glob string) []string {
		var out []string
		for _, f := range files {
			if ok, _ := path.Match(glob, f); ok {
				out = append(out, f)
			}
		}
		return out
	}

	covered := map[string]bool{}
	for _, e := range entries {
		for _, nf := range e.files {
			if _, err := path.Match(nf.glob, ""); err != nil {
				t.Errorf("%s:%d: %q is not a valid glob: %v", noticesFile, nf.line, nf.glob, err)
				continue
			}
			got := matches(nf.glob)
			if len(got) == 0 {
				t.Errorf("%s:%d: the entry %q names %s, and no committed file is that", noticesFile, nf.line, e.title, nf.glob)
			}
			for _, f := range got {
				covered[f] = true
				if nf.table == "" {
					continue
				}
				b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
				if err != nil {
					t.Fatal(err)
				}
				decl := regexp.MustCompile(`(?m)^(?:var|const|func|type) ` + regexp.QuoteMeta(nf.table) + `\b`)
				if !decl.Match(b) {
					t.Errorf("%s:%d: the entry %q names the table %s in %s, and %s declares no %s", noticesFile, nf.line, e.title, nf.table, f, f, nf.table)
				}
			}
		}
	}

	own := map[string]bool{}
	for _, o := range ownFiles {
		got := matches(o.glob)
		if len(got) == 0 {
			t.Errorf("ownFiles lists %s (%s), and no committed file is that; take it off the list", o.glob, o.why)
		}
		for _, f := range got {
			own[f] = true
		}
	}

	inScope := 0
	for _, f := range files {
		why, err := mayBeThirdParty(root, f)
		if err != nil {
			t.Fatal(err)
		}
		if why == "" {
			continue
		}
		inScope++
		if !covered[f] && !own[f] {
			t.Errorf("%s is committed and %s, and no entry of %s names it. If it came from elsewhere, "+
				"add an entry saying where from and under which licence; if this repository wrote it, add it to ownFiles in internal/lint/notices_test.go",
				f, why, noticesFile)
		}
	}
	// The testdata files, the generated RFC tables and the two ICC profiles.
	if inScope < 30 {
		t.Fatalf("only %d committed files were in scope; this has stopped looking", inScope)
	}
}

// TestLocalFilesAreNeverCommitted. An entry's "Local:" names what is made on
// a developer's machine from someone else's work that may not be passed on:
// the ISO 32000 examples, extracted from the hand-placed specifications into
// testdata/spec-examples/ (they were committed, and shipped in every module
// zip, until 2026-10). Each must be ignored by git, so that `git add -A`
// cannot take it, and no committed file may be one.
func TestLocalFilesAreNeverCommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required to check .gitignore: %v", err)
	}
	root := testfiles.Root(t)
	files := committedFiles(t, root)
	n := 0
	for _, e := range readNotices(t, root) {
		for _, l := range e.local {
			n++
			for _, f := range files {
				if ok, _ := path.Match(l.glob, f); ok {
					t.Errorf("%s:%d: %s is committed, and the entry %q says what %s names must never be", noticesFile, l.line, f, e.title, l.glob)
				}
			}
			// A path the glob names: each wildcard made a letter.
			p := strings.NewReplacer("*", "x", "?", "x").Replace(l.glob)
			cmd := exec.Command("git", "check-ignore", "-q", "--no-index", "--", p)
			cmd.Dir = root
			err := cmd.Run()
			var exit *exec.ExitError
			switch {
			case err == nil:
			case errors.As(err, &exit) && exit.ExitCode() == 1:
				t.Errorf("%s:%d: the entry %q says %s must never be committed, and .gitignore does not cover it (%s)", noticesFile, l.line, e.title, l.glob, p)
			default:
				t.Fatalf("git check-ignore %s: %v", p, err)
			}
		}
	}
	if n == 0 {
		t.Fatalf("no entry of %s names a Local file; the ISO examples' entry does, so this has stopped reading them", noticesFile)
	}
}

// TestEveryNoticeQuotedFromHereIsWhatTheFileSays. A quotation whose source is
// a file this repository keeps is checked against it. One quoted from upstream
// names a URL and a pin, which a test without the network cannot read; those
// are counted, not checked.
func TestEveryNoticeQuotedFromHereIsWhatTheFileSays(t *testing.T) {
	root := testfiles.Root(t)
	entries := readNotices(t, root)
	tracked := map[string]bool{}
	for _, f := range committedFiles(t, root) {
		tracked[f] = true
	}
	checked, upstream := 0, 0
	for _, e := range entries {
		for _, q := range e.quotes {
			if strings.Contains(q.from, "://") || strings.ContainsAny(q.from, " ") || strings.HasPrefix(q.from, "/") {
				upstream++
				continue
			}
			if !tracked[q.from] {
				t.Errorf("%s:%d quotes %s, which is not a committed file", noticesFile, q.line, q.from)
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(q.from)))
			if err != nil {
				t.Fatal(err)
			}
			got, want := squeezeNotice(q.text), squeezeNotice(string(b))
			switch {
			case len(got) < 40:
				t.Errorf("%s:%d quotes almost nothing of %s", noticesFile, q.line, q.from)
			case !strings.Contains(want, got):
				t.Errorf("%s:%d says it quotes %s, and the text is not in it", noticesFile, q.line, q.from)
			}
			checked++
		}
	}
	t.Logf("%d quotations checked against the files they quote; %d quoted from upstream", checked, upstream)
	if checked < 3 {
		t.Fatalf("only %d quotations from committed files were found; this has stopped reading them", checked)
	}
}

// squeezeNotice is text as a quotation is compared: each line without the
// comment marks a source writes its header in ("//", "#", "%"), and no white
// space at all — a notice rewrapped is the same notice, and the check is about
// the words.
func squeezeNotice(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimLeft(l, " \t")
		l = strings.TrimPrefix(l, "//")
		l = strings.TrimLeft(l, "#%")
		for _, r := range l {
			switch r {
			case ' ', '\t', '\r', '\f', '\v', '\u00a0', '\ufeff':
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestTheNoticesParserReadsWhatIsThere. The checks above are only as good as
// the reading, so the reading is checked on a file whose every part is known.
func TestTheNoticesParserReadsWhatIsThere(t *testing.T) {
	rule := strings.Repeat("=", 76)
	data := "PREAMBLE\n\nFiles:   not/an/entry\n\n" +
		rule + "\nFirst\n" + rule + "\n\n" +
		"Files:   a/b.go (tableA)\n         c/*.txt\n" +
		"Source:  https://example.org,\n         at abc\n" +
		"Licence: MIT\n\nBody text.\n\nQuoted from c/x.txt:\n\n    one\n\n    two\nAfter.\n\n" +
		rule + "\nSecond\n" + rule + "\n\n" +
		"Files:   d.bin\nSource:  here\nLicence: none\n         stated\n\n" +
		rule + "\nThird\n" + rule + "\n\n" +
		"Local:   gen/*.json\n         gen2/x\nSource:  made here\nLicence: none\n"
	got := parseNotices(data)
	if len(got) != 3 {
		t.Fatalf("read %d entries, want 3: %+v", len(got), got)
	}
	a, b, c := got[0], got[1], got[2]
	if a.title != "First" || len(a.files) != 2 || a.files[0].glob != "a/b.go" || a.files[0].table != "tableA" ||
		a.files[1].glob != "c/*.txt" || a.files[1].table != "" {
		t.Errorf("first entry's files read as %+v", a.files)
	}
	if a.source != "https://example.org,\nat abc\n" || a.licence != "MIT\n" {
		t.Errorf("first entry's source and licence read as %q, %q", a.source, a.licence)
	}
	if len(a.quotes) != 1 || a.quotes[0].from != "c/x.txt" || squeezeNotice(a.quotes[0].text) != "onetwo" {
		t.Errorf("first entry's quotations read as %+v", a.quotes)
	}
	if b.title != "Second" || len(b.files) != 1 || b.files[0].glob != "d.bin" || b.licence != "none\nstated\n" || len(b.quotes) != 0 || len(b.local) != 0 {
		t.Errorf("second entry read as %+v", b)
	}
	if c.title != "Third" || len(c.files) != 0 || len(c.local) != 2 || c.local[0].glob != "gen/*.json" || c.local[1].glob != "gen2/x" ||
		c.source != "made here\n" {
		t.Errorf("third entry read as %+v", c)
	}
}
