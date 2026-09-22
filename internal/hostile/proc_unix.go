//go:build unix && !linux

package hostile

import (
	"os"
	"os/exec"
	"syscall"
)

// Only Linux exposes a child's resident set cheaply (/proc). Elsewhere the
// memory cap is not enforced; see the package doc.
const rssSupported = false

func sysProcAttr() *syscall.SysProcAttr {
	// Its own process group, so a kill reaches anything the child started.
	// There is no parent-death signal outside Linux; the child's
	// -test.timeout is the backstop if the parent dies.
	return &syscall.SysProcAttr{Setpgid: true}
}

func startChild(cmd *exec.Cmd) error { return cmd.Start() }

func killGroup(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	_ = p.Kill()
}

func peakRSS(int) int64 { return 0 }

func signalOf(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
