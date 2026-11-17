package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

const KEY_INSTALL_USAGE = `Usage: oh-my-cpa key install --runtime <claude|codex|opencode> [--repo <repo>] [--base-url <url>] [--config <path>] [--replace] [--home <dir>]

Writes the repository key into the runtime's own configuration file. This is
the only command that leaves the plaintext key on disk, and it says so on
stderr before it writes. The previous file is copied into $OMC_HOME/backups/
first when it exists.

  claude    ~/.claude/settings.json, env.ANTHROPIC_BASE_URL and
            env.ANTHROPIC_AUTH_TOKEN (or $CLAUDE_CONFIG_DIR/settings.json)
  codex     $CODEX_HOME/config.toml or ~/.codex/config.toml, a managed
            [model_providers.oh-my-cpa] table plus model_provider
  opencode  ~/.config/opencode/opencode.json, provider.oh-my-cpa.options
            (or $OPENCODE_CONFIG)

Only the user-level file of each runtime is written: a project-level
.claude/settings.json belongs to a repository and would end up committed with
the key in it.

One installed slot per runtime per machine. Installing another repository
replaces the slot and the previous values survive only in the backup.
--replace is required when the slot holds a value this tool did not write.

--repo defaults to the repository of the current directory. --config names
the configuration file instead of the runtime's default.
`

func init() {
	registerKeyCommand(&keyCommand{
		Name:  "install",
		Usage: "write the key into a runtime's configuration file, after backing it up",
		Run:   runKeyInstall,
	})
}

func runKeyInstall(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newKeyFlagSet("install", stderr)
	runtimeFlag := flags.String("runtime", "", "claude, codex or opencode")
	repoFlag := flags.String("repo", "", "repository identity; defaults to the current directory's")
	baseURLFlag := flags.String("base-url", "", "the CPA gateway root")
	configFlag := flags.String("config", "", "the runtime's configuration file")
	replace := flags.Bool("replace", false, "overwrite a slot this tool did not install")
	homeFlag := flags.String("home", "", "where keys, envelopes and state live (default $OMC_HOME or ~/.omc)")
	rest, help, err := parseKeyFlags(flags, args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(stdout, KEY_INSTALL_USAGE)
		return nil
	}
	if len(rest) > 0 {
		return keyclient.Errorf(keyclient.CODE_USAGE, "unexpected argument %q", rest[0])
	}
	if strings.TrimSpace(*runtimeFlag) == "" {
		return keyclient.Errorf(keyclient.CODE_RUNTIME_UNKNOWN, "--runtime is required; use claude, codex or opencode")
	}
	runtime, err := keyclient.ParseRuntime(strings.TrimSpace(*runtimeFlag))
	if err != nil {
		return err
	}
	home, err := keyclient.ResolveHome(*homeFlag)
	if err != nil {
		return err
	}
	repoID, repoDisplay, err := keyclient.ResolveRepo(*repoFlag, ".")
	if err != nil {
		return err
	}

	// The key material is checked before anything is written, so a repository that has
	// no key or no envelope leaves no backup and no edited file behind.
	key, err := keyclient.InstallKeyMaterial(home, repoID)
	if err != nil {
		return err
	}
	record := keyclient.RepoState{Repo: repoID, RepoDisplay: repoDisplay}

	baseURL, err := resolveInstallBaseURL(home, *baseURLFlag)
	if err != nil {
		return err
	}

	result, err := keyclient.Install(keyclient.InstallOptions{
		Home:       home,
		Runtime:    runtime,
		Repo:       record,
		Key:        key,
		BaseURL:    baseURL.Root,
		ConfigPath: *configFlag,
		Replace:    *replace,
		Now:        time.Now(),
		Warn:       stderr,
	})
	if err != nil {
		return err
	}
	if baseURL.Source == "flag" {
		// Remember the address for the commands that follow, the way `key gen` does. The
		// install itself is already committed by this point, so a failure here is
		// reported against config.json rather than rolling the install back.
		if err := keyclient.WithHomeLock(home, 0, func() error {
			return home.WriteConfig(baseURL.Root, time.Now())
		}); err != nil {
			return err
		}
	}
	if baseURL.Insecure {
		fmt.Fprintf(stderr, "warning: %s is plain http to a host other than this machine, so the key crosses the network unencrypted\n", baseURL.Root)
	}
	if result.Unchanged {
		fmt.Fprintf(stdout, "%s already holds this key for %s in %s; nothing changed\n", runtime, keyclient.RepoLabel(record), result.ConfigPath)
		return nil
	}
	fmt.Fprintf(stdout, "installed %s for %s into %s\n", runtime, keyclient.RepoLabel(record), result.ConfigPath)
	if result.BackupPath != "" {
		fmt.Fprintf(stdout, "previous file backed up to %s\n", result.BackupPath)
	}
	if result.PreviousModelProvider != nil {
		fmt.Fprintf(stdout, "codex model_provider was %q and is saved for `key purge` to restore\n", *result.PreviousModelProvider)
	}
	fmt.Fprintf(stdout, "remove it with `oh-my-cpa key purge --repo %s --runtime %s`\n", repoID, runtime)
	return nil
}

// resolveInstallBaseURL applies the same precedence as the other key commands: the
// flag, then OMCPA_CPA_BASE_URL, then $OMC_HOME/config.json.
func resolveInstallBaseURL(home keyclient.Home, flagValue string) (keyclient.BaseURL, error) {
	config, err := home.ReadConfig()
	if err != nil {
		return keyclient.BaseURL{}, err
	}
	return keyclient.ResolveBaseURL(flagValue, os.Getenv(keyclient.ENV_CPA_BASE_URL), config)
}
