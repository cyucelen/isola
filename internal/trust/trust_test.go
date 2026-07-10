package trust

import (
	"path/filepath"
	"testing"
)

func TestInstallMissingCAErrors(t *testing.T) {
	// A missing CA must fail fast, before any privileged command runs.
	err := Install(filepath.Join(t.TempDir(), "absent.crt"))
	if err == nil {
		t.Fatal("expected an error for a missing CA certificate")
	}
}

func TestIsTrustedMissingCA(t *testing.T) {
	// A path that does not exist is not trusted, and the check must not panic.
	if IsTrusted(filepath.Join(t.TempDir(), "absent.crt")) {
		t.Error("a nonexistent CA should not report as trusted")
	}
}
