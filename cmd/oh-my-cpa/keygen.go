package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

const KEY_GEN_USAGE = `Usage: oh-my-cpa key gen --repo <repo> [--base-url <url>] [--home <dir>]

Generates an X25519 key pair for the repository and writes the private key
under $OMC_HOME/keys/ with owner-only permissions; the exact filename is
printed on stderr. The public key is the only thing written to stdout:
  x25519:<base64>

If a key already exists for the repository it is reused and its public key
printed again. gen never replaces a key, because that would make every
envelope already delivered for the repository unreadable. Replace one with
` + "`key purge --repo <repo>`" + ` followed by gen.

--base-url is remembered in $OMC_HOME/config.json for later commands.
`

func init() {
	registerKeyCommand(&keyCommand{
		Name:  "gen",
		Usage: "generate an X25519 key pair for a repository; print only the public key",
		Run:   runKeyGen,
	})
}

func runKeyGen(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newKeyFlagSet("gen", stderr)
	repo := flags.String("repo", "", "repository identity, for example org/repo")
	baseURL := flags.String("base-url", "", "the CPA gateway root; remembered in $OMC_HOME/config.json")
	homeFlag := flags.String("home", "", "where keys, envelopes and state live (default $OMC_HOME or ~/.omc)")
	rest, help, err := parseKeyFlags(flags, args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(stdout, KEY_GEN_USAGE)
		return nil
	}
	if len(rest) > 0 {
		return keyclient.Errorf(keyclient.CODE_USAGE, "unexpected argument %q", rest[0])
	}
	home, err := keyclient.ResolveHome(*homeFlag)
	if err != nil {
		return err
	}
	result, err := keyclient.Generate(keyclient.GenOptions{
		Home:    home,
		Repo:    *repo,
		BaseURL: *baseURL,
		Now:     time.Now(),
	})
	if err != nil {
		return err
	}
	// Everything except the key goes to stderr: AC-2 requires stdout to hold the public
	// key and nothing else, so a caller can pipe it straight into the apply call.
	if result.Reused {
		fmt.Fprintf(stderr, "reused the existing key for %s at %s\n", keyclient.RepoLabel(result.Record), result.Record.PrivateKeyFile)
	} else {
		fmt.Fprintf(stderr, "generated a key for %s at %s\n", keyclient.RepoLabel(result.Record), result.Record.PrivateKeyFile)
	}
	if result.WroteConfig {
		fmt.Fprintf(stderr, "saved the gateway address in %s\n", home.ConfigPath())
	}
	fmt.Fprintln(stdout, result.Pairs.PublicKey)
	return nil
}
