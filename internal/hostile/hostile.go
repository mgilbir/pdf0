package hostile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Limits bounds the child process. A zero field takes its default, never
// "unlimited": a harness whose zero value removed the cap would be one typo
// away from the in-process test it replaces.
type Limits struct {
	// MaxRSS caps the child's resident set size in bytes (Linux only; see the
	// package doc). Default DefaultMaxRSS.
	MaxRSS int64
	// Timeout caps the child's wall-clock run time, including process start-up.
	// Default DefaultTimeout.
	Timeout time.Duration
	// MaxStack is passed to debug.SetMaxStack in the child, so a runaway
	// recursion becomes a fast fatal stack overflow rather than a climb to the
	// runtime's 1 GB default. Default DefaultMaxStack.
	MaxStack int
}

// Defaults for a zero Limits field.
const (
	DefaultMaxRSS   int64         = 1 << 30
	DefaultTimeout  time.Duration = 60 * time.Second
	DefaultMaxStack int           = 64 << 20
)

// pollInterval is how often the parent samples the child's memory. At a few
// GB/s of fresh allocation it bounds the overshoot past MaxRSS to tens of
// megabytes before the kill lands.
var pollInterval = 5 * time.Millisecond // a var only so a self-test can take polling out of the picture

// Output capture bounds. The parent keeps the head and the tail of the child's
// combined stdout and stderr and drops the middle, so its own memory does not
// grow with the child's output.
const (
	headCap = 8 << 10
	tailCap = 32 << 10
	lineCap = 512 // longest line prefix inspected for sentinels and crash lines
)

// Kind classifies how the child ended. See the package doc.
type Kind int

const (
	OK Kind = iota
	OverMemory
	Timeout
	Fatal
	Failed
	Skipped
	NotRun
)

func (k Kind) String() string {
	switch k {
	case OK:
		return "ok"
	case OverMemory:
		return "over memory"
	case Timeout:
		return "timeout"
	case Fatal:
		return "fatal"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	case NotRun:
		return "not run"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Outcome is what the parent observed.
type Outcome struct {
	Kind Kind
	// PeakRSS is the child's peak resident set in bytes, or 0 where the
	// platform does not report it.
	PeakRSS int64
	// Elapsed is the child's wall-clock run time.
	Elapsed time.Duration
	// ExitCode is the child's exit status, or -1 if a signal ended it.
	ExitCode int
	// Crash is the runtime's summary of how the child died: the first
	// "fatal error: ..." or "panic: ..." line, else the first "runtime: ..."
	// line, else the signal. Empty for a child that exited normally.
	Crash string
	// Output is the captured head and tail of the child's -test.v output.
	Output string
	// Limits is what was enforced, after defaults and race scaling.
	Limits Limits
}

func (o Outcome) String() string {
	switch o.Kind {
	case OverMemory:
		return fmt.Sprintf("over memory (peak %s, cap %s) after %v", mib(o.PeakRSS), mib(o.Limits.MaxRSS), o.Elapsed.Round(time.Millisecond))
	case Timeout:
		return fmt.Sprintf("timeout: still running after %v (peak %s)", o.Limits.Timeout, mib(o.PeakRSS))
	case Fatal:
		c := o.Crash
		if c == "" {
			c = "no crash line captured"
		}
		return fmt.Sprintf("fatal: exit %d after %v: %s", o.ExitCode, o.Elapsed.Round(time.Millisecond), c)
	case Failed:
		return fmt.Sprintf("failed: an assertion in the subprocess failed (exit %d)", o.ExitCode)
	case NotRun:
		return fmt.Sprintf("not run: the subprocess exited %d without running the test body", o.ExitCode)
	}
	return fmt.Sprintf("%v (peak %s, %v)", o.Kind, mib(o.PeakRSS), o.Elapsed.Round(time.Millisecond))
}

func mib(n int64) string {
	if n <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

// envChild names the environment marker. Its value is "<nonce> <test name>".
const envChild = "PDF0_HOSTILE_CHILD"

// Run runs fn under lim in a child process and fails t if the child breaches a
// limit, crashes, or fails an assertion. In the child it simply runs fn. See
// the package doc.
func Run(t *testing.T, lim Limits, fn func(t *testing.T)) {
	t.Helper()
	out := run(t, lim.effective(), fn)
	if InChild() {
		return
	}
	switch out.Kind {
	case OK:
		t.Logf("hostile: ok (peak %s, %v)", mib(out.PeakRSS), out.Elapsed.Round(time.Millisecond))
		return
	case Skipped:
		t.Skipf("hostile: the subprocess skipped:\n%s", out.Output)
	}
	t.Logf("hostile: subprocess output:\n%s", out.Output)
	t.Fatalf("hostile: %s", out)
}

// RaceScale is the factor Run scales MaxRSS and Timeout by: 8 under the race
// detector, 1 otherwise. A test that asserts a wall-clock bound inside fn,
// which Run does not scale, multiplies the bound by it.
const RaceScale = raceScale

// InChild reports whether this process is a hostile child. Code after a run
// call executes in the child too; the self-tests use this to keep their
// assertions on the Outcome in the parent.
func InChild() bool { return os.Getenv(envChild) != "" }

var (
	usedMu sync.Mutex
	used   = map[*testing.T]bool{}
)

// run is Run without the verdict: the self-tests observe the Outcome.
func run(t *testing.T, lim Limits, fn func(t *testing.T)) Outcome {
	t.Helper()
	lim = lim.withDefaults()

	usedMu.Lock()
	again := used[t]
	used[t] = true
	usedMu.Unlock()
	if again {
		t.Fatalf("hostile: Run called twice in %s; use one subtest per hostile case", t.Name())
	}
	t.Cleanup(func() {
		usedMu.Lock()
		delete(used, t)
		usedMu.Unlock()
	})

	if v := os.Getenv(envChild); v != "" {
		return runChild(t, v, lim, fn)
	}
	return runParent(t, lim)
}

// withDefaults fills the zero fields.
func (l Limits) withDefaults() Limits {
	if l.MaxRSS <= 0 {
		l.MaxRSS = DefaultMaxRSS
	}
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	if l.MaxStack <= 0 {
		l.MaxStack = DefaultMaxStack
	}
	return l
}

// effective is what Run enforces: the defaults, then the race-detector scale.
// run does not scale, so the self-tests exercise exactly the caps they state.
func (l Limits) effective() Limits {
	l = l.withDefaults()
	l.MaxRSS *= raceScale
	l.Timeout *= raceScale
	return l
}

// The child prints these, prefixed by the parent's random nonce, so ordinary
// test output cannot be mistaken for them.
const (
	sentinelStart = "start"
	sentinelDone  = "done"
	sentinelSkip  = "skip"
	sentinelPeak  = "peak " // followed by the child's own VmHWM in bytes
)

func runChild(t *testing.T, marker string, lim Limits, fn func(t *testing.T)) Outcome {
	t.Helper()
	nonce, name, ok := strings.Cut(marker, " ")
	if !ok || name != t.Name() {
		// A different test reached Run in the child: -test.run selected more
		// than one test, or Run is nested inside another Run. Neither is
		// supported.
		t.Fatalf("hostile: the child for %q reached Run in %q", name, t.Name())
	}
	debug.SetMaxStack(lim.MaxStack)
	fmt.Fprintf(os.Stdout, "\nhostile-%s: %s\n", nonce, sentinelStart)
	returned := false
	defer func() {
		// t.Skip unwinds through here via runtime.Goexit. A panic must reach
		// the testing package unrecovered, so this never calls recover.
		if !returned && t.Skipped() {
			fmt.Fprintf(os.Stdout, "\nhostile-%s: %s\n", nonce, sentinelSkip)
		}
	}()
	fn(t)
	returned = true
	// The child's own high-water mark, measured by itself: it covers a spike
	// the parent's polling missed, and it is the child's alone. (The kernel's
	// ru_maxrss is not: Linux carries the forking process's peak into the
	// child across exec, which charged every child with its parent's memory.)
	fmt.Fprintf(os.Stdout, "\nhostile-%s: %s%d\n", nonce, sentinelPeak, peakRSS(os.Getpid()))
	fmt.Fprintf(os.Stdout, "\nhostile-%s: %s\n", nonce, sentinelDone)
	return Outcome{Kind: OK, Limits: lim}
}

func runParent(t *testing.T, lim Limits) Outcome {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	var nb [8]byte
	if _, err := rand.Read(nb[:]); err != nil {
		t.Fatalf("hostile: nonce: %v", err)
	}
	nonce := hex.EncodeToString(nb[:])

	args := []string{
		"-test.run=" + RunPattern(t.Name()),
		"-test.v",
		"-test.count=1",
		// A backstop only: the parent kills the child at lim.Timeout. If the
		// parent dies without delivering the kill on a platform without a
		// parent-death signal, the testing package ends the child here.
		"-test.timeout=" + (lim.Timeout + 30*time.Second).String(),
	}
	if testing.Short() {
		args = append(args, "-test.short")
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(withoutEnv(os.Environ(), envChild), envChild+"="+nonce+" "+t.Name())
	cmd.SysProcAttr = sysProcAttr()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("hostile: pipe: %v", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	start := time.Now()
	if err := startChild(cmd); err != nil {
		pr.Close()
		pw.Close()
		t.Fatalf("hostile: start %s: %v", exe, err)
	}
	pw.Close()

	if !rssSupported {
		t.Logf("hostile: the memory cap is not enforced on this platform; the timeout and crash detection still apply")
	}

	c := newCapture("hostile-" + nonce + ": ")
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := pr.Read(buf)
			c.write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var (
		kind    Kind
		killed  bool
		peak    int64
		waitErr error
	)
	sample := func() {
		if p := peakRSS(cmd.Process.Pid); p > peak {
			peak = p
		}
	}
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	deadline := time.NewTimer(lim.Timeout)
	defer deadline.Stop()
wait:
	for {
		select {
		case waitErr = <-waitDone:
			break wait
		case <-deadline.C:
			sample()
			kind, killed = Timeout, true
			killGroup(cmd.Process)
			waitErr = <-waitDone
			break wait
		case <-tick.C:
			sample()
			if rssSupported && peak > lim.MaxRSS {
				kind, killed = OverMemory, true
				killGroup(cmd.Process)
				waitErr = <-waitDone
				break wait
			}
		}
	}
	elapsed := time.Since(start)
	// The child is gone. A grandchild it started could still hold the pipe
	// open; the parent does not wait for it forever. (The group is not killed
	// again here: once its leader has been reaped the process-group ID can be
	// reused, and a late kill could reach an unrelated group.)
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		pr.Close()
		<-readDone
	}
	pr.Close()

	if c.peak > peak {
		peak = c.peak
	}
	out := Outcome{
		PeakRSS:  peak,
		Elapsed:  elapsed,
		ExitCode: cmd.ProcessState.ExitCode(),
		Crash:    c.crashLine(),
		Output:   c.output(),
		Limits:   lim,
	}
	switch {
	case killed:
		out.Kind = kind
	case rssSupported && peak > lim.MaxRSS:
		// A spike between two polls is still a breach: the child reports its
		// own high-water mark when fn returns.
		out.Kind = OverMemory
	default:
		out.Kind = classify(waitErr, out.ExitCode, c)
	}
	if out.Kind == Fatal && out.Crash == "" {
		if s := signalOf(cmd.ProcessState); s != "" {
			out.Crash = "killed by signal " + s
		}
	}
	return out
}

func classify(waitErr error, code int, c *capture) Kind {
	var ee *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &ee) {
		return Fatal
	}
	switch {
	case code == 0 && c.done:
		return OK
	case code == 0 && c.skip:
		return Skipped
	case code == 0:
		return NotRun
	case c.crashLine() != "":
		return Fatal
	case code == 1 && c.started:
		// The testing package exits 1 for a failed test; a runtime crash or
		// an unrecovered panic exits 2 and prints a crash line.
		return Failed
	}
	return Fatal
}

// RunPattern returns a -test.run pattern that selects exactly the test or
// subtest named name, as t.Name() reports it. The testing package splits the
// pattern on unbracketed, unescaped slashes and matches each element against
// the corresponding level of the name, so each level is quoted and anchored on
// its own.
func RunPattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

func withoutEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// capture keeps a bounded head and tail of the child's output and recognises
// the sentinels and the runtime's crash lines as they stream past. Its size is
// fixed, whatever the child writes.
type capture struct {
	prefix  string
	head    []byte
	tail    []byte // ring of tailCap bytes
	tailPos int
	total   int64
	line    []byte // current line, truncated to lineCap
	crash   string // first "fatal error:" or "panic:" line
	rtNote  string // first "runtime:" line
	started bool
	done    bool
	skip    bool
	peak    int64 // the child's self-reported VmHWM, if it reported one
}

func newCapture(prefix string) *capture {
	return &capture{
		prefix: prefix,
		head:   make([]byte, 0, headCap),
		tail:   make([]byte, tailCap),
		line:   make([]byte, 0, lineCap),
	}
}

func (c *capture) write(p []byte) {
	c.total += int64(len(p))
	if n := headCap - len(c.head); n > 0 {
		c.head = append(c.head, p[:min(n, len(p))]...)
	}
	for _, b := range p {
		c.tail[c.tailPos] = b
		c.tailPos = (c.tailPos + 1) % tailCap
		if b == '\n' {
			c.endLine()
			continue
		}
		if len(c.line) < lineCap {
			c.line = append(c.line, b)
		}
	}
}

func (c *capture) endLine() {
	l := string(c.line)
	c.line = c.line[:0]
	if rest, ok := strings.CutPrefix(l, c.prefix); ok {
		switch rest {
		case sentinelStart:
			c.started = true
		case sentinelDone:
			c.done = true
		case sentinelSkip:
			c.skip = true
		default:
			if n, err := strconv.ParseInt(strings.TrimPrefix(rest, sentinelPeak), 10, 64); err == nil && strings.HasPrefix(rest, sentinelPeak) {
				c.peak = n
			}
		}
		return
	}
	// The runtime writes these at column 0. A test's own t.Log output is
	// indented, so it is not mistaken for one, and they are consulted only when
	// the child exited non-zero.
	if c.crash == "" && (strings.HasPrefix(l, "fatal error: ") || strings.HasPrefix(l, "panic: ")) {
		c.crash = l
	}
	if c.rtNote == "" && strings.HasPrefix(l, "runtime: ") {
		c.rtNote = l
	}
}

// crashLine is the runtime's own summary of how the child died, if it gave one.
func (c *capture) crashLine() string {
	if c.crash != "" {
		return c.crash
	}
	return c.rtNote
}

// output reassembles the captured head and tail, marking any omitted middle.
func (c *capture) output() string {
	if c.total <= tailCap {
		return string(c.tail[:c.total]) // the ring has not wrapped: it is all here
	}
	tail := append(append(make([]byte, 0, tailCap), c.tail[c.tailPos:]...), c.tail[:c.tailPos]...)
	tailStart := c.total - tailCap // stream offset of tail[0]
	head := int64(len(c.head))
	if tailStart <= head {
		return string(c.head) + string(tail[head-tailStart:])
	}
	return string(c.head) + fmt.Sprintf("\n[... %d bytes of output omitted ...]\n", tailStart-head) + string(tail)
}
