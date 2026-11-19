package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

const KEY_EXEC_USAGE = `Usage: oh-my-cpa key exec --runtime <claude|codex|opencode> [--repo <repo>] [--base-url <url>] [--models <ids>] [--home <dir>] -- <command> [args...]

Opens the repository's envelope in memory and runs <command> with only the
variables that runtime needs. The plaintext is never written to disk, never
printed and never put in this process's command line. The child inherits
stdin, stdout, stderr and the rest of the environment.

claude    sets ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN; removes
          ANTHROPIC_API_KEY for the child because it may take precedence
codex     requires <command> to be codex itself; inserts -c provider
          overrides and passes the key through OMC_REPO_KEY (Codex is not
          known to select a provider from the environment alone)
opencode  sets OPENCODE_CONFIG_CONTENT to the user's configuration merged
          with provider.oh-my-cpa, including the gateway's model list; the
          key is in that variable only

--repo defaults to the repository of the current directory (its git origin,
or the directory name when there is no remote).

--models takes a comma-separated list of model ids for the opencode runtime
and is only valid there. Without it, exec asks the gateway for GET
<base-url>/v1/models before the child starts; a gateway that does not answer
fails the command with models_unavailable instead of starting OpenCode with
a provider it would refuse at the first request. Pass --models when the
gateway cannot be reached from this machine.

Everything after -- is the command and its arguments, passed through
unchanged: this command runs no shell and expands nothing.

The command's exit code becomes this command's exit code once the command
has started.
`

func init() {
	registerKeyCommand(&keyCommand{
		Name:  "exec",
		Usage: "run a command with the repository key in its environment",
		Run:   runKeyExec,
	})
}

func runKeyExec(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newKeyFlagSet("exec", stderr)
	runtimeName := flags.String("runtime", "", "claude, codex or opencode")
	repo := flags.String("repo", "", "repository identity; defaults to the current directory's")
	baseURL := flags.String("base-url", "", "the CPA gateway root; overrides the environment and config.json")
	modelsFlag := flags.String("models", "", "comma-separated OpenCode model ids; default: read the gateway's /v1/models")
	homeFlag := flags.String("home", "", "where keys, envelopes and state live (default $OMC_HOME or ~/.omc)")
	// parseKeyFlags keeps everything after "--" and hands it back as rest; nothing
	// between the flags and that terminator is a command, so anything there is a
	// mistake worth reporting rather than something to run.
	rest, help, err := parseKeyFlags(flags, args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(stdout, KEY_EXEC_USAGE)
		return nil
	}
	if flags.NArg() > 0 {
		return keyclient.Errorf(keyclient.CODE_USAGE, "unexpected argument %q; the command to run goes after --", flags.Arg(0))
	}
	if len(rest) == 0 {
		return keyclient.Errorf(keyclient.CODE_COMMAND_REQUIRED, "no command; put the command after `--`, for example `oh-my-cpa key exec --runtime claude -- claude`")
	}
	runtime, err := keyclient.ParseRuntime(*runtimeName)
	if err != nil {
		return err
	}
	models, err := keyclient.ParseModelList(*modelsFlag)
	if err != nil {
		return err
	}
	if len(models) > 0 && runtime != keyclient.RuntimeOpenCode {
		return keyclient.Errorf(keyclient.CODE_USAGE, "--models applies only to --runtime opencode")
	}
	home, err := keyclient.ResolveHome(*homeFlag)
	if err != nil {
		return err
	}
	status, err := keyclient.RunChild(ctx, keyclient.ExecOptions{
		Runtime: runtime,
		Repo:    *repo,
		BaseURL: *baseURL,
		Models:  models,
		Argv:    rest,
		Home:    home,
		Stdin:   os.Stdin,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	if err != nil {
		return err
	}
	if status != keyclient.EXIT_OK {
		// The child's own status, passed through without a diagnostic: whatever went
		// wrong is the child's to report, and it has already had the chance.
		return &exitCodeCarrier{Status: status, Message: "child exited with status " + strconv.Itoa(status)}
	}
	return nil
}
