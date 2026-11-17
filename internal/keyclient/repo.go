package keyclient

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

const (
	// GIT_TIMEOUT bounds the two git invocations used to read a remote. A repository on
	// a slow or unreachable network share must not make `key exec` hang before it
	// starts the child.
	GIT_TIMEOUT = 5 * time.Second

	// MAX_GIT_OUTPUT bounds what is read from git; a remote URL is short, and a runaway
	// command should not be able to fill memory.
	MAX_GIT_OUTPUT = 8 << 10
)

// InferRepo determines the repository of a directory: its `origin` remote, else its
// first remote, else the directory's own name.
//
// The remote is preferred because the same clone exists under many directory names and
// the identity must match what the console issued the key for. Every failure degrades
// silently to the next source: a missing git binary, a repository with no remotes and a
// timeout all mean "this checkout does not tell us", which is normal and not an error
// for the user to fix.
func InferRepo(dir string) (repoID, repoDisplay string, err error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", "", Wrapf(err, CODE_REPO_INVALID, "cannot resolve %q: %v", dir, err)
	}
	for _, candidate := range remoteCandidates(absolute) {
		repoID, _, normalizeErr := repokey.NormalizeRepo(candidate)
		if normalizeErr != nil || strings.TrimSpace(repoID) == "" {
			continue
		}
		display, displayErr := repokey.RepoDisplay(candidate)
		if displayErr != nil || strings.TrimSpace(display) == "" {
			display = repoID
		}
		return repoID, display, nil
	}
	// Local identity: the directory's own name, which NormalizeRepo also reduces a path
	// to. A checkout with no remote is still a place a key can be kept.
	return DeriveRepoID(absolute)
}

// ResolveRepo applies the precedence `--repo` > inferred from dir. The flag is taken
// verbatim because a caller who names a repository means it.
func ResolveRepo(flagValue, dir string) (repoID, repoDisplay string, err error) {
	if strings.TrimSpace(flagValue) != "" {
		return DeriveRepoID(flagValue)
	}
	return InferRepo(dir)
}

// remoteCandidates returns the URLs to try, in order: origin, then every other
// remote in git's own order. A remote that cannot be read is skipped.
func remoteCandidates(dir string) []string {
	if output, ok := runGit(dir, "remote", "get-url", "origin"); ok {
		if value := firstLine(output); value != "" {
			return []string{value}
		}
	}
	output, ok := runGit(dir, "remote")
	if !ok {
		return nil
	}
	candidates := make([]string, 0, 4)
	for _, name := range strings.Split(output, "\n") {
		name = strings.TrimSpace(name)
		if name == "" || name == "origin" {
			continue
		}
		urlOutput, ok := runGit(dir, "remote", "get-url", name)
		if !ok {
			continue
		}
		if value := firstLine(urlOutput); value != "" {
			candidates = append(candidates, value)
		}
	}
	return candidates
}

// runGit runs one git command in dir with a bounded timeout. It reports ok=false for
// every failure, including git not being installed, because every caller treats that
// the same way.
func runGit(dir string, arguments ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), GIT_TIMEOUT)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, arguments...)...)
	// Never prompt: this runs before a terminal interaction, and a credential prompt
	// would look like a hang rather than the fallback it should be.
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	command.Stdin = nil
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) || ctx.Err() != nil {
			return "", false
		}
		return "", false
	}
	if len(output) > MAX_GIT_OUTPUT {
		output = output[:MAX_GIT_OUTPUT]
	}
	return string(output), true
}

// firstLine returns the first non-empty line of git's output, trimmed. git may print a
// warning before the value, so a blank leading line is skipped rather than returned.
func firstLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
