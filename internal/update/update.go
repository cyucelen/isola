// Package update backs `isola update`: it finds the latest GitHub release,
// works out how this binary was installed, and either replaces a standalone
// binary in place (checksum-verified) or hands off to the package manager.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	repoOwner = "cyucelen"
	repoName  = "isola"
	// binName is the executable inside each release tarball.
	binName = "isola"
	// maxDownload caps a downloaded asset so a bad URL can't exhaust memory.
	maxDownload = 200 << 20 // 200 MiB
)

// Method is how the running isola binary was installed, which decides whether
// `isola update` can replace it directly or must defer to a package manager.
type Method int

const (
	// Standalone is a plain binary (dropped on PATH, built from source) that
	// isola can replace in place.
	Standalone Method = iota
	// GoInstall is a `go install` binary under GOBIN/GOPATH; update via go.
	GoInstall
	// Homebrew, Pacman, Dpkg, RPM are package-manager-owned; replacing the file
	// would corrupt the manager's manifest, so we defer to it.
	Homebrew
	Pacman
	Dpkg
	RPM
)

// Release is the subset of a GitHub release we use.
type Release struct {
	Tag    string            // e.g. "v0.3.0"
	Assets map[string]string // asset file name -> download URL
}

// httpClient is used for all network calls; the caller supplies the timeout
// through the request context.
var httpClient = &http.Client{}

// LatestRelease fetches the latest published release from GitHub.
func LatestRelease(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "isola-update")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("querying GitHub releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub releases returned %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decoding release: %w", err)
	}
	if payload.TagName == "" {
		return Release{}, fmt.Errorf("GitHub returned a release with no tag")
	}

	assets := make(map[string]string, len(payload.Assets))
	for _, a := range payload.Assets {
		assets[a.Name] = a.URL
	}
	return Release{Tag: payload.TagName, Assets: assets}, nil
}

// AssetName returns the release tarball name for a version and platform, matching
// the goreleaser name_template ("isola_<version>_<os>_<arch>.tar.gz", version
// without a leading "v").
func AssetName(version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", binName, strings.TrimPrefix(version, "v"), goos, goarch)
}

// NewerAvailable reports whether latest is a strictly newer release than current.
// It returns an error when current is not a parseable release version (e.g. a
// "dev" or source build), so the caller can steer the user to the right path.
func NewerAvailable(current, latest string) (bool, error) {
	cur, err := parseVersion(current)
	if err != nil {
		return false, fmt.Errorf("current version %q is not a release build: %w", current, err)
	}
	lat, err := parseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("latest version %q is unparseable: %w", latest, err)
	}
	return lat.greater(cur), nil
}

type semver struct{ major, minor, patch int }

func (a semver) greater(b semver) bool {
	if a.major != b.major {
		return a.major > b.major
	}
	if a.minor != b.minor {
		return a.minor > b.minor
	}
	return a.patch > b.patch
}

// parseVersion parses "vMAJOR.MINOR.PATCH" (leading "v" optional), ignoring any
// prerelease/build suffix ("-next", "+meta") so a snapshot compares by its core.
func parseVersion(v string) (semver, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("not MAJOR.MINOR.PATCH")
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, fmt.Errorf("non-numeric component %q", p)
		}
		nums[i] = n
	}
	return semver{nums[0], nums[1], nums[2]}, nil
}

// ParseChecksum returns the hex sha256 recorded for asset in a goreleaser
// checksums.txt ("<sha256>  <filename>" per line).
func ParseChecksum(checksums, asset string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %q", asset)
}

// Detect classifies how the binary at exe (symlinks already resolved) was
// installed. It shells out to package managers only to confirm ownership of a
// system path, so a standalone binary needs no external tools.
func Detect(exe string) Method {
	if isHomebrewPath(exe, os.Getenv("HOMEBREW_PREFIX")) {
		return Homebrew
	}
	if runtime.GOOS == "linux" {
		if pkgOwns("pacman", []string{"-Qo", exe}) {
			return Pacman
		}
		if pkgOwns("dpkg", []string{"-S", exe}) {
			return Dpkg
		}
		if pkgOwns("rpm", []string{"-qf", exe}) {
			return RPM
		}
	}
	if isGoInstallPath(exe, goBinDir()) {
		return GoInstall
	}
	return Standalone
}

// isHomebrewPath reports whether exe lives inside a Homebrew installation, by
// the configured prefix or the well-known Cellar/Caskroom/linuxbrew layouts.
func isHomebrewPath(exe, prefix string) bool {
	if prefix != "" && withinDir(exe, prefix) {
		return true
	}
	for _, marker := range []string{"/Cellar/", "/Caskroom/", "/homebrew/", "/.linuxbrew/"} {
		if strings.Contains(exe, marker) {
			return true
		}
	}
	return false
}

// isGoInstallPath reports whether exe sits in the Go bin directory.
func isGoInstallPath(exe, goBin string) bool {
	return goBin != "" && filepath.Dir(exe) == filepath.Clean(goBin)
}

// withinDir reports whether path is dir or nested under it.
func withinDir(path, dir string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// pkgOwns reports whether a package manager claims ownership of a file (exit 0).
func pkgOwns(manager string, args []string) bool {
	if _, err := exec.LookPath(manager); err != nil {
		return false
	}
	return exec.Command(manager, args...).Run() == nil
}

// goBinDir returns where `go install` places binaries: GOBIN, else GOPATH/bin.
func goBinDir() string {
	if out, err := exec.Command("go", "env", "GOBIN").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return d
		}
	}
	if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			return filepath.Join(d, "bin")
		}
	}
	return ""
}

// Download fetches url and returns its bytes, capped at maxDownload.
func Download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "isola-update")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// VerifySHA256 checks data against a hex-encoded expected digest.
func VerifySHA256(data []byte, expectedHex string) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != expectedHex {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, expectedHex)
	}
	return nil
}

// ExtractBinary returns the isola executable bytes from a .tar.gz archive.
func ExtractBinary(targz []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, fmt.Errorf("opening gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == binName {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
	return nil, fmt.Errorf("%q not found in archive", binName)
}

// ReplaceExecutable atomically swaps the file at target for data, preserving the
// existing file mode. It writes a temp file in the same directory and renames it,
// so the running process (holding the old inode) is undisturbed. A permission
// error is wrapped with guidance since a system path needs sudo.
func ReplaceExecutable(target string, data []byte) error {
	dir := filepath.Dir(target)
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".isola-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("no write access to %s; re-run with sudo or update via your package manager: %w", dir, err)
		}
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		if os.IsPermission(err) {
			return fmt.Errorf("no write access to %s; re-run with sudo: %w", target, err)
		}
		return err
	}
	return nil
}
