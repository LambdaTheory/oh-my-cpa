package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

const KEY_PURGE_USAGE = `Usage: oh-my-cpa key purge --repo <repo> [--runtime <runtime>] [--dry-run] [--home <dir>]

Removes the repository's private key, envelope, installed values and state
record. With --runtime, removes only that runtime's installed value and leaves
the key and envelope in place.

A value in a runtime's configuration is removed only when it is this
repository's key; a value something else changed is left alone and reported.
Backups are kept unless they contain this repository's plaintext key, and those
go with the key. --dry-run prints what would be removed and changes nothing.

Purge is local. The key in CPA remains valid; stop or reissue it in the console
to revoke it.
`

func init() {
	registerKeyCommand(&keyCommand{
		Name:  "purge",
		Usage: "remove a repository's private key, envelope and installed values",
		Run:   runKeyPurge,
	})
}

func runKeyPurge(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newKeyFlagSet("purge", stderr)
	repoFlag := flags.String("repo", "", "repository identity, for example org/repo")
	runtimeFlag := flags.String("runtime", "", "limit the run to claude, codex or opencode")
	dryRun := flags.Bool("dry-run", false, "print what would be removed without changing anything")
	homeFlag := flags.String("home", "", "where keys, envelopes and state live (default $OMC_HOME or ~/.omc)")
	rest, help, err := parseKeyFlags(flags, args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(stdout, KEY_PURGE_USAGE)
		return nil
	}
	if len(rest) > 0 {
		return keyclient.Errorf(keyclient.CODE_USAGE, "unexpected argument %q", rest[0])
	}
	// Purge names its repository explicitly. Inferring it from the current directory
	// would let a `cd` decide which key gets deleted.
	if strings.TrimSpace(*repoFlag) == "" {
		return keyclient.Errorf(keyclient.CODE_REPO_REQUIRED, "--repo is required; pass the identity the key was installed for")
	}
	var runtime keyclient.Runtime
	if strings.TrimSpace(*runtimeFlag) != "" {
		parsed, err := keyclient.ParseRuntime(strings.TrimSpace(*runtimeFlag))
		if err != nil {
			return err
		}
		runtime = parsed
	}
	home, err := keyclient.ResolveHome(*homeFlag)
	if err != nil {
		return err
	}
	result, err := keyclient.Purge(keyclient.PurgeOptions{
		Repo:    *repoFlag,
		Runtime: runtime,
		DryRun:  *dryRun,
		Home:    home,
		Now:     time.Now(),
		Warn:    stderr,
	})
	reportPurge(stdout, stderr, result, *dryRun)
	if err != nil {
		return err
	}
	// The hint that matters most: purge cleaned this machine, not the gateway. Saying it
	// on success is what keeps purge from being mistaken for a revocation; a dry run says
	// the same thing in the conditional, because nothing has been cleared yet.
	if *dryRun {
		fmt.Fprintln(stdout, "nothing was changed; a real purge clears the local material only, and the key in CPA stays valid")
	} else {
		fmt.Fprintln(stdout, "local material cleared; the key in CPA is still valid — stop or reissue it in the console to revoke it")
	}
	return nil
}

// reportPurge prints the three lists a person needs: what went, what stayed and why,
// and what is still installed. On a dry run the wording is future tense throughout, so
// the output cannot be mistaken for a result.
func reportPurge(stdout, stderr io.Writer, result keyclient.PurgeResult, dryRun bool) {
	removedVerb, keptVerb := "removed", "kept"
	if dryRun {
		removedVerb = "would remove"
	}
	for _, path := range result.Removed {
		fmt.Fprintf(stdout, "%s: %s\n", removedVerb, path)
	}
	for _, note := range result.Kept {
		fmt.Fprintf(stderr, "%s: %s\n", keptVerb, note)
	}
	if result.BackupScanUnavailable {
		fmt.Fprintln(stderr, "warning: the private key is already gone, so a backup can only be recognised from what state recorded; the rest are kept")
	}
	if len(result.Removed) == 0 && len(result.Pending) == 0 {
		fmt.Fprintln(stdout, "nothing to remove; the repository was already clean")
	}
	for _, pending := range result.Pending {
		fmt.Fprintf(stderr, "still installed: %s\n", pending)
	}
}
