package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

// keyStdin is where `key accept` reads the ciphertext result. The dispatcher gives a
// command only its writers, and one command needs the input stream; a package variable is
// the smallest seam that lets tests hand over a payload without replacing the process's
// own stdin.
var keyStdin io.Reader = os.Stdin

const KEY_ACCEPT_USAGE = `Usage: oh-my-cpa key accept --repo <repo> [--application-id <id>] [--json] [--home <dir>]

Reads the JSON returned by the MCP tool ` + "`repo_key_ciphertext`" + ` from stdin and
stores its envelope under $OMC_HOME/envelopes/. Both the whole MCP answer
({"status":"success","data":{...}}) and the bare data object are accepted.

The envelope is opened once in memory with the repository's private key before
it is stored, so an envelope sealed to another key is refused instead of
saved. Nothing from the input is echoed: neither the ciphertext nor the
plaintext appears in the output.

The private key must already exist; run ` + "`key gen --repo <repo>`" + ` first. Accepting
a new envelope for the same repository replaces the previous one.
`

func init() {
	registerKeyCommand(&keyCommand{
		Name:  "accept",
		Usage: "read a repo_key_ciphertext result on stdin and store its envelope",
		Run:   runKeyAccept,
	})
}

func runKeyAccept(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newKeyFlagSet("accept", stderr)
	repo := flags.String("repo", "", "repository identity, for example org/repo")
	applicationID := flags.String("application-id", "", "the application id to record with the envelope")
	asJSON := flags.Bool("json", false, "print a machine-readable result")
	homeFlag := flags.String("home", "", "where keys, envelopes and state live (default $OMC_HOME or ~/.omc)")
	rest, help, err := parseKeyFlags(flags, args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(stdout, KEY_ACCEPT_USAGE)
		return nil
	}
	if len(rest) > 0 {
		return keyclient.Errorf(keyclient.CODE_USAGE, "unexpected argument %q; pass the result on stdin", rest[0])
	}
	if strings.TrimSpace(*repo) == "" {
		return keyclient.Errorf(keyclient.CODE_REPO_REQUIRED, "no repository; pass --repo <org/repo>")
	}
	home, err := keyclient.ResolveHome(*homeFlag)
	if err != nil {
		return err
	}
	input, err := readAcceptInput(ctx, keyStdin)
	if err != nil {
		return keyclient.Wrapf(err, keyclient.CODE_ENVELOPE_INVALID, "cannot read the input from stdin: %v", err)
	}
	if len(input) == 0 {
		return keyclient.Errorf(keyclient.CODE_ENVELOPE_INVALID, "stdin is empty; pipe the result of repo_key_ciphertext into this command")
	}
	result, err := keyclient.Accept(keyclient.AcceptOptions{
		Home:          home,
		Repo:          *repo,
		ApplicationID: *applicationID,
		Input:         input,
		Now:           time.Now(),
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeAcceptJSON(stdout, result)
	}
	mask := result.Record.KeyMask
	if mask == "" {
		mask = "mask not stated"
	}
	verb := "accepted"
	if result.Replaced {
		verb = "replaced the envelope for"
	}
	fmt.Fprintf(stdout, "%s %s: key %s at %s\n", verb, keyclient.RepoLabel(result.Record), mask, result.Record.EnvelopeFile)
	return nil
}

type acceptJSON struct {
	Repo               string `json:"repo"`
	RepoDisplay        string `json:"repo_display"`
	ApplicationID      string `json:"application_id,omitempty"`
	ApplicationKind    string `json:"application_kind,omitempty"`
	KeyMask            string `json:"key_mask,omitempty"`
	ApprovalStatus     string `json:"approval_status"`
	StatusSource       string `json:"status_source"`
	EnvelopeFile       string `json:"envelope_file"`
	EnvelopeReceivedAt string `json:"envelope_received_at"`
	EnvelopeAlgorithm  string `json:"envelope_algorithm"`
	Replaced           bool   `json:"replaced"`
}

func writeAcceptJSON(writer io.Writer, result keyclient.AcceptResult) error {
	document := acceptJSON{
		Repo:               result.Record.Repo,
		RepoDisplay:        result.Record.RepoDisplay,
		ApplicationID:      result.Record.ApplicationID,
		ApplicationKind:    result.Record.ApplicationKind,
		KeyMask:            result.Record.KeyMask,
		ApprovalStatus:     result.Record.ApprovalStatus,
		StatusSource:       result.Record.StatusSource,
		EnvelopeFile:       result.Record.EnvelopeFile,
		EnvelopeReceivedAt: result.Record.EnvelopeReceivedAt.UTC().Format(time.RFC3339),
		EnvelopeAlgorithm:  result.Record.EnvelopeAlgorithm,
		Replaced:           result.Replaced,
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return keyclient.Wrapf(err, keyclient.CODE_STATE_CORRUPT, "cannot encode the result: %v", err)
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

// readAcceptInput reads at most one byte past the limit, so the parser can tell "too large"
// from "exactly at the limit" without buffering the rest.
func readAcceptInput(ctx context.Context, source io.Reader) ([]byte, error) {
	if source == nil {
		return nil, errors.New("no input stream")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(source, keyclient.MAX_ACCEPT_BYTES+1))
}
