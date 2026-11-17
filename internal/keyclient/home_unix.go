//go:build !windows

package keyclient

import (
	"os"
	"syscall"
)

// restrictDirectory narrows a directory to its owner. A failure to do so stops the
// command: a key under a directory another local account can write to is not a
// key that belongs to this user.
func restrictDirectory(path string) error {
	if err := os.Chmod(path, DIR_MODE); err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot restrict %s to this user: %v", path, err)
	}
	return verifyDirectory(path)
}

// verifyDirectory rejects a directory that group or others can write.
//
// Read and execute are allowed on purpose: a read-only mount of the home directory
// is a legitimate setup, and the write that follows fails on its own terms. Write
// permission for another account is the case that lets someone swap a key between
// generation and use.
func verifyDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return Errorf(CODE_HOME_INSECURE, "%s is writable by group or others (%04o); run `chmod 700 %s`", path, mode, path)
	}
	return nil
}

// restrictFile narrows a file to its owner and confirms the result.
func restrictFile(path string) error {
	if err := os.Chmod(path, FILE_MODE); err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot restrict %s to this user: %v", path, err)
	}
	return verifyFile(path)
}

// verifyFile rejects a file that any account other than its owner can read or write.
func verifyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return Errorf(CODE_HOME_INSECURE, "%s is accessible to other users (%04o); run `chmod 600 %s`", path, mode, path)
	}
	return nil
}

// syncDirectory flushes a directory entry after a rename so the new name survives a
// crash. Failure is not fatal: some filesystems (and Windows) do not support it.
func syncDirectory(path string) {
	if directory, err := os.Open(path); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
}

// tryLockFile attempts a non-blocking exclusive flock on an open handle. A lock held
// elsewhere is not an error, only a "not yet", so AcquireHomeLock can poll.
func tryLockFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return false, nil
	}
	return false, err
}

// unlockFile releases the flock. Closing the file releases it too.
func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
