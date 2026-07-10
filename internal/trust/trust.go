// Package trust installs isola's development CA certificate into the OS trust
// store so browsers and tools accept the HTTPS certificates isola generates.
// Installing a trusted root always requires elevation, so Install shells out to
// sudo and prompts the user; it can never be silent.
package trust

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/term"
)

// Interactive reports whether stdin is a real terminal. Auto-trust only runs
// when interactive, so a non-interactive `isola up` (an agent, CI, or stdin
// redirected from a pipe or /dev/null) never attempts a sudo password prompt it
// cannot answer. A plain char-device check is not enough: /dev/null is a
// character device but not a terminal, so this uses an actual TTY test.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// Supported reports whether automatic trust-store installation is implemented
// for the current OS.
func Supported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

// IsTrusted reports whether the CA at caPath is already trusted by the system.
// A best-effort check: on an error or an unsupported OS it returns false, so the
// caller treats trust as not-yet-established rather than silently skipping.
func IsTrusted(caPath string) bool {
	switch runtime.GOOS {
	case "darwin":
		// verify-cert succeeds only if the cert chains to a trusted root.
		return exec.Command("security", "verify-cert", "-c", caPath).Run() == nil
	case "linux":
		if _, err := os.Stat(linuxDestPath()); err == nil {
			return true
		}
		return false
	default:
		return false
	}
}

// Install adds the CA at caPath to the system trust store, prompting for a
// password via sudo. It returns an error if the CA is missing, the OS is
// unsupported, or the user declines/cancels the prompt.
func Install(caPath string) error {
	if _, err := os.Stat(caPath); os.IsNotExist(err) {
		return fmt.Errorf("CA certificate not found at %s", caPath)
	}
	switch runtime.GOOS {
	case "darwin":
		return installDarwin(caPath)
	case "linux":
		return installLinux(caPath)
	default:
		return fmt.Errorf("automatic trust is not supported on %s; install %s manually", runtime.GOOS, caPath)
	}
}

func installDarwin(caPath string) error {
	return run("sudo", "security", "add-trusted-cert",
		"-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain",
		caPath)
}

func linuxDestPath() string {
	return filepath.Join("/usr/local/share/ca-certificates/isola", "isola-dev-ca.crt")
}

func installLinux(caPath string) error {
	dest := linuxDestPath()
	if err := run("sudo", "mkdir", "-p", filepath.Dir(dest)); err != nil {
		return err
	}
	if err := run("sudo", "cp", caPath, dest); err != nil {
		return err
	}
	return run("sudo", "update-ca-certificates")
}

// run executes a command wired to the user's terminal so sudo can prompt.
func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
