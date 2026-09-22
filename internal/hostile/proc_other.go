//go:build !unix

package hostile

import (
	"os"
	"os/exec"
	"syscall"
)

// No process groups and no cheap resident-set reading here: the timeout and
// crash detection apply, the memory cap does not. See the package doc.
const rssSupported = false

func sysProcAttr() *syscall.SysProcAttr { return nil }

func startChild(cmd *exec.Cmd) error { return cmd.Start() }

func killGroup(p *os.Process) { _ = p.Kill() }

func peakRSS(int) int64 { return 0 }

func exitPeakRSS(*os.ProcessState) int64 { return 0 }

func signalOf(*os.ProcessState) string { return "" }
