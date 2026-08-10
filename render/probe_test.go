package render

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// Throwaway diagnostic probe. Deleted before committing.

func TestProbeFailingByDir(t *testing.T) {
	root := wptDir(t)
	tests := findReftests(t, root)
	byDir := map[string]int{}
	var all []string
	for _, rt := range tests {
		got, _, err := renderForCompare(root, rt.test)
		if err != nil {
			continue
		}
		want, _, err := renderForCompare(root, rt.ref)
		if err != nil {
			continue
		}
		if pictureEqual(got, want, pageClip()) != rt.mismatch {
			continue
		}
		dir := rt.name
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			dir = dir[:i]
		}
		byDir[dir]++
		all = append(all, rt.name)
	}
	type kv struct {
		d string
		n int
	}
	var ks []kv
	for d, n := range byDir {
		ks = append(ks, kv{d, n})
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].n > ks[j].n })
	for _, k := range ks[:min(30, len(ks))] {
		t.Logf("%4d %s", k.n, k.d)
	}
	sort.Strings(all)
	os.WriteFile("/tmp/claude-1000/-home-mgilbir-Projects-mgilbir-pdf0/f480077b-25c9-477c-a844-55ee4ffaae4d/scratchpad/failing.txt",
		[]byte(strings.Join(all, "\n")), 0o644)
}

// TestProbeStatus reports pass/vacuous/fail for every test matching PROBE.
func TestProbeStatus(t *testing.T) {
	root := wptDir(t)
	name := os.Getenv("PROBE")
	if name == "" {
		t.Skip("set PROBE")
	}
	tests := findReftests(t, root)
	var clean, vac, fail int
	for _, rt := range tests {
		if !strings.Contains(rt.name, name) {
			continue
		}
		got, gc, err := renderForCompare(root, rt.test)
		if err != nil {
			t.Logf("%s: BROKE %v", rt.name, err)
			continue
		}
		want, wc, err := renderForCompare(root, rt.ref)
		if err != nil {
			t.Logf("%s: BROKE %v", rt.name, err)
			continue
		}
		passed := pictureEqual(got, want, pageClip()) != rt.mismatch
		switch {
		case !passed:
			fail++
			t.Logf("FAIL    %s (clean %v/%v)", rt.name, gc, wc)
		case gc && wc:
			clean++
			t.Logf("CLEAN   %s", rt.name)
		default:
			vac++
			t.Logf("VACUOUS %s (clean %v/%v)", rt.name, gc, wc)
		}
	}
	t.Logf("== %d clean, %d vacuous, %d fail", clean, vac, fail)
}

// TestProbeDump dumps the display lists of one named test pair.
func TestProbeDump(t *testing.T) {
	root := wptDir(t)
	name := os.Getenv("PROBE")
	if name == "" {
		t.Skip("set PROBE")
	}
	tests := findReftests(t, root)
	for _, rt := range tests {
		if !strings.Contains(rt.name, name) {
			continue
		}
		got, gc, err := renderForCompare(root, rt.test)
		if err != nil {
			t.Fatal(err)
		}
		want, wc, err := renderForCompare(root, rt.ref)
		if err != nil {
			t.Fatal(err)
		}
		eq := pictureEqual(got, want, pageClip())
		t.Logf("=== %s  equal=%v mismatch=%v clean=%v/%v", rt.name, eq, rt.mismatch, gc, wc)
		t.Logf("--- TEST %s\n%s", rt.test, dumpOps(got))
		t.Logf("--- REF  %s\n%s", rt.ref, dumpOps(want))
	}
}

func dumpOps(ops []Op) string {
	var b strings.Builder
	for _, op := range ops {
		switch v := op.(type) {
		case FillRect:
			fmt.Fprintf(&b, "  fill   %s %s overhang=%v\n", rectKey(v.Rect), v.Color, v.Overhang)
		case DrawText:
			fmt.Fprintf(&b, "  text   %q at %s,%s size %s\n", v.Text, num(v.At.X), num(v.At.Y), num(v.Size))
		case DrawImage:
			fmt.Fprintf(&b, "  image  %s %s\n", v.Key, rectKey(v.Rect))
		case TileImage:
			fmt.Fprintf(&b, "  tile   %s clip %s tile %s\n", v.Key, rectKey(v.Clip), rectKey(v.Tile))
		default:
			fmt.Fprintf(&b, "  %T %+v\n", op, op)
		}
	}
	return b.String()
}

// TestProbeFindings prints the findings raised for one document.
func TestProbeFindings(t *testing.T) {
	root := wptDir(t)
	name := os.Getenv("PROBE")
	if name == "" {
		t.Skip("set PROBE")
	}
	tests := findReftests(t, root)
	for _, rt := range tests {
		if !strings.Contains(rt.name, name) {
			continue
		}
		for _, f := range []string{rt.test, rt.ref} {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src := cdataRe.ReplaceAllString(string(data), "$1")
			res, err := newSuiteResolver(root, dirOf(f))
			if err != nil {
				t.Fatal(err)
			}
			built := Build(Input{HTML: src, Resources: res})
			rec := NewRecorder(nil)
			Layout(built.Root, A4.Content(), fontSetForWPT(), rec)
			t.Logf("--- %s", f)
			for _, fi := range built.Findings {
				t.Logf("   build %v %s unsupported=%v", fi.Rule, fi.Message, fi.Unsupported())
			}
			for _, fi := range rec.Findings() {
				t.Logf("   lay   %v %s unsupported=%v", fi.Rule, fi.Message, fi.Unsupported())
			}
			res.Close()
		}
	}
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}
