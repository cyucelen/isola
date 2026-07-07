package process

import (
	"path/filepath"
	"testing"
)

func TestLogFileName(t *testing.T) {
	got := LogFileName("feature-auth", "frontend")
	if want := "feature-auth.frontend.log"; got != want {
		t.Errorf("LogFileName = %q, want %q", got, want)
	}
}

func TestLogPath(t *testing.T) {
	got := LogPath("/repo/.isola", "main", "web")
	want := filepath.Join("/repo/.isola", "logs", "main.web.log")
	if got != want {
		t.Errorf("LogPath = %q, want %q", got, want)
	}
}
