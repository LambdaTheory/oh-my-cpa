package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

const KEY_STATUS_USAGE = `Usage: oh-my-cpa key status [--repo <repo>] [--application-id <id>] [--remote] [--json] [--home <dir>]

Shows the local keys, the repository each belongs to, whether its envelope is
present, the last known application state, and which runtime has that key
installed. Without --remote this reads only $OMC_HOME and needs no network.

--application-id records which application belongs to the repository, which is
what you know between applying for a key and receiving it. It needs --repo
unless the repository already holds this id.

--remote asks OMC for the current state of the recorded applications and of
each repository's key. It needs ` + keyclient.ENV_SERVER_URL + ` and
` + keyclient.ENV_MANAGEMENT_KEY + `, the same pair the mcp command uses; without
them it reports remote_not_configured and changes nothing. It is the only
option that uses the network and the only one that writes state.
`

func init() {
	registerKeyCommand(&keyCommand{
		Name:  "status",
		Usage: "show local keys, their approval state and installed runtimes",
		Run:   runKeyStatus,
	})
}

func runKeyStatus(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newKeyFlagSet("status", stderr)
	repoFlag := flags.String("repo", "", "only this repository, for example org/repo")
	applicationID := flags.String("application-id", "", "associate this application id with --repo")
	remote := flags.Bool("remote", false, "ask OMC for the current application and repository state")
	asJSON := flags.Bool("json", false, "print a machine-readable report")
	homeFlag := flags.String("home", "", "where keys, envelopes and state live (default $OMC_HOME or ~/.omc)")
	rest, help, err := parseKeyFlags(flags, args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprint(stdout, KEY_STATUS_USAGE)
		return nil
	}
	if len(rest) > 0 {
		return keyclient.Errorf(keyclient.CODE_USAGE, "unexpected argument %q", rest[0])
	}
	home, err := keyclient.ResolveHome(*homeFlag)
	if err != nil {
		return err
	}
	applicationIDValue := strings.TrimSpace(*applicationID)
	repoInput := strings.TrimSpace(*repoFlag)
	filterRepo := ""
	if repoInput != "" {
		filterRepo, _, err = keyclient.DeriveRepoID(repoInput)
		if err != nil {
			return err
		}
	}
	if applicationIDValue != "" {
		repoID := filterRepo
		if repoID == "" {
			// The repository may already be recorded under this id; without that record
			// there is no way to know which repository the id belongs to.
			repoID = recordedRepoForApplication(home, applicationIDValue)
			if repoID == "" {
				return keyclient.Errorf(keyclient.CODE_REPO_REQUIRED, "--application-id needs --repo unless a recorded repository already holds this id")
			}
		}
		if _, err := keyclient.AssociateApplication(home, repoID, applicationIDValue, time.Now()); err != nil {
			return err
		}
	}
	config, err := home.ReadConfig()
	if err != nil {
		return err
	}
	baseURL, err := keyclient.ResolveBaseURL("", "", config)
	if err != nil {
		if keyclient.CodeOf(err) != keyclient.CODE_BASE_URL_REQUIRED {
			return err
		}
		baseURL = keyclient.BaseURL{Source: "absent"}
	}
	var failures []keyclient.RepoRefreshError
	if *remote {
		client, err := keyclient.NewRemoteClient()
		if err != nil {
			return err
		}
		store, err := keyclient.OpenStoreForUpdate(home, 0)
		if err != nil {
			return err
		}
		failures, err = keyclient.RefreshRemote(ctx, store, client, time.Now())
		closeErr := store.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	report, err := keyclient.BuildReport(home, baseURL, filterRepo)
	if err != nil {
		return err
	}
	report.ApplyRemoteFailures(failures)
	if *asJSON {
		if err := keyclient.WriteReportJSON(stdout, report); err != nil {
			return err
		}
	} else {
		keyclient.WriteReport(stdout, report)
	}
	if summary := keyclient.RefreshSummary(failures); summary != "" {
		return keyclient.Errorf(keyclient.CODE_REMOTE_UNAVAILABLE, "%s", summary)
	}
	return nil
}

// recordedRepoForApplication finds the repository an application id was recorded under, so
// `status --application-id <id>` works after the association was made once.
func recordedRepoForApplication(home keyclient.Home, applicationID string) string {
	store, err := keyclient.OpenStore(home)
	if err != nil {
		return ""
	}
	for repoID, record := range store.Repos() {
		if record.ApplicationID == applicationID {
			return repoID
		}
	}
	return ""
}
