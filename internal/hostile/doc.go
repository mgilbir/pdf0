// Package hostile runs a test that feeds hostile input in a capped child
// process, so that a regression fails the test instead of taking down the
// machine running it.
//
// # Why
//
// pdf0 reads untrusted files, and most of its denial-of-service guards have a
// regression test: a crafted input that used to allocate gigabytes, loop, or
// recurse until the stack overflowed, now asserted to be rejected cheaply. Run
// in-process, such a test only protects anything while the guard holds. The day
// the guard regresses, the test does what the input was built to do: it
// allocates until the kernel's OOM killer picks a victim, which on a developer
// machine is often the editor, the shell or the agent session rather than the
// test binary, and on CI is the runner. A fatal stack overflow cannot be
// recovered and ends the whole test binary, taking every other test's result
// with it. Either way the regression is reported as an infrastructure failure,
// if it is reported at all, never as the one test that caught it.
//
// Run moves the hostile part of the test into a subprocess with a hard memory
// cap, a hard wall-clock cap and a small goroutine stack limit, and turns each
// way the child can die into an ordinary, named test failure.
//
// # How to use it
//
// Wrap the body that touches the hostile input:
//
//	func TestObjStmBombIsRejected(t *testing.T) {
//		hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: 20 * time.Second}, func(t *testing.T) {
//			bomb := buildBomb(t)
//			_, err := pdf0.Read(bytes.NewReader(bomb), int64(len(bomb)))
//			if err == nil || !strings.Contains(err.Error(), "budget") {
//				t.Fatalf("Read = %v, want the budget error", err)
//			}
//		})
//	}
//
// The function runs the real API and asserts on its result exactly as an
// in-process test would; that is how a test proves a hostile input is rejected
// within bounds. The limits are the safety net underneath the assertion, not
// the assertion itself: a test whose only check is "the child did not die" is
// a crash test, and pdf0's regression tests assert a bounded error, a specific
// finding or specific bytes.
//
// Rules:
//   - Call Run at most once per test. Put several hostile cases in subtests, one
//     Run each.
//   - Nothing after Run in the same test body is protected, and in the child it
//     runs too. Keep the whole hostile part inside fn.
//   - Build inputs in code where possible. go test caches a passing result
//     keyed on the files the test binary opened, and it cannot see the files
//     the child opens.
//
// # How it works
//
// In the parent, Run re-executes the current test binary with -test.run
// anchored to exactly the calling test (each element of a subtest path is
// quoted and anchored separately, so names containing regexp metacharacters
// match only themselves) and an environment marker naming that test. In the
// child, Run finds the marker, sets debug.SetMaxStack, calls fn in-process and
// prints a sentinel when fn returns. The parent watches the child and never
// buffers more than a fixed amount of its output: the first and last few
// kilobytes, plus the first runtime crash line.
//
// The child is started in its own process group, and every kill is sent to the
// group. On Linux the child also receives SIGKILL if the parent dies, so an
// interrupted go test cannot leave an unbounded child behind.
//
// # Outcomes
//
// Each outcome except OK and Skipped fails the calling test with t.Fatalf, and
// the child's captured -test.v output is logged with it.
//
//   - OK: fn returned, every assertion in it passed, and the child stayed
//     inside its limits.
//   - OverMemory: the child's resident set exceeded Limits.MaxRSS. The peak is
//     reported. The parent polls the child's peak RSS (VmHWM) and kills it on
//     breach; a child that spiked above the cap between polls and then finished
//     is still OverMemory, because the child reports its own VmHWM when fn
//     returns. (Not ru_maxrss: Linux carries the parent's peak into the
//     child's across exec.)
//   - Timeout: the child ran longer than Limits.Timeout and was killed.
//   - Fatal: the child died of something no test can recover from: a runtime
//     fatal error ("fatal error: stack overflow", concurrent map writes), an
//     unrecovered panic, a signal, or an unexpected exit status. The first crash
//     line is reported. A fatal stack overflow is what an unbounded recursion
//     looks like here, because the child's maximum stack is small.
//   - Failed: fn ran to completion or called t.FailNow, and an assertion in it
//     failed. This is the ordinary test failure, reported with the child's log.
//   - Skipped: fn called t.Skip. The parent skips too.
//   - NotRun: the child exited cleanly without running fn. This is a harness
//     error, for example a test name the child could not select.
//
// # Platforms
//
// The timeout, the fatal detection and the process-group kill work on every
// Unix. The memory cap is enforced on Linux only, where the kernel exposes the
// child's resident set in /proc. Elsewhere Run still runs the child under the
// timeout and logs that the memory cap is not enforced; it does not skip.
//
// Under the race detector, which multiplies memory use by 5-10x and run time by
// 2-20x, Run scales MaxRSS and Timeout by 8. Assertions inside fn, including
// wall-clock thresholds, are not scaled.
package hostile
