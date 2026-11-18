//go:build !windows

package keyclient

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHomePermissionsAreOwnerOnly is the Unix evidence for AC-2's 0600 claim: the home
// and its subdirectories are 0700, and every file this package writes is 0600.
func TestHomePermissionsAreOwnerOnly(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{home.Root, home.KeysDir(), home.EnvelopesDir(), home.BackupsDir()} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != DIR_MODE {
			t.Fatalf("%s has mode %04o, want %04o", directory, mode, DIR_MODE)
		}
	}

	if _, err := WritePrivateKey(home, "org/repo", make([]byte, PRIVATE_KEY_BYTES)); err != nil {
		t.Fatal(err)
	}
	keyPath, err := home.KeyPath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, keyPath, FILE_MODE)

	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, home.StatePath(), FILE_MODE)

	if err := home.WriteConfig("http://127.0.0.1:8317", time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	assertMode(t, home.ConfigPath(), FILE_MODE)
}

// TestPrivateKeyKeepsItsModeUnderAPermissiveUmask catches the create-then-chmod gap: a
// umask that would make the new file readable by others must not leave it that way after
// the write returns.
func TestPrivateKeyKeepsItsModeUnderAPermissiveUmask(t *testing.T) {
	previous := syscall.Umask(0o000)
	defer syscall.Umask(previous)

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
	assertMode(t, keyPath, FILE_MODE)
}

// TestEnsureRefusesAGroupWritableHome is the home_insecure path: the key is not written
// into a directory another local account can replace files in.
func TestEnsureRefusesAGroupWritableHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate another account having loosened the home before this run.
	if err := os.Chmod(root, 0o777); err != nil {
		t.Skipf("cannot change modes on this filesystem: %v", err)
	}
	home := Home{Root: root}
	// An existing directory is inspected, not silently repaired: a home another account
	// can write to must stop the command so the person knows why.
	err := home.Ensure()
	if CodeOf(err) != CODE_HOME_INSECURE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_HOME_INSECURE, err)
	}
	if !strings.Contains(MessageOf(err), "chmod 700") {
		t.Fatalf("the message should name the fix: %q", MessageOf(err))
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o777 {
		t.Fatalf("Ensure changed a directory it should only have inspected: %04o", mode)
	}
}

// TestEnsureTightensADirectoryItCreates is the other half: a home this command creates is
// 0700 from the start, even under a umask that would leave it open.
func TestEnsureTightensADirectoryItCreates(t *testing.T) {
	previous := syscall.Umask(0o000)
	defer syscall.Umask(previous)

	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{home.Root, home.KeysDir(), home.EnvelopesDir(), home.BackupsDir()} {
		assertMode(t, directory, DIR_MODE)
	}
}

func TestVerifyDirectoryNamesTheFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Skipf("cannot change modes on this filesystem: %v", err)
	}
	if err := verifyDirectory(path); err != nil {
		t.Fatalf("0755 is readable by others but not writable: %v", err)
	}
	if err := os.Chmod(path, 0o775); err != nil {
		t.Fatal(err)
	}
	err := verifyDirectory(path)
	if CodeOf(err) != CODE_HOME_INSECURE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_HOME_INSECURE, err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != want {
		t.Fatalf("%s has mode %04o, want %04o", path, mode, want)
	}
}
