package hostile

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The self-tests plant each failure mode in a real child and assert the parent
// classifies it. Their caps are small so a planted bomb cannot destabilise the
// machine running the suite.
var small = Limits{MaxRSS: 256 << 20, Timeout: 10 * time.Second}

// sink keeps allocations reachable so the compiler and the GC cannot drop them.
var sink [][]byte

// touch allocates n bytes and writes to every page, so they are resident.
func touch(n int) []byte {
	b := make([]byte, n)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	return b
}

//go:noinline
func recurse(n int) int {
	var pad [512]byte
	pad[n%len(pad)] = byte(n)
	return recurse(n+1) + int(pad[0])
}

var spinCounter int

func TestClassifiesOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		lim   Limits
		fn    func(t *testing.T)
		want  Kind
		crash string // substring of Outcome.Crash
		log   string // substring of Outcome.Output
	}{
		{
			name: "well behaved",
			lim:  small,
			fn: func(t *testing.T) {
				sink = append(sink, touch(8<<20))
				t.Log("child says hello")
			},
			want: OK,
			log:  "child says hello",
		},
		{
			// The unanchored pattern "well_behaved" would select this and the
			// case above in one child, and the second to reach Run would fail.
			name: "well behaved too",
			lim:  small,
			fn:   func(t *testing.T) {},
			want: OK,
		},
		{
			// Every regexp metacharacter the testing package's pattern
			// splitter treats specially, and one it does not.
			name: `meta [x](y)|z\.*+?^$ {1}`,
			lim:  small,
			fn:   func(t *testing.T) { t.Log("meta ran") },
			want: OK,
			log:  "meta ran",
		},
		{
			name: "allocation bomb",
			lim:  small,
			fn: func(t *testing.T) {
				for {
					sink = append(sink, touch(1<<20))
				}
			},
			want: OverMemory,
		},
		{
			// Over the cap and gone before the parent necessarily polls: the
			// kernel's peak accounting after exit still catches it.
			name: "allocation spike then return",
			lim:  small,
			fn: func(t *testing.T) {
				sink = append(sink, touch(400<<20))
				sink = nil
			},
			want: OverMemory,
		},
		{
			name:  "infinite recursion",
			lim:   small,
			fn:    func(t *testing.T) { recurse(0) },
			want:  Fatal,
			crash: "fatal error: stack overflow",
		},
		{
			name: "spin loop",
			lim:  Limits{MaxRSS: 256 << 20, Timeout: 2 * time.Second},
			fn: func(t *testing.T) {
				for {
					spinCounter++
				}
			},
			want: Timeout,
		},
		{
			name:  "plain panic",
			lim:   small,
			fn:    func(t *testing.T) { panic("planted panic") },
			want:  Fatal,
			crash: "panic: planted panic",
		},
		{
			name: "panic in another goroutine",
			lim:  small,
			fn: func(t *testing.T) {
				done := make(chan struct{})
				go func() { defer close(done); var m map[string]int; m["x"] = 1 }()
				<-done
			},
			want:  Fatal,
			crash: "panic: assignment to entry in nil map",
		},
		{
			name: "failed assertion",
			lim:  small,
			fn:   func(t *testing.T) { t.Errorf("planted assertion failure") },
			want: Failed,
			log:  "planted assertion failure",
		},
		{
			name: "fatal assertion",
			lim:  small,
			fn:   func(t *testing.T) { t.Fatalf("planted FailNow") },
			want: Failed,
			log:  "planted FailNow",
		},
		{
			name: "skip",
			lim:  small,
			fn:   func(t *testing.T) { t.Skip("planted skip") },
			want: Skipped,
			log:  "planted skip",
		},
		{
			// Exit status 0 without the body finishing: the parent must not
			// read that as a pass.
			name: "exit zero mid-test",
			lim:  small,
			fn:   func(t *testing.T) { os.Exit(0) },
			want: NotRun,
		},
		{
			name: "exit non-zero mid-test",
			lim:  small,
			fn:   func(t *testing.T) { os.Exit(3) },
			want: Fatal,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := run(t, c.lim, c.fn)
			if InChild() {
				return
			}
			t.Logf("outcome: %s", out)
			if out.Kind != c.want {
				t.Fatalf("classified %v, want %v\noutput:\n%s", out.Kind, c.want, out.Output)
			}
			if c.crash != "" && !strings.Contains(out.Crash, c.crash) {
				t.Errorf("crash line %q, want it to contain %q", out.Crash, c.crash)
			}
			if c.log != "" && !strings.Contains(out.Output, c.log) {
				t.Errorf("captured output lacks %q:\n%s", c.log, out.Output)
			}
			switch c.want {
			case OverMemory:
				if rssSupported && out.PeakRSS <= out.Limits.MaxRSS {
					t.Errorf("OverMemory with peak %d under the cap %d", out.PeakRSS, out.Limits.MaxRSS)
				}
			case Timeout:
				if out.Elapsed < out.Limits.Timeout {
					t.Errorf("Timeout after only %v, limit %v", out.Elapsed, out.Limits.Timeout)
				}
			}
		})
	}
}

// The parent keeps a bounded head and tail of the child's output, however much
// the child writes, and the tail is the end of the stream.
func TestOutputIsBounded(t *testing.T) {
	out := run(t, small, func(t *testing.T) {
		line := strings.Repeat("x", 1023) + "\n"
		for range 16 << 10 { // 16 MiB
			os.Stdout.WriteString(line)
		}
		t.Fatalf("the last word")
	})
	if InChild() {
		return
	}
	if out.Kind != Failed {
		t.Fatalf("classified %v, want Failed", out.Kind)
	}
	// A literal, not headCap+tailCap: the bound is the property under test, so
	// it must not move with the constants it checks.
	if n := len(out.Output); n > 48<<10 {
		t.Errorf("captured %d bytes of 16 MiB, want at most 48 KiB", n)
	}
	if !strings.Contains(out.Output, "the last word") {
		t.Errorf("the tail lost the end of the output")
	}
	if !strings.Contains(out.Output, "bytes of output omitted") {
		t.Errorf("the omitted middle is not marked")
	}
}

// Run itself, on the passing path.
func TestRunPasses(t *testing.T) {
	Run(t, small, func(t *testing.T) {
		if !InChild() {
			t.Fatalf("fn ran in the parent")
		}
	})
}

// A zero Limits takes the defaults; it never means "uncapped".
func TestZeroLimitsAreCapped(t *testing.T) {
	if l := (Limits{}).withDefaults(); l.MaxRSS != DefaultMaxRSS || l.Timeout != DefaultTimeout || l.MaxStack != DefaultMaxStack {
		t.Fatalf("zero Limits became %+v", l)
	}
	if l := (Limits{}).effective(); l.MaxRSS != DefaultMaxRSS*raceScale || l.Timeout != DefaultTimeout*raceScale || l.MaxStack != DefaultMaxStack {
		t.Fatalf("zero Limits became %+v under Run", l)
	}
}

// RunPattern selects exactly one name at each level, as the testing package
// applies it: split on "/", each element matched against one level.
func TestRunPattern(t *testing.T) {
	names := []string{"TestA", "TestA/sub", "TestA/sub_2", "TestAB/sub", `TestA/[x](y)|z\.*`, "TestA/sub/deeper"}
	for _, name := range names {
		pat := RunPattern(name)
		elems := strings.Split(pat, "/")
		for _, other := range names {
			levels := strings.Split(other, "/")
			match := len(levels) >= len(elems)
			for i := 0; match && i < len(elems); i++ {
				match = regexp.MustCompile(elems[i]).MatchString(levels[i])
			}
			// A pattern for a parent selects the parent's subtests too; that
			// is how -test.run reaches a subtest at all.
			want := other == name || strings.HasPrefix(other, name+"/")
			if match != want {
				t.Errorf("RunPattern(%q) = %q matches %q: %v, want %v", name, pat, other, match, want)
			}
		}
	}
}
