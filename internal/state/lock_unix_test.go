//go:build unix

package state

import (
	"os"
	"testing"
	"time"
)

// A waiter that opened a lock file which is then deleted and recreated must not
// treat its lock on the unlinked file as the lock: it must queue on the current
// file instead, so two callers never hold "the lock" at once.
func TestServiceLockSurvivesReplacedFile(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := store.ServiceLockPath("main", "web")

	hold := func(release <-chan struct{}, entered chan<- struct{}) error {
		return store.WithServiceLock("main", "web", func() error {
			close(entered)
			<-release
			return nil
		})
	}

	// A holds the lock on the original file.
	releaseA, enteredA := make(chan struct{}), make(chan struct{})
	doneA := make(chan error, 1)
	go func() { doneA <- hold(releaseA, enteredA) }()
	<-enteredA

	// B opens the original file and waits on it.
	releaseB, enteredB := make(chan struct{}), make(chan struct{})
	doneB := make(chan error, 1)
	go func() { doneB <- hold(releaseB, enteredB) }()
	time.Sleep(200 * time.Millisecond) // let B open the file and start waiting

	// The file is deleted under them (as an unlocked delete would), and C
	// locks the recreated one. A and C overlapping is that delete's fault; what
	// withFileLock must prevent is B joining them.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	releaseC, enteredC := make(chan struct{}), make(chan struct{})
	doneC := make(chan error, 1)
	go func() { doneC <- hold(releaseC, enteredC) }()
	select {
	case <-enteredC:
	case <-time.After(5 * time.Second):
		t.Fatal("C should lock the recreated file while A holds the unlinked one")
	}

	// A releases. B now gets the flock on the unlinked file, but C holds the
	// current one, so B must keep waiting.
	close(releaseA)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	select {
	case <-enteredB:
		t.Fatal("B entered while C holds the lock on the current file")
	case <-time.After(300 * time.Millisecond):
	}

	close(releaseC)
	if err := <-doneC; err != nil {
		t.Fatal(err)
	}
	select {
	case <-enteredB:
	case <-time.After(5 * time.Second):
		t.Fatal("B should get the lock once C releases it")
	}
	close(releaseB)
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}
}

// RemoveServiceLock waits for the current holder and deletes the file only
// while holding it.
func TestRemoveServiceLockWaitsForHolder(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := store.ServiceLockPath("main", "web")

	release, entered := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.WithServiceLock("main", "web", func() error {
			close(entered)
			<-release
			if _, err := os.Stat(path); err != nil {
				t.Error("lock file was removed while a holder had it")
			}
			return nil
		})
	}()
	<-entered

	removed := make(chan error, 1)
	go func() { removed <- store.RemoveServiceLock("main", "web") }()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatalf("RemoveServiceLock: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed, stat err = %v", err)
	}

	// Already gone is not an error.
	if err := store.RemoveServiceLock("main", "web"); err != nil {
		t.Errorf("removing a missing lock file: %v", err)
	}
}
