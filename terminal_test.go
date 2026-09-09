package main

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
)

func TestNonInteractiveCommandDoesNotQueryTerminalColors(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "isola")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build isola: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "version")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start isola on pseudo-terminal: %v", err)
	}
	defer terminal.Close()

	out, readErr := io.ReadAll(terminal)
	if readErr != nil && !errors.Is(readErr, syscall.EIO) {
		t.Fatalf("read pseudo-terminal: %v", readErr)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("isola version: %v\n%s", err, out)
	}

	const osc11Query = "\x1b]11;?\x1b\\"
	if bytes.Contains(out, []byte(osc11Query)) {
		t.Fatalf("isola version queried the terminal background color: %q", strings.ReplaceAll(string(out), "\x1b", "<ESC>"))
	}
}
