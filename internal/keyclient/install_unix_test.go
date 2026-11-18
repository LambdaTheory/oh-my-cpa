//go:build !windows

package keyclient

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestInstallFilePermissionsAreOwnerOnly is the Unix half of the permission claim for
// AC-8: the backup is 0600 like everything else this tool writes, because it can quote
// another file's secrets.
//
// Windows has no mode bits to assert; install_unix_test.go's counterpart claim there is
// the DACL implementation in home_windows.go, which restrictFile applies to every file
// this package creates.
func TestInstallFilePermissionsAreOwnerOnly(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	// A permissive umask must not leave the backup readable in the gap between create and
	// restrict, and must not leave the configuration group- or world-readable either.
	previous := syscall.Umask(0o000)
	t.Cleanup(func() { syscall.Umask(previous) })

	result := installForTest(t, home, repo, RuntimeClaude, config, false)
	if result.BackupPath == "" {
		t.Fatal("no backup was taken")
	}
	assertMode(t, result.BackupPath, FILE_MODE)
	assertMode(t, config, FILE_MODE)
	assertMode(t, home.StatePath(), FILE_MODE)
}

// TestInstallCreatesAMissingConfigWithOwnerOnlyMode covers the file-install creates from
// nothing: there was no previous file to copy, so nothing else would set its mode.
func TestInstallCreatesAMissingConfigWithOwnerOnlyMode(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	previous := syscall.Umask(0o000)
	t.Cleanup(func() { syscall.Umask(previous) })

	result := installForTest(t, home, repo, RuntimeClaude, config, false)
	if result.BackupPath != "" {
		t.Fatalf("a missing file produced a backup at %s", result.BackupPath)
	}
	assertMode(t, config, FILE_MODE)
}

// TestPurgeLeavesTheRemainingFilesOwnerOnly keeps the guarantee across a removal: an
// edited configuration is rewritten, so its mode is set again by the atomic write.
func TestPurgeLeavesTheRemainingFilesOwnerOnly(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, config, false)
	previous := syscall.Umask(0o000)
	t.Cleanup(func() { syscall.Umask(previous) })

	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, config, FILE_MODE)
	assertMode(t, home.StatePath(), FILE_MODE)
}
