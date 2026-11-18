package keyclient

import (
	"os"
	"time"
)

// LOCK_TIMEOUT is how long a write command waits for another one holding the
// per-home lock. Writes are short (a state rewrite, a key file), so anything longer
// means something is wrong and the caller should be told rather than made to wait.
const LOCK_TIMEOUT = 5 * time.Second

// LOCK_POLL_INTERVAL is how often a waiting command retries. Short enough that a
// normal handover is not noticeable, long enough not to spin a core.
const LOCK_POLL_INTERVAL = 25 * time.Millisecond

// Lock is an exclusive advisory lock on $OMC_HOME/.lock.
//
// The lock is held on an open handle, not on the existence of a file, so a process
// that dies releases it with its handle: a crash cannot leave the next command
// blocked forever. Every command that writes state or a key takes it; read-only
// commands do not, because a consistent read of an atomically renamed file does not
// need one.
type Lock struct {
	file *os.File
}

// AcquireHomeLock takes the lock, waiting up to timeout for another holder to
// release it. A zero timeout means the package default.
func AcquireHomeLock(home Home, timeout time.Duration) (*Lock, error) {
	if timeout <= 0 {
		timeout = LOCK_TIMEOUT
	}
	file, err := os.OpenFile(home.LockPath(), os.O_RDWR|os.O_CREATE, FILE_MODE)
	if err != nil {
		return nil, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot open the lock file %s: %v", home.LockPath(), err)
	}
	deadline := time.Now().Add(timeout)
	for {
		acquired, err := tryLockFile(file)
		if err != nil {
			file.Close()
			return nil, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot lock %s: %v", home.LockPath(), err)
		}
		if acquired {
			return &Lock{file: file}, nil
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, Errorf(CODE_STATE_LOCKED, "another oh-my-cpa key command has held %s for more than %s", home.LockPath(), timeout)
		}
		time.Sleep(LOCK_POLL_INTERVAL)
	}
}

// Release drops the lock. Closing the handle also releases it, so a Release that
// fails has still left the next command unblocked.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// WithHomeLock runs fn while holding the per-home write lock.
func WithHomeLock(home Home, timeout time.Duration, fn func() error) error {
	lock, err := AcquireHomeLock(home, timeout)
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}
