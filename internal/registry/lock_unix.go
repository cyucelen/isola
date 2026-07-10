//go:build unix

package registry

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// withLock executes fn while holding an exclusive lock on the registry.
func (s *Store) withLock(fn func() error) error {
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening registry lock: %w", err)
	}
	defer func() { _ = f.Close() }()

	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("acquiring registry lock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("acquiring registry lock: timed out after %v", lockTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	return fn()
}
