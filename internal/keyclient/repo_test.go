package keyclient

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo builds a throwaway repository under t.TempDir() so the inference tests do
// not depend on the checkout they run from.
func initGitRepo(t *testing.T, remotes map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed in this environment: %v", err)
	}
	directory := t.TempDir()
	run := func(arguments ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
		command.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
		}
	}
	run("init", "--quiet")
	for name, url := range remotes {
		run("remote", "add", name, url)
	}
	return directory
}

func TestInferRepoPrefersOrigin(t *testing.T) {
	directory := initGitRepo(t, map[string]string{
		"origin": "git@github.com:Org/Repo.git",
		"backup": "git@github.com:Other/Elsewhere.git",
	})
	repoID, display, err := InferRepo(directory)
	if err != nil {
		t.Fatal(err)
	}
	if repoID != "org/repo" {
		t.Fatalf("repo = %q, want %q", repoID, "org/repo")
	}
	if display != "Org/Repo" {
		t.Fatalf("display = %q, want the committed spelling %q", display, "Org/Repo")
	}
}

func TestInferRepoFallsBackToTheFirstRemote(t *testing.T) {
	directory := initGitRepo(t, map[string]string{
		"upstream": "git@github.com:Org/Repo.git",
	})
	repoID, _, err := InferRepo(directory)
	if err != nil {
		t.Fatal(err)
	}
	if repoID != "org/repo" {
		t.Fatalf("repo = %q, want %q", repoID, "org/repo")
	}
}

func TestInferRepoFallsBackToTheDirectoryName(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "my-local-repo")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	repoID, display, err := InferRepo(directory)
	if err != nil {
		t.Fatal(err)
	}
	if repoID != "my-local-repo" {
		t.Fatalf("repo = %q, want the directory name %q", repoID, "my-local-repo")
	}
	if display != "my-local-repo" {
		t.Fatalf("display = %q", display)
	}
}

// TestInferRepoSurvivesABrokenRemote is the degradation the plan requires: a remote git
// cannot resolve is not an error, because a checkout without a usable remote is still a
// place a key can be kept.
func TestInferRepoSurvivesABrokenRemote(t *testing.T) {
	directory := initGitRepo(t, map[string]string{
		"origin": "/nonexistent/path/that/does/not/exist",
	})
	if _, _, err := InferRepo(directory); err != nil {
		t.Fatalf("a broken remote should degrade, not fail: %v", err)
	}
}

func TestResolveRepoUsesTheFlagVerbatim(t *testing.T) {
	repoID, display, err := ResolveRepo("Some/Repo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if repoID != "some/repo" || display != "Some/Repo" {
		t.Fatalf("repo = %q, display = %q", repoID, display)
	}
}

// TestResolveRepoRefusesAPath: --repo names a repository identity. A path-shaped value
// must fail with repo_invalid instead of being reduced to its last segment, which would
// silently select a different key.
func TestResolveRepoRefusesAPath(t *testing.T) {
	for _, value := range []string{
		"../../../etc/passwd",
		"a/../../b",
		"/etc/passwd",
		`E:\works\repo`,
		"./repo",
		`..\repo`,
	} {
		if _, _, err := ResolveRepo(value, t.TempDir()); CodeOf(err) != CODE_REPO_INVALID {
			t.Fatalf("ResolveRepo(%q) error = %v, want %s", value, err, CODE_REPO_INVALID)
		}
	}
}

func TestResolveRepoInfersWhenTheFlagIsEmpty(t *testing.T) {
	directory := initGitRepo(t, map[string]string{"origin": "https://github.com/org/repo"})
	repoID, _, err := ResolveRepo("", directory)
	if err != nil {
		t.Fatal(err)
	}
	if repoID != "org/repo" {
		t.Fatalf("repo = %q, want the inferred %q", repoID, "org/repo")
	}
}

func TestFirstLineSkipsBlankOutput(t *testing.T) {
	if got := firstLine("\n  \n git@github.com:Org/Repo.git \n"); got != "git@github.com:Org/Repo.git" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine("\n \n"); got != "" {
		t.Fatalf("firstLine on blank output = %q", got)
	}
}
