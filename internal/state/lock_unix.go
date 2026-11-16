//go:build unix

package state

import (
	"errors"
	"fmt"
	"os"
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
	return withFileLock(s.ServiceLockPath(branch, service), 2*time.Minute, fn)
}

// RemoveServiceLock deletes a service's lock file while holding it, so no
// holder loses its lock to the delete. A waiter that opened the old file
// notices it was removed once it gets the lock, and locks the new file instead
// (see withFileLock). A lock file that is already gone is not an error.
func (s *FileStore) RemoveServiceLock(branch, service string) error {
	path := s.ServiceLockPath(branch, service)
	for {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("opening lock file: %w", err)
		}
		if err := flockUntil(f, time.Now().Add(lockTimeout), lockTimeout); err != nil {
			_ = f.Close()
			return err
		}
		current, err := isCurrentFile(f, path)
		if err == nil && current {
			err = os.Remove(path)
		}
		unlockAndClose(f)
		if err != nil {
			return err
		}
		if current {
			return nil
		}
		// Replaced while we waited: the path now names a newer lock. Retry on it.
	}
}

// withFileLock runs fn holding an exclusive flock on path. A flock belongs to
// the open file, not the path, so a lock file deleted and recreated while this
// caller waited would leave it holding a lock on an unlinked file while another
// process holds the new one. After locking, it checks that path still names
// the file it locked, and starts over on the current file if not.
func withFileLock(path string, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return fmt.Errorf("opening lock file: %w", err)
		}
		if err := flockUntil(f, deadline, timeout); err != nil {
			_ = f.Close()
			return err
		}
		current, err := isCurrentFile(f, path)
		if err != nil {
			unlockAndClose(f)
			return err
		}
		if !current {
			unlockAndClose(f)
			continue
		}
		defer unlockAndClose(f)
		return fn()
	}
}

// flockUntil takes an exclusive flock on f, retrying until deadline. timeout
// is the caller's whole budget, for the error message.
func flockUntil(f *os.File, deadline time.Time, timeout time.Duration) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("acquiring lock: timed out after %v", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// isCurrentFile reports whether path still names the open file f, i.e. it was
// not removed or replaced since f was opened.
func isCurrentFile(f *os.File, path string) (bool, error) {
	held, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("checking lock file: %w", err)
	}
	onDisk, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking lock file: %w", err)
	}
	return os.SameFile(held, onDisk), nil
}

func unlockAndClose(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
