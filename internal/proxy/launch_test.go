package proxy

import (
	"testing"

	"github.com/cyucelen/isola/internal/state"
)

func TestIsRunningFalseWhenNoProxy(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	running, err := IsRunning(store)
	if err != nil {
		t.Fatalf("IsRunning: %v", err)
	}
	if running {
		t.Error("expected no proxy running for a fresh state")
	}
}

func TestStopWhenNotRunning(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := Stop(store)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped {
		t.Error("Stop should report false when no proxy was running")
	}
	// State should be marked stopped and safe to reload.
	st, _ := store.Load()
	if st.Proxy.Status == state.StatusRunning {
		t.Error("proxy should not be running after Stop")
	}
}
