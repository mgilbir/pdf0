package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The resource policy.
//
// This is the only place in the engine that can read anything the caller did
// not hand it, so every refusal it makes is tested by making the thing it
// refuses actually exist. A test that asks for "../secret.txt" in a directory
// with no secret in it proves nothing: it would pass against a resolver that
// happily read whatever it was pointed at and simply found nothing there.
//
// Each case below therefore plants a real file outside the root, with real
// content, and requires that the content does not come back.

// planted builds a directory tree with something worth stealing outside it.
//
// The layout is:
//
//	<tmp>/secret.txt        the file that must never be read
//	<tmp>/root/             what the resolver is rooted at
//	<tmp>/root/inside.txt   a file it may read
//	<tmp>/root/escape.txt   a symlink to ../secret.txt
//	<tmp>/root/local.txt    a symlink to inside.txt
func planted(t *testing.T) (root string, resolver *DirResolver) {
	t.Helper()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("INSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("this filesystem does not support symbolic links: %v", err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "local.txt")); err != nil {
		t.Fatal(err)
	}
	r, err := NewDirResolver(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return root, r
}

// TestDirResolverReadsWhatItMay is the positive half. Without it the refusals
// below would be satisfied by a resolver that refuses everything.
func TestDirResolverReadsWhatItMay(t *testing.T) {
	_, res := planted(t)
	for _, ref := range []string{"inside.txt", "./inside.txt", "local.txt"} {
		got, err := res.Resolve(ref)
		if err != nil {
			t.Errorf("%q: %v", ref, err)
			continue
		}
		if string(got) != "INSIDE" {
			t.Errorf("%q read %q, want INSIDE", ref, got)
		}
	}
}

// TestDirResolverRefusesEscapes is the containment guarantee, one refusal per
// mechanism. The file being reached for exists and holds SECRET, so a resolver
// that let any of these through would return it.
//
// Each case names the *reason* it expects, and that is not decoration. Several
// of these are refused twice over — once by the name check and once by os.Root
// at the system call — and a test that only asked whether an error came back
// would pass with either half removed, which is how a defence in depth quietly
// becomes a defence in one.
func TestDirResolverRefusesEscapes(t *testing.T) {
	root, res := planted(t)
	base := filepath.Dir(root)
	absolute := filepath.Join(base, "secret.txt")

	// The planted secret really is reachable through the symbolic link from
	// outside the resolver. Without this the symlink case below would pass
	// against a broken link, which proves nothing at all about containment.
	if got, err := os.ReadFile(filepath.Join(root, "escape.txt")); err != nil || string(got) != "SECRET" {
		t.Fatalf("the planted symlink does not reach the secret (%q, %v); this "+
			"test would pass without the resolver refusing anything", got, err)
	}

	cases := []struct {
		name string
		ref  string
		why  string
	}{
		{"parent directory", "../secret.txt", "leaves the directory"},
		{"parent directory, buried", "sub/../../secret.txt", "leaves the directory"},
		{"absolute path", absolute, "absolute path"},
		{"absolute path, rooted", "/etc/hostname", "absolute path"},
		// The one refusal this package does not make itself: os.Root resolves
		// every component against the directory at the system call, so a
		// symbolic link out of it is refused by the kernel rather than by a
		// string comparison — which is the whole reason os.Root is used.
		{"symbolic link out of the root", "escape.txt", "escapes"},
		{"windows separator", `..\secret.txt`, "leaves the directory"},
	}
	for _, tc := range cases {
		got, err := res.Resolve(tc.ref)
		if err == nil {
			t.Errorf("%s: %q was read and returned %q; it must be refused",
				tc.name, tc.ref, got)
			continue
		}
		if strings.Contains(string(got), "SECRET") {
			t.Errorf("%s: %q returned the secret alongside an error", tc.name, tc.ref)
		}
		if !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: %q was refused with %q, which does not mention %q — so "+
				"the check meant to catch it may not be the one that did",
				tc.name, tc.ref, err, tc.why)
		}
	}
}

// TestDirResolverRefusesSchemes pins that a URL is never turned into a path.
//
// The engine has no network and must not acquire one by a caller writing a
// resolver that hands the string to something that does — which is why the
// scheme is rejected here rather than left to the resolver's own judgement.
func TestDirResolverRefusesSchemes(t *testing.T) {
	_, res := planted(t)
	for _, ref := range []string{
		"http://example.com/x.png",
		"https://example.com/x.png",
		"file:///etc/hostname",
		"ftp://example.com/x.png",
		"HTTP://EXAMPLE.COM/x.png",
		"c:/windows/win.ini",
	} {
		if _, err := res.Resolve(ref); err == nil {
			t.Errorf("%q was accepted; a reference with a scheme must be refused", ref)
		} else if !strings.Contains(err.Error(), "scheme") {
			t.Errorf("%q was refused for the wrong reason: %v", ref, err)
		}
	}
}

// TestResourcePathAcceptsWhatItShould keeps the scheme check from becoming a
// blanket refusal: a file name with a colon in it after the first slash, a
// query string, and a per-cent escape are all ordinary references.
func TestResourcePathAcceptsWhatItShould(t *testing.T) {
	cases := map[string]string{
		"support/blue.png":       "support/blue.png",
		"support/blue.png?v=2":   "support/blue.png",
		"support/blue.png#frag":  "support/blue.png",
		"support/blue%20sky.png": "support/blue sky.png",
		"a/b/c.png":              "a/b/c.png",
		"./x.png":                "./x.png",
	}
	for ref, want := range cases {
		got, err := resourcePath(ref)
		if err != nil {
			t.Errorf("%q: %v", ref, err)
			continue
		}
		if got != want {
			t.Errorf("%q became %q, want %q", ref, got, want)
		}
	}
}

// TestDirResolverSizeCap plants a file over the cap and requires the read to be
// refused.
//
// The cap is set on the resolver rather than by lowering the package variable,
// so what is tested is the value a caller can actually choose. The file is one
// byte over: a test that used a file far below the cap would pass with the cap
// deleted, which is the shape of failure this repository has met before.
func TestDirResolverSizeCap(t *testing.T) {
	dir := t.TempDir()
	const cap = 1024
	big := make([]byte, cap+1)
	small := make([]byte, cap)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.bin"), small, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	res.WithMaxBytes(cap)

	if got, err := res.Resolve("small.bin"); err != nil || len(got) != cap {
		t.Fatalf("a file exactly at the cap was refused: %d bytes, %v", len(got), err)
	}
	if got, err := res.Resolve("big.bin"); err == nil {
		t.Errorf("a file one byte over the cap was read: %d bytes", len(got))
	}
}

// TestDirResolverRefusesZeroCap pins that a cap of zero does not switch the cap
// off, which is what a zero value in a configuration struct would otherwise do.
func TestDirResolverRefusesZeroCap(t *testing.T) {
	dir := t.TempDir()
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	before := res.max
	res.WithMaxBytes(0)
	if res.max != before {
		t.Errorf("a cap of zero changed the limit to %d; it must be ignored", res.max)
	}
	res.WithMaxBytes(-1)
	if res.max != before {
		t.Errorf("a negative cap changed the limit to %d; it must be ignored", res.max)
	}
}

// TestDirResolverRefusesNonRegularFiles keeps a resolver from being pointed at
// something with no end.
//
// A directory stands in for the general case, which includes a named pipe and a
// character device: none has a size, so the size cap cannot save the read, and
// /dev/zero through a resolver rooted at /dev would produce bytes until the
// process died.
//
// The reason is asserted rather than only the refusal, because reading a
// directory fails on its own on every system this runs on — so a test that
// asked only whether an error came back would pass with the check deleted, and
// the named pipe that the check actually exists for would then block the render
// for ever. That was found by planting exactly that defect.
func TestDirResolverRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	_, err = res.Resolve("sub")
	if err == nil {
		t.Fatal("a directory was read as a resource")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory was refused with %q rather than for not being a "+
			"regular file; the check that stops a named pipe may be gone", err)
	}
}

// TestNoResolverLoadsNothing is the deny-by-default guarantee, checked through
// the whole pipeline rather than on the resolver: an Input with no resolver
// must not load an image even when the file is sitting there and the reference
// is correct.
func TestNoResolverLoadsNothing(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "x.png"), 15, 15)

	built := Build(Input{HTML: `<img id="i" src="x.png">`})
	box := findBox(t, built.Root, "i")
	if box.Replaced != nil {
		t.Fatal("an image was loaded with no resolver configured")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "no resource resolver")
	fired[RuleResourceBlocked] = true
}

// requireFinding asserts that a rule fired with a message mentioning want.
func requireFinding(t *testing.T, findings []Finding, rule Rule, want string) {
	t.Helper()
	for _, f := range findings {
		if f.Rule == rule && strings.Contains(f.Message, want) {
			return
		}
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.Error())
	}
	t.Fatalf("no %s finding mentioning %q; got:\n%s", rule, want, strings.Join(got, "\n"))
}

// findBox returns the box generated by the element with the given id.
func findBox(t *testing.T, root *Box, id string) *Box {
	t.Helper()
	var found *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if found != nil || b == nil {
			return
		}
		if b.Element != nil {
			if got, _ := b.Element.Attr("id"); got == id {
				found = b
				return
			}
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no box for #%s", id)
	}
	return found
}
