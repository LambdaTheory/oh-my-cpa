package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

// KEY_USAGE lists the commands that are actually compiled in, so a build with one slice
// merged and another not yet describes itself accurately.
const KEY_USAGE_PREAMBLE = "Usage: oh-my-cpa key <command> [options]\n" +
	"\n" +
	"Manage the repository keys this machine holds for Oh My CPA. The key stays\n" +
	"local: `exec` opens the delivery envelope in memory and gives one child\n" +
	"process the variables its runtime needs; `install` writes it to a runtime's\n" +
	"own configuration file only when asked.\n" +
	"\n" +
	"Commands:\n"

const KEY_USAGE_EPILOGUE = "\n" +
	"Run `oh-my-cpa key <command> --help` for one command's options.\n" +
	"\n" +
	"Environment:\n" +
	"  OMC_HOME                  where keys, envelopes and state live; default\n" +
	"                            ~/.omc, created with owner-only permissions\n" +
	"  " + keyclient.ENV_CPA_BASE_URL + "        the CPA gateway this key is used against; also\n" +
	"                            accepted as --base-url; `key gen` and `key install`\n" +
	"                            remember it in $OMC_HOME/config.json\n" +
	"  OMCPA_SERVER_URL          OMC console URL, for `status --remote`\n" +
	"  OMCPA_CPA_MANAGEMENT_KEY  the management key, for `status --remote`\n" +
	"  OMCPA_AGENT_NAME          the agent name reported by `status --remote`\n" +
	"  OMCPA_DEVICE_NAME         the device name reported by `status --remote`\n" +
	"\n" +
	"Exit codes:\n" +
	"  0  success\n" +
	"  1  operation failed\n" +
	"  2  usage error\n" +
	"  3  configuration missing or invalid\n" +
	"  4  key, envelope, application or installed value not found\n" +
	"  5  conflict: a different key or value already occupies the place\n" +
	"\n" +
	"`exec` exits with the child's exit code once the child has started.\n"

// keyCommand is one `key` subcommand.
//
// Subcommands are registered rather than switched on so each slice of the feature can
// be developed and merged without editing this file: a new file registers from its own
// init, and both help and dispatch pick it up. Nothing depends on registration order,
// so adding a file cannot reorder existing output.
type keyCommand struct {
	// Name is the word after `key`.
	Name string
	// Usage is the one-line description shown in the top-level help.
	Usage string
	// Run executes the command with the arguments after its name. The returned error
	// carries the exit code; nil means success.
	Run func(ctx context.Context, args []string, stdout, stderr io.Writer) error
}

// keyCommands holds every registered subcommand, keyed by Name.
var keyCommands = map[string]*keyCommand{}

// registerKeyCommand adds a subcommand. A duplicate name or a nil Run is a programming
// error and panics at startup: two files defining `accept` would otherwise resolve by
// map iteration order, which is neither stable nor reviewable.
func registerKeyCommand(command *keyCommand) {
	if command == nil || strings.TrimSpace(command.Name) == "" {
		panic("keyclient: a key command needs a name")
	}
	if command.Run == nil {
		panic("keyclient: key command " + command.Name + " has no Run")
	}
	if _, exists := keyCommands[command.Name]; exists {
		panic("keyclient: duplicate key command " + command.Name)
	}
	keyCommands[command.Name] = command
}

// sortedKeyCommands returns the registered commands in name order.
func sortedKeyCommands() []*keyCommand {
	commands := make([]*keyCommand, 0, len(keyCommands))
	for _, command := range keyCommands {
		commands = append(commands, command)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands
}

// KeyUsage renders the top-level help from the registered commands.
func KeyUsage() string {
	var builder strings.Builder
	builder.WriteString(KEY_USAGE_PREAMBLE)
	names := sortedKeyCommands()
	width := 0
	for _, command := range names {
		if len(command.Name) > width {
			width = len(command.Name)
		}
	}
	for _, command := range names {
		fmt.Fprintf(&builder, "  %-*s  %s\n", width, command.Name, command.Usage)
	}
	builder.WriteString(KEY_USAGE_EPILOGUE)
	return builder.String()
}

// runKey dispatches `oh-my-cpa key ...` and returns the process exit code.
//
// It runs no part of the service: no environment file, no configuration, no database,
// no gateway client, and no signal handler. A key command must work on a machine that
// has never run the console, and a handler that caught Ctrl-C would keep a child
// started by `key exec` from seeing it.
func runKey(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, KeyUsage())
		return keyclient.EXIT_USAGE
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, KeyUsage())
		return keyclient.EXIT_OK
	}
	command, ok := keyCommands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, KeyUsage())
		return keyclient.EXIT_USAGE
	}
	if err := command.Run(ctx, args[1:], stdout, stderr); err != nil {
		if code := exitCodeCarrierOf(err); code >= 0 {
			return code
		}
		fmt.Fprintf(stderr, "%s\n", keyclientMessage(err))
		return keyclient.ExitCode(err)
	}
	return keyclient.EXIT_OK
}

// exitCodeCarrier is an error whose message was already written to stderr, carrying only
// the status still to return. `key exec` uses it to pass a child's status through
// without printing a diagnostic of its own.
type exitCodeCarrier struct {
	Status  int
	Message string
}

func (e *exitCodeCarrier) Error() string { return e.Message }

// exitCodeCarrierOf returns the carried status, or -1 when err is not one.
func exitCodeCarrierOf(err error) int {
	var carrier *exitCodeCarrier
	if errors.As(err, &carrier) {
		return carrier.Status
	}
	return -1
}

// keyclientMessage is the one line printed for a failure: the stable code, then the
// human sentence.
func keyclientMessage(err error) string {
	if code := keyclient.CodeOf(err); code != "" {
		return code + ": " + keyclient.MessageOf(err)
	}
	return err.Error()
}

// parseKeyFlags parses a command's own flags, keeping every argument after "--" for the
// command to run. flag.Parse stops at the terminator and drops it; `key exec` needs both
// the delimiter and the child's arguments, which may themselves start with "-".
//
// A help request is detected before parsing, because flag would otherwise reject -h as
// an unknown flag rather than let the command print its usage.
func parseKeyFlags(flags *flag.FlagSet, args []string) (rest []string, help bool, err error) {
	before := args
	for index, argument := range args {
		if argument == "--" {
			before = args[:index]
			rest = args[index+1:]
			break
		}
	}
	for _, argument := range before {
		if argument == "-h" || argument == "--help" || argument == "-help" {
			return rest, true, nil
		}
	}
	if parseErr := flags.Parse(before); parseErr != nil {
		return nil, false, keyclient.Errorf(keyclient.CODE_USAGE, "%v", parseErr)
	}
	if rest == nil {
		rest = flags.Args()
	}
	return rest, false, nil
}

// newKeyFlagSet builds a FlagSet whose errors go to stderr, with usage suppressed so a
// command prints its own.
func newKeyFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("key "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {}
	return flags
}
