package keyclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestResolveHomePrecedence(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	t.Run("default is .omc under the user home", func(t *testing.T) {
		t.Setenv(ENV_HOME, "")
		home, err := ResolveHome("")
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(home.Root) != DEFAULT_HOME_DIRNAME {
			t.Fatalf("default home = %q, want a directory named %q", home.Root, DEFAULT_HOME_DIRNAME)
		}
		if !filepath.IsAbs(home.Root) {
			t.Fatalf("default home %q is not absolute", home.Root)
		}
	})

	t.Run("OMC_HOME overrides the default", func(t *testing.T) {
		configured := filepath.Join(t.TempDir(), "configured")
		t.Setenv(ENV_HOME, configured)
		home, err := ResolveHome("")
		if err != nil {
			t.Fatal(err)
		}
		if home.Root != configured {
			t.Fatalf("home = %q, want %q", home.Root, configured)
		}
	})

	t.Run("flag overrides OMC_HOME", func(t *testing.T) {
		t.Setenv(ENV_HOME, filepath.Join(t.TempDir(), "configured"))
		flagged := filepath.Join(t.TempDir(), "flagged")
		home, err := ResolveHome(flagged)
		if err != nil {
			t.Fatal(err)
		}
		if home.Root != flagged {
			t.Fatalf("home = %q, want the flag value %q", home.Root, flagged)
		}
	})

	t.Run("a relative value resolves against the working directory", func(t *testing.T) {
		t.Setenv(ENV_HOME, "relative-home")
		home, err := ResolveHome("")
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(working, "relative-home"); home.Root != want {
			t.Fatalf("home = %q, want %q", home.Root, want)
		}
	})
}

func TestEnsureCreatesLayout(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{home.Root, home.KeysDir(), home.EnvelopesDir(), home.BackupsDir()} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("%s: %v", directory, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", directory)
		}
	}
	if _, err := os.Stat(home.StatePath()); !os.IsNotExist(err) {
		t.Fatalf("Ensure created state.json; it should only create directories (err=%v)", err)
	}
}

func TestCheckReadableHomeRejectsAnInsecureDirectory(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := home.CheckReadableHome(); err != nil {
		t.Fatalf("a fresh home should pass: %v", err)
	}
	if err := os.Chmod(home.Root, 0o777); err != nil {
		t.Skipf("cannot change the mode on this platform: %v", err)
	}
	err := home.CheckReadableHome()
	if CodeOf(err) != CODE_HOME_INSECURE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_HOME_INSECURE, err)
	}
}

func TestCheckReadableHomeReportsAMissingHome(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "absent")}
	err := home.CheckReadableHome()
	if CodeOf(err) != CODE_HOME_UNAVAILABLE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_HOME_UNAVAILABLE, err)
	}
	if !strings.Contains(MessageOf(err), "key gen") {
		t.Fatalf("message should point at key gen: %q", MessageOf(err))
	}
}

func TestFileStemMapping(t *testing.T) {
	cases := []struct {
		name    string
		repo    string
		want    string
		missing string
	}{
		{name: "org and repo are percent-encoded", repo: "org/repo", want: "org%2Frepo-"},
		{name: "host path keeps its separators encoded", repo: "gitlab.example.com/Group/Repo", want: "gitlab.example.com%2FGroup%2FRepo-"},
		{name: "a Windows reserved name is prefixed", repo: "con", want: "_con-"},
		{name: "a reserved name with an extension is prefixed", repo: "CON.json", want: "_CON.json-"},
		{name: "an ordinary local name is unchanged", repo: "my-repo_1.2", want: "my-repo_1.2-"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stem, err := fileStem(testCase.repo)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(stem, testCase.want) {
				t.Fatalf("stem(%q) = %q, want prefix %q", testCase.repo, stem, testCase.want)
			}
			if len(stem) < len(testCase.want)+8 {
				t.Fatalf("stem(%q) = %q, want an eight-character hash suffix", testCase.repo, stem)
			}
		})
	}
}

func TestFileStemIsCaseSensitiveAndStable(t *testing.T) {
	lower, err := fileStem("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := fileStem("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if lower != repeat {
		t.Fatalf("the same identity produced %q and %q", lower, repeat)
	}
	withCase, err := fileStem("Org/Repo")
	if err != nil {
		t.Fatal(err)
	}
	if lower == withCase {
		t.Fatalf("%q and %q collided; the hash must distinguish case on a case-insensitive filesystem", lower, withCase)
	}
}

func TestFileStemTruncatesOnARuneBoundary(t *testing.T) {
	long := "组织/" + strings.Repeat("репозиторий", 20)
	slug := fileSlug(long)
	if len(slug) > SLUG_MAX_BYTES {
		t.Fatalf("slug length %d exceeds the %d-byte cap: %q", len(slug), SLUG_MAX_BYTES, slug)
	}
	if !utf8.ValidString(slug) {
		t.Fatalf("truncation split a rune: %q", slug)
	}
	// The hash still tells two long identities apart even when their slugs are equal.
	stem, err := fileStem(long)
	if err != nil {
		t.Fatal(err)
	}
	other, err := fileStem(long + "x")
	if err != nil {
		t.Fatal(err)
	}
	if other == stem {
		t.Fatalf("two long identities collided: %q", stem)
	}
}

func TestFileStemRejectsAnEmptyIdentity(t *testing.T) {
	if _, err := fileStem("  "); CodeOf(err) != CODE_REPO_REQUIRED {
		t.Fatalf("code = %q, want %q", CodeOf(err), CODE_REPO_REQUIRED)
	}
}

func TestFileStemTrimsTrailingDotsAndSpaces(t *testing.T) {
	if slug := fileSlug("repo."); slug != "repo" {
		t.Fatalf("slug = %q, want %q", slug, "repo")
	}
	if slug := fileSlug("repo "); slug != "repo" {
		t.Fatalf("slug = %q, want %q", slug, "repo")
	}
}

func TestBackupPathIsUnique(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 17, 9, 15, 0, 0, time.UTC)
	first, err := home.BackupPath("/home/user/.claude/settings.json", at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != home.BackupsDir() {
		t.Fatalf("backup %q is outside %q", first, home.BackupsDir())
	}
	if !strings.HasSuffix(first, ".json") {
		t.Fatalf("backup %q should keep the .json extension", first)
	}
	if strings.Contains(filepath.Base(first), "/") {
		t.Fatalf("backup name contains a separator: %q", first)
	}
	if err := os.WriteFile(first, []byte("{}"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	second, err := home.BackupPath("/home/user/.claude/settings.json", at)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("the second backup reused %q", first)
	}
}
