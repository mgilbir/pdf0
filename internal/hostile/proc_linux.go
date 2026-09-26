//go:build linux

package hostile

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const rssSupported = true

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		// Its own process group, so a kill reaches anything the child started.
		Setpgid: true,
		// If the parent dies — go test's own timeout, ^C, an OOM kill — the
		// child goes with it instead of running on uncapped.
		Pdeathsig: syscall.SIGKILL,
	}
}

// startChild starts cmd.
//
// Pdeathsig fires when the OS thread that forked the child exits, not the
// process. The Go runtime ends a thread only when a goroutine exits while
// locked to it, which nothing in a test binary is expected to do, so in
// practice the signal arrives when the parent process dies. The child's
// -test.timeout is the backstop for the case where it does not.
func startChild(cmd *exec.Cmd) error { return cmd.Start() }

func killGroup(p *os.Process) {
	// Negative pid: the whole process group the child leads.
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	_ = p.Kill()
}

// peakRSS reads a process's peak resident set (VmHWM) from /proc: the parent
// polls the child with it, and the child reports its own when fn returns. The
// high-water mark rather than the current VmRSS catches a spike that came and
// went between two polls. VmHWM belongs to the address space exec created, so
// unlike ru_maxrss it does not include the parent's. It returns 0 once the
// process has exited.
func peakRSS(pid int) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		rest, ok := bytes.CutPrefix(line, []byte("VmHWM:"))
		if !ok {
			continue
		}
		f := bytes.Fields(rest) // "12345 kB"
		if len(f) != 2 || string(f[1]) != "kB" {
			return 0
		}
		kb, err := strconv.ParseInt(string(f[0]), 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}

func signalOf(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
