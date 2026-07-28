package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestAssetName(t *testing.T) {
	// The leading "v" is stripped to match the goreleaser name_template.
	got := AssetName("v0.3.0", "darwin", "arm64")
	if want := "isola_0.3.0_darwin_arm64.tar.gz"; got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
	if got := AssetName("1.2.3", "linux", "amd64"); got != "isola_1.2.3_linux_amd64.tar.gz" {
		t.Errorf("AssetName without v = %q", got)
	}
}

func TestNewerAvailable(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
		wantErr     bool
	}{
		{"v0.2.0", "v0.3.0", true, false},
		{"v0.2.0", "v0.2.1", true, false},
		{"v1.0.0", "v0.9.9", false, false},
		{"v0.3.0", "v0.3.0", false, false},
		{"0.2.0", "v0.2.0", false, false},      // "v" optional, equal
		{"0.2.0-next", "v0.2.0", false, false}, // snapshot compares by its core
		{"v0.2.0", "v0.2.0-next", false, false},
		{"dev", "v0.3.0", false, true}, // unparseable current
		{"", "v0.3.0", false, true},
	}
	for _, c := range cases {
		got, err := NewerAvailable(c.cur, c.latest)
		if (err != nil) != c.wantErr {
			t.Errorf("NewerAvailable(%q,%q) err=%v, wantErr=%v", c.cur, c.latest, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("NewerAvailable(%q,%q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestParseChecksum(t *testing.T) {
	sums := "abc123  isola_0.3.0_linux_amd64.tar.gz\ndef456  isola_0.3.0_darwin_arm64.tar.gz\n"
	got, err := ParseChecksum(sums, "isola_0.3.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("ParseChecksum: %v", err)
	}
	if got != "def456" {
		t.Errorf("ParseChecksum = %q, want def456", got)
	}
	if _, err := ParseChecksum(sums, "isola_0.3.0_windows_amd64.tar.gz"); err == nil {
		t.Error("ParseChecksum should error for a missing asset")
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("hello isola")
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	if err := VerifySHA256(data, hexSum); err != nil {
		t.Errorf("VerifySHA256 should pass on a matching digest: %v", err)
	}
	if err := VerifySHA256(data, "deadbeef"); err == nil {
		t.Error("VerifySHA256 should fail on a mismatched digest")
	}
}

func TestExtractBinary(t *testing.T) {
	want := []byte("#!/bin/echo fake-isola-binary\n")
	targz := makeTarGz(t, map[string][]byte{
		"README.md": []byte("docs"),
		"isola":     want,
	})
	got, err := ExtractBinary(targz)
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("ExtractBinary returned wrong bytes: %q", got)
	}

	// An archive without the binary is an error, not a silent empty result.
	noBin := makeTarGz(t, map[string][]byte{"LICENSE": []byte("MIT")})
	if _, err := ExtractBinary(noBin); err == nil {
		t.Error("ExtractBinary should error when the isola binary is absent")
	}
}

func TestIsHomebrewPath(t *testing.T) {
	cases := []struct {
		exe, prefix string
		want        bool
	}{
		{"/opt/homebrew/Cellar/isola/0.3.0/bin/isola", "", true},
		{"/usr/local/Caskroom/isola/0.3.0/isola", "", true},
		{"/home/linuxbrew/.linuxbrew/bin/isola", "", true},
		{"/opt/brew/bin/isola", "/opt/brew", true}, // matched by prefix
		{"/home/me/bin/isola", "/opt/brew", false},
		{"/usr/local/go/bin/isola", "", false},
	}
	for _, c := range cases {
		if got := isHomebrewPath(c.exe, c.prefix); got != c.want {
			t.Errorf("isHomebrewPath(%q,%q) = %v, want %v", c.exe, c.prefix, got, c.want)
		}
	}
}

func TestIsGoInstallPath(t *testing.T) {
	if !isGoInstallPath("/home/me/go/bin/isola", "/home/me/go/bin") {
		t.Error("a binary in GOBIN should be detected as a go install")
	}
	if isGoInstallPath("/usr/bin/isola", "/home/me/go/bin") {
		t.Error("a binary outside GOBIN should not be a go install")
	}
	if isGoInstallPath("/home/me/go/bin/isola", "") {
		t.Error("an empty go bin dir should never match")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/isola"
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceExecutable(target, []byte("new-binary")); err != nil {
		t.Fatalf("ReplaceExecutable: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary" {
		t.Errorf("target content = %q, want new-binary", got)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode not preserved: got %v", fi.Mode().Perm())
	}
	// No temp turds left behind on success.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the target file, got %d entries", len(entries))
	}
}

// makeTarGz builds a gzipped tar containing the given files.
func makeTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
