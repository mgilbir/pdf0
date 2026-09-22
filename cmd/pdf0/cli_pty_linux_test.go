package main

// The prompt path, on a real pseudo-terminal: the built command runs as a
// session leader with a pty as its controlling terminal and its stdin, and the
// test types into the master side. This is what proves that echo is off while
// the password is typed and is restored afterwards, including on Ctrl-C;
// fakeTerminal only covers the logic around the prompt.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// pty is a pseudo-terminal pair; the test holds the master.
type pty struct {
	master, slave *os.File

	mu  sync.Mutex
	out bytes.Buffer // everything the child wrote to the terminal
}

func openPTY(t *testing.T) *pty {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	var ptn uint32
	var ioctlErr error
	rc, err := m.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	// Control, not Fd: Fd would switch the master to blocking mode, and a
	// blocked read could then never be interrupted by Close.
	err = rc.Control(func(fd uintptr) {
		var unlock int32
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
			ioctlErr = e
			return
		}
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptn))); e != 0 {
			ioctlErr = e
		}
	})
	if err != nil || ioctlErr != nil {
		m.Close()
		t.Fatalf("setting up the pty: %v %v", err, ioctlErr)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptn), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	p := &pty{master: m, slave: s}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := m.Read(buf)
			p.mu.Lock()
			p.out.Write(buf[:n])
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		s.Close()
		m.Close()
		<-done
	})
	return p
}

func (p *pty) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// waitFor waits until the terminal output contains want.
func (p *pty) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(p.output(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q on the terminal; got %q", want, p.output())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *pty) echoOn(t *testing.T) bool {
	t.Helper()
	tios, err := getTermios(p.slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	return tios.Lflag&syscall.ECHO != 0
}

func (p *pty) typeLine(t *testing.T, s string) {
	t.Helper()
	if _, err := p.master.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

// start runs the command with the pty as its controlling terminal, stdin and
// stderr. stdout is the pty too unless another file is given.
func (p *pty) start(t *testing.T, stdout *os.File, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(cliBinary, args...)
	cmd.Env = cleanEnv()
	cmd.Dir = t.TempDir()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = p.slave, p.slave, p.slave
	if stdout != nil {
		cmd.Stdout = stdout
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func exitStatus(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- cmd.Wait() }()
	select {
	case err := <-errc:
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatal(err)
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the command did not exit")
	}
	return -1
}

// TestCLIPromptOnTerminal: with the file locked, no password supplied and a
// terminal on stdin, decrypt prompts; echo is off while the password is typed,
// the password never appears on the terminal, and echo is back on afterwards
// (C113).
func TestCLIPromptOnTerminal(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "typed-secret", "ownerpw"))
	out := filepath.Join(dir, "dec.pdf")
	p := openPTY(t)
	if !p.echoOn(t) {
		t.Fatal("a fresh pty has echo off; the restore check below would prove nothing")
	}

	cmd := p.start(t, nil, "decrypt", enc, out)
	p.waitFor(t, "Password for")
	if p.echoOn(t) {
		t.Error("echo is on while the password prompt is waiting")
	}
	p.typeLine(t, "typed-secret\n")
	if code := exitStatus(t, cmd); code != 0 {
		t.Fatalf("decrypt with a typed password: exit %d; terminal:\n%s", code, p.output())
	}
	if strings.Contains(p.output(), "typed-secret") {
		t.Errorf("the password was echoed to the terminal: %q", p.output())
	}
	if !p.echoOn(t) {
		t.Error("echo was not restored after the prompt")
	}
	if doc := openDoc(t, readFile(t, out), ""); doc.Encrypted {
		t.Error("the decrypted output is still encrypted")
	}
}

// TestCLIPromptInterrupted: Ctrl-C at the prompt exits 130 and leaves the
// terminal with echo on, not in the no-echo state the prompt set.
func TestCLIPromptInterrupted(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	p := openPTY(t)
	cmd := p.start(t, nil, "extract", enc)
	p.waitFor(t, "Password for")
	if p.echoOn(t) {
		t.Fatal("echo is on while the password prompt is waiting")
	}
	p.typeLine(t, "\x03") // Ctrl-C: the line discipline sends SIGINT
	if code := exitStatus(t, cmd); code != 130 {
		t.Errorf("interrupted prompt: exit %d, want 130", code)
	}
	if !p.echoOn(t) {
		t.Error("echo was left off after Ctrl-C at the prompt")
	}
}

// TestCLIEncryptPromptOnTerminal: encrypt asks twice for the user password on
// a terminal, with echo off both times.
func TestCLIEncryptPromptOnTerminal(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	out := filepath.Join(dir, "enc.pdf")
	p := openPTY(t)
	cmd := p.start(t, nil, "encrypt", plain, out)
	p.waitFor(t, "User password")
	p.typeLine(t, "pw-one\n")
	p.waitFor(t, "Repeat the user password")
	if p.echoOn(t) {
		t.Error("echo is on at the confirmation prompt")
	}
	p.typeLine(t, "pw-one\n")
	if code := exitStatus(t, cmd); code != 0 {
		t.Fatalf("encrypt with typed passwords: exit %d; terminal:\n%s", code, p.output())
	}
	if strings.Contains(p.output(), "pw-one") {
		t.Errorf("the password was echoed: %q", p.output())
	}
	if openDoc(t, readFile(t, out), "pw-one").Locked() {
		t.Error("the typed password does not open the output")
	}
}

// TestCLIRefusesPDFToTerminal: "-" as the output with stdout on a terminal is
// refused rather than spraying binary over the screen.
func TestCLIRefusesPDFToTerminal(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	p := openPTY(t)
	cmd := p.start(t, nil, "repair", plain, "-")
	if code := exitStatus(t, cmd); code != 2 {
		t.Errorf("repair to a terminal: exit %d, want 2", code)
	}
	p.waitFor(t, "refusing to write a PDF to a terminal")
	if strings.Contains(p.output(), "%PDF-") {
		t.Error("PDF bytes reached the terminal")
	}
}
