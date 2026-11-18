//go:build windows

package keyclient

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// restrictDirectory replaces the directory's DACL with a single entry for the
// current user and stops inheritance, so nothing inherited from a parent folder can
// grant another account access.
//
// FAT32, exFAT and some network shares have no ACL support. SetNamedSecurityInfo
// then fails and the command stops with home_insecure: refusing to write a private
// key is better than writing one everybody can read.
func restrictDirectory(path string) error {
	if err := setOwnerOnlyDACL(path, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT); err != nil {
		return err
	}
	return verifyOwnerOnly(path)
}

// restrictFile narrows a file to the current user with no inheritance flags.
func restrictFile(path string) error {
	if err := setOwnerOnlyDACL(path, windows.NO_INHERITANCE); err != nil {
		return err
	}
	return verifyFile(path)
}

func setOwnerOnlyDACL(path string, inheritance uint32) error {
	user, err := currentUserSID()
	if err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot read the current user's SID: %v", err)
	}
	var pinner runtime.Pinner
	pinner.Pin(user)
	defer pinner.Unpin()

	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user),
		},
	}}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot build an ACL for %s: %v", path, err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot restrict %s to this user (does the filesystem support ACLs?): %v", path, err)
	}
	return nil
}

// verifyDirectory checks that no allow entry in the DACL names a broad group. The
// current user's own entry is expected; Everyone, Users and Authenticated Users are
// what would let another account read or replace a key.
func verifyDirectory(path string) error {
	return verifyOwnerOnly(path)
}

// verifyFile is the file counterpart of verifyDirectory.
func verifyFile(path string) error {
	return verifyOwnerOnly(path)
}

func verifyOwnerOnly(path string) error {
	if _, err := os.Stat(path); err != nil {
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", path, err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot read the ACL of %s: %v", path, err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot read the ACL control bits of %s: %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return Errorf(CODE_HOME_INSECURE, "%s still inherits permissions from its parent; restrict it to this user", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return Wrapf(err, CODE_HOME_INSECURE, "cannot read the ACL of %s: %v", path, err)
	}
	broad := []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,
		windows.WinAuthenticatedUserSid,
		windows.WinBuiltinUsersSid,
		windows.WinBuiltinGuestsSid,
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return Wrapf(err, CODE_HOME_INSECURE, "cannot read entry %d of the ACL of %s: %v", index, path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		for _, wellKnown := range broad {
			if sid.IsWellKnown(wellKnown) {
				return Errorf(CODE_HOME_INSECURE, "%s grants access to %s; restrict it to this user", path, sid.String())
			}
		}
	}
	return nil
}

// currentUserSID is the SID of the account running this process, which is the only
// account that should have access to $OMC_HOME.
func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

// syncDirectory is a no-op: Windows has no directory fsync, and a file handle's own
// Sync is what the atomic write uses.
func syncDirectory(string) {}

// tryLockFile takes a non-blocking exclusive byte-range lock on the whole file. A
// lock held elsewhere reports ERROR_LOCK_VIOLATION, which is the "not yet" case
// AcquireHomeLock polls on.
func tryLockFile(file *os.File) (bool, error) {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped,
	)
	if err == nil {
		return true, nil
	}
	if err == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	return false, err
}

// unlockFile releases the byte-range lock.
func unlockFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, new(windows.Overlapped))
}
