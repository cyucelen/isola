package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/update"
	"github.com/spf13/cobra"
)

var updateCheck bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update isola to the latest release",
	Long: `Update isola to the latest release.

Detects how isola was installed and does the right thing: a standalone or
"go install" binary is replaced in place with the latest release (verified
against its published checksum), while a Homebrew, AUR, or deb/rpm install is
upgraded through its package manager (or the exact command is printed when it
needs sudo). Use --check to only report whether an update is available.`,
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		cur, _, _ := versionInfo()

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		rel, err := update.LatestRelease(ctx)
		if err != nil {
			return err
		}
		logging.Info("Current: %s   Latest: %s", cur, rel.Tag)

		newer, err := update.NewerAvailable(cur, rel.Tag)
		if err != nil {
			// A dev/source build has no release version to compare against.
			logging.Info("You're on a development build; update from your source checkout (git pull && make build).")
			return nil
		}
		if !newer {
			logging.Info("✓ already on the latest release (%s)", rel.Tag)
			return nil
		}

		exe, err := resolvedExecutable()
		if err != nil {
			return err
		}
		method := update.Detect(exe)

		if updateCheck {
			logging.Info("Update available: %s → %s", cur, rel.Tag)
			logging.Info("To update: %s", updateHint(method, exe))
			return nil
		}

		switch method {
		case update.Homebrew:
			return runManager("brew", "upgrade", "--cask", "isola")
		case update.GoInstall:
			return runManager("go", "install", "github.com/cyucelen/isola@latest")
		case update.Pacman, update.Dpkg, update.RPM:
			// These need sudo (and, for AUR, an unknown helper), so we print the
			// command rather than running it.
			logging.Info("isola was installed with a package manager. To update:")
			logging.Info("  %s", updateHint(method, exe))
			return nil
		default:
			return selfUpdate(ctx, rel, exe)
		}
	},
}

// selfUpdate downloads the release tarball for this platform, verifies its
// checksum, and replaces the running binary in place.
func selfUpdate(ctx context.Context, rel update.Release, exe string) error {
	asset := update.AssetName(rel.Tag, runtime.GOOS, runtime.GOARCH)
	assetURL, ok := rel.Assets[asset]
	if !ok {
		return fmt.Errorf("no release asset %q for your platform (%s/%s)", asset, runtime.GOOS, runtime.GOARCH)
	}
	sumsURL, ok := rel.Assets["checksums.txt"]
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}

	logging.Info("↓ downloading %s", asset)
	tarball, err := update.Download(ctx, assetURL)
	if err != nil {
		return err
	}
	sums, err := update.Download(ctx, sumsURL)
	if err != nil {
		return err
	}

	wantSHA, err := update.ParseChecksum(string(sums), asset)
	if err != nil {
		return err
	}
	if err := update.VerifySHA256(tarball, wantSHA); err != nil {
		return err
	}

	bin, err := update.ExtractBinary(tarball)
	if err != nil {
		return err
	}
	if err := update.ReplaceExecutable(exe, bin); err != nil {
		return err
	}

	logging.Info("✓ checksum ok, replaced %s → %s", exe, rel.Tag)
	return nil
}

// runManager runs a package-manager command with inherited stdio so its prompts
// and progress reach the user directly.
func runManager(name string, args ...string) error {
	logging.Info("→ running: %s %v", name, args)
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not found on PATH; install it or update isola manually", name)
	}
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

// updateHint is the human-facing command for a given install method.
func updateHint(method update.Method, exe string) string {
	switch method {
	case update.Homebrew:
		return "brew upgrade --cask isola"
	case update.GoInstall:
		return "go install github.com/cyucelen/isola@latest"
	case update.Pacman:
		return "yay -S isola-bin   (or your AUR helper)"
	case update.Dpkg:
		return "download the latest .deb from https://github.com/cyucelen/isola/releases/latest and: sudo dpkg -i isola_*_linux_*.deb"
	case update.RPM:
		return "download the latest .rpm from https://github.com/cyucelen/isola/releases/latest and: sudo rpm -U isola_*_linux_*.rpm"
	default:
		return "isola update   (replaces " + exe + " in place)"
	}
}

// resolvedExecutable returns the running binary's path with symlinks resolved,
// so detection and in-place replacement target the real file.
func resolvedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating the isola binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheck, "check", false, "Only report whether an update is available; make no changes")
	rootCmd.AddCommand(updateCmd)
}
