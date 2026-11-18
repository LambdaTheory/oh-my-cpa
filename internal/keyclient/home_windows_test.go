//go:build windows

package keyclient

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestHomeACLIsOwnerOnly is the Windows counterpart of the Unix 0600 assertion.
//
// It deliberately does not compare Mode().Perm() with 0600: on Windows that only tracks
// the read-only bit, so the comparison would say nothing about who can open the file.
// The claim being tested is the one that matters — no allow entry names Everyone,
// Authenticated Users, Users or Guests, and the DACL no longer inherits.
func TestHomeACLIsOwnerOnly(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePrivateKey(home, "org/repo", make([]byte, PRIVATE_KEY_BYTES)); err != nil {
		t.Fatal(err)
	}
	keyPath, err := home.KeyPath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home.Root, home.KeysDir(), home.EnvelopesDir(), home.BackupsDir(), keyPath} {
		assertOwnerOnlyACL(t, path)
	}
}

// TestHomeACLIsInheritedByNewFiles checks that a file created beside the key inherits
// the restriction, so a future command that writes state or a backup does not have to
// know the ACL rules.
func TestHomeACLIsInheritedByNewFiles(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home.Root, "new-file.json")
	if err := os.WriteFile(path, []byte("{}"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnlyACL(t, path)
}

func assertOwnerOnlyACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if descriptor == nil {
		t.Fatalf("%s has no security descriptor", path)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("%s still inherits its permissions", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatalf("%s entry %d: %v", path, index, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		for _, broad := range []windows.WELL_KNOWN_SID_TYPE{
			windows.WinWorldSid,
			windows.WinAuthenticatedUserSid,
			windows.WinBuiltinUsersSid,
			windows.WinBuiltinGuestsSid,
		} {
			if sid.IsWellKnown(broad) {
				t.Fatalf("%s grants access to %s", path, sid.String())
			}
		}
	}
}
