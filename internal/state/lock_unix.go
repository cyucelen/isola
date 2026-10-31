//go:build unix

package state

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// WithLock executes fn while holding an exclusive file lock.
func (s *FileStore) WithLock(fn func() error) error {
	return withFileLock(s.lockPath, lockTimeout, fn)
}

// WithServiceLock serializes lifecycle changes across CLI and proxy processes.
// It is separate from the state lock so setup never blocks unrelated services.
func (s *FileStore) WithServiceLock(branch, service string, fn func() error) error {
	key := sha256.Sum256([]byte(PortKey(branch, service)))
	return withFileLock(filepath.Join(s.dir, fmt.Sprintf("service-%x.lock", key)), 2*time.Minute, fn)
}

func withFileLock(path string, timeout time.Duration, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	defer func() { _ = f.Close() }()

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("acquiring lock: timed out after %v", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	return fn()
}
