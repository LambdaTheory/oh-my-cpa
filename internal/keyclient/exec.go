package keyclient

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// MAX_ENVELOPE_BYTES bounds the envelope this command will read. An envelope is a few
// hundred bytes; a file larger than this is not one, and reading it whole would let a
// mistaken path fill memory before the format check can refuse it.
const MAX_ENVELOPE_BYTES = 1 << 20

// ExecOptions is one `key exec` run: which repository's key to open, which runtime to
// prepare, and the command to start.
type ExecOptions struct {
	Runtime Runtime
	// Repo is the --repo value; empty means infer it from Dir.
	Repo string
	// Dir is the directory used to infer the repository and as the child's working
	// directory. Empty means the current directory.
	Dir string
	// BaseURL is the --base-url value. The environment and config.json are its alternatives.
	BaseURL string
	// Env is the environment this command inherited. Nil means os.Environ().
	Env []string
	// Argv is the command and its arguments, exactly as they followed `--`.
	Argv []string
	// Home is where the key and envelope live.
	Home Home
	// Stdin, Stdout and Stderr are the child's streams. Nil means this process's own.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// RunChild opens the repository's envelope in memory, prepares the runtime, and runs the
// command with that single child's environment.
//
// The order is fixed and every step is refused before the child starts: an unreadable
// key, a missing envelope or an unresolvable gateway address must not leave a process
// running with a partial environment, because that process would fail in its own words
// rather than this command's.
//
// ctx is accepted for the command signature and deliberately not used to start or
// cancel the child. Cancelling a CommandContext sends the child a kill signal the
// terminal did not send, so Ctrl-C would kill the child instead of reaching it; the
// child shares this process's console and receives the interrupt on its own.
func RunChild(ctx context.Context, options ExecOptions) (int, error) {
	_ = ctx
	if len(options.Argv) == 0 {
		return EXIT_USAGE, Errorf(CODE_COMMAND_REQUIRED, "no command; put the command after `--`, for example `oh-my-cpa key exec --runtime claude -- claude`")
	}
	env := options.Env
	if env == nil {
		env = os.Environ()
	}
	directory := options.Dir
	if strings.TrimSpace(directory) == "" {
		directory, _ = os.Getwd()
	}
	repoID, display, err := ResolveRepo(options.Repo, directory)
	if err != nil {
		return EXIT_USAGE, err
	}
	home := options.Home
	if err := checkExecHome(home, repoID); err != nil {
		return ExitCode(err), err
	}

	// The private key is read once, used once and erased as soon as the plaintext
	// exists. Go cannot promise that a copied string is unreachable, so this is a
	// best-effort narrowing of the window, not a guarantee.
	privateKey, err := ReadPrivateKey(home, repoID)
	if err != nil {
		return ExitCode(err), err
	}
	defer zeroBytes(privateKey)

	envelopePath, err := home.EnvelopePath(repoID)
	if err != nil {
		return ExitCode(err), err
	}
	envelopeJSON, err := readEnvelope(envelopePath, repoID)
	if err != nil {
		return ExitCode(err), err
	}

	config, err := home.ReadConfig()
	if err != nil {
		return ExitCode(err), err
	}
	resolved, err := ResolveBaseURL(options.BaseURL, envValue(env, ENV_CPA_BASE_URL), config)
	if err != nil {
		return ExitCode(err), err
	}

	plaintext, err := repokey.Decrypt(privateKey, envelopeJSON)
	if err != nil {
		if errors.Is(err, repokey.ErrDecryptFailed) {
			return EXIT_FAILED, Errorf(CODE_DECRYPT_FAILED, "the envelope for %s does not open with this machine's private key; its key may have been replaced. Re-apply for a key, then `key accept` the new envelope", display)
		}
		return EXIT_FAILED, Errorf(CODE_ENVELOPE_INVALID, "the envelope for %s is not a valid version 1 %s envelope", display, repokey.Algorithm)
	}
	// Erase the private key before the child starts: nothing after this point needs it,
	// and the child inherits only an environment, never this process's memory.
	zeroBytes(privateKey)
	if strings.TrimSpace(plaintext) == "" {
		return EXIT_FAILED, Errorf(CODE_DECRYPT_FAILED, "the envelope for %s opened to an empty key", display)
	}

	injectOptions := InjectOptions{
		Key:                plaintext,
		Root:               resolved.Root,
		Argv:               options.Argv,
		Env:                env,
		OpenCodeConfigPath: OpenCodeConfigPath(env, userHomeDir()),
	}
	if options.Runtime == RuntimeOpenCode {
		document, readErr := readOptionalFile(injectOptions.OpenCodeConfigPath)
		if readErr != nil {
			return ExitCode(readErr), readErr
		}
		injectOptions.OpenCodeDocument = document
	}

	injection, err := options.Runtime.Inject(injectOptions)
	if err != nil {
		return ExitCode(err), err
	}

	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	// Warnings name a variable, never its value.
	for _, warning := range injection.Warnings {
		writeLine(stderr, warning)
	}
	if resolved.Insecure {
		writeLine(stderr, "warning: "+resolved.Root+" is plain http to a host that is not this machine; the key and the traffic are readable on the network")
	}

	command := exec.Command(injection.Argv[0], injection.Argv[1:]...)
	command.Env = injection.Env
	command.Dir = directory
	command.Stdin = options.Stdin
	command.Stdout = options.Stdout
	command.Stderr = stderr
	if command.Stdin == nil {
		command.Stdin = os.Stdin
	}
	if command.Stdout == nil {
		command.Stdout = os.Stdout
	}

	runErr := command.Run()
	if runErr == nil {
		return EXIT_OK, nil
	}
	var exitError *exec.ExitError
	if errors.As(runErr, &exitError) {
		if code := exitError.ExitCode(); code >= 0 {
			return code, nil
		}
		// A child killed by a signal has no exit status. Reporting 1 keeps the failure
		// visible; the shell's own 128+N convention is not reachable portably here.
		writeLine(stderr, "oh-my-cpa: "+injection.Argv[0]+" was terminated by a signal")
		return EXIT_FAILED, nil
	}
	return EXIT_FAILED, Errorf(CODE_EXEC_FAILED, "cannot run %s: %v", injection.Argv[0], runErr)
}

// checkExecHome treats an absent home as "no key for this repository" rather than as a
// broken installation. The home is created by `key gen`, so a machine that never ran it
// has no key to run with, and key_not_found is the answer that names the next step. An
// existing home that is open to other users is still refused: reading a private key from
// a directory someone else could have replaced is the failure this check exists for.
func checkExecHome(home Home, repoID string) error {
	if _, err := os.Stat(home.Root); errors.Is(err, os.ErrNotExist) {
		return Errorf(CODE_KEY_NOT_FOUND, "no private key for %s; run `oh-my-cpa key gen --repo %s`", repoID, repoID)
	}
	return home.CheckReadableHome()
}

// readEnvelope reads and size-checks a delivery envelope.
func readEnvelope(path, repoID string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", Errorf(CODE_ENVELOPE_MISSING, "no delivery envelope for %s; run `oh-my-cpa key accept --repo %s` with the repo_key_ciphertext result", repoID, repoID)
		}
		return "", Wrapf(err, CODE_ENVELOPE_INVALID, "cannot read %s: %v", path, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, MAX_ENVELOPE_BYTES+1))
	if err != nil {
		return "", Wrapf(err, CODE_ENVELOPE_INVALID, "cannot read %s: %v", path, err)
	}
	if len(content) > MAX_ENVELOPE_BYTES {
		return "", Errorf(CODE_ENVELOPE_INVALID, "%s is larger than %d bytes and is not a delivery envelope", path, MAX_ENVELOPE_BYTES)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return "", Errorf(CODE_ENVELOPE_INVALID, "%s is empty; run `oh-my-cpa key accept --repo %s` again", path, repoID)
	}
	return string(content), nil
}

// readOptionalFile reads a configuration file that may legitimately not exist.
func readOptionalFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot read %s: %v", path, err)
	}
	return content, nil
}

// zeroBytes overwrites a byte slice. Go's compiler may copy values it can see, and a
// garbage collector pass can leave a copy in an unswept page, so this narrows the window
// in which a key is reachable rather than closing it. Callers must still avoid printing
// the buffer or handing it to anything that keeps it.
func zeroBytes(buffer []byte) {
	for index := range buffer {
		buffer[index] = 0
	}
}

// userHomeDir is the home directory OpenCode's default configuration path is under. A
// failure yields an empty string, which OpenCodeConfigPath turns into "no file".
func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// writeLine writes one line to w, ignoring a write error: a warning this process cannot
// deliver must not turn a working child into a failure.
func writeLine(w io.Writer, line string) {
	_, _ = io.WriteString(w, line+"\n")
}
