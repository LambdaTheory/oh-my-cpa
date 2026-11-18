package keyclient

import (
	"errors"
	"fmt"
	"strings"
)

// Stable error codes. Each is printed before the human-readable message, and is the
// machine field of `--json` output, so a script can branch on it across versions.
//
// A message may name a path, a repository display, a key mask, a fingerprint, a
// runtime or a child exit code. It must never carry a private key, a plaintext
// gateway key, an envelope body, a management key or an approval token.
const (
	CODE_HOME_UNAVAILABLE = "home_unavailable"
	CODE_HOME_INSECURE    = "home_insecure"
	CODE_REPO_REQUIRED    = "repo_required"
	CODE_REPO_INVALID     = "repo_invalid"
	CODE_KEY_NOT_FOUND    = "key_not_found"
	CODE_KEY_UNREADABLE   = "key_unreadable"
	// CODE_KEY_EXISTS covers only the race where a key file appears between the
	// existence check and the exclusive create. An existing key is otherwise reused by
	// `key gen` and is not an error.
	CODE_KEY_EXISTS            = "key_exists"
	CODE_ENVELOPE_MISSING      = "envelope_missing"
	CODE_ENVELOPE_INVALID      = "envelope_invalid"
	CODE_ENVELOPE_MISMATCH     = "envelope_key_mismatch"
	CODE_BASE_URL_REQUIRED     = "base_url_required"
	CODE_BASE_URL_INVALID      = "base_url_invalid"
	CODE_INSECURE_BASE_URL     = "insecure_base_url"
	CODE_RUNTIME_UNKNOWN       = "runtime_unknown"
	CODE_COMMAND_REQUIRED      = "command_required"
	CODE_CODEX_COMMAND         = "codex_command_required"
	CODE_DECRYPT_FAILED        = "decrypt_failed"
	CODE_STATE_UNREADABLE      = "state_unreadable"
	CODE_STATE_CORRUPT         = "state_corrupt"
	CODE_STATE_LOCKED          = "state_locked"
	CODE_CONFIG_UNREADABLE     = "config_unreadable"
	CODE_CONFIG_NOT_JSON       = "config_not_json"
	CODE_CONFIG_TOO_LARGE      = "config_too_large"
	CODE_CONFIG_CONFLICT       = "config_conflict"
	CODE_OWNED_BY_ANOTHER      = "config_owned_by_another_repo"
	CODE_BACKUP_FAILED         = "backup_failed"
	CODE_INSTALL_FAILED        = "install_failed"
	CODE_NOT_INSTALLED         = "install_not_installed"
	CODE_NOT_OWNED             = "install_not_owned"
	CODE_REMOTE_NOT_CONFIGURED = "remote_not_configured"
	CODE_REMOTE_UNAVAILABLE    = "remote_unavailable"
	CODE_PURGE_PENDING         = "purge_pending"
	CODE_EXEC_FAILED           = "exec_failed"
	CODE_USAGE                 = "usage"

	// CODE_MODELS_REQUIRED covers a missing or unusable OpenCode model list. OpenCode
	// refuses a custom provider that declares no models, so this command does not write
	// one: the gateway's answer or an explicit --models has to name at least one.
	CODE_MODELS_REQUIRED = "models_required"
	// CODE_MODELS_UNAVAILABLE is the gateway not answering GET /v1/models, or answering
	// something that is not a model list.
	CODE_MODELS_UNAVAILABLE = "models_unavailable"
)

// Exit codes, one per kind of failure. `key exec` reports the child's own exit code
// once the child has started, so these only cover the phase before that.
const (
	EXIT_OK        = 0
	EXIT_FAILED    = 1
	EXIT_USAGE     = 2
	EXIT_CONFIG    = 3
	EXIT_NOT_FOUND = 4
	EXIT_CONFLICT  = 5
)

// exitCodes maps every code to its exit status. A code not listed here is a
// programming error; ExitCode falls back to 1 rather than inventing success.
var exitCodes = map[string]int{
	CODE_HOME_UNAVAILABLE:      EXIT_FAILED,
	CODE_HOME_INSECURE:         EXIT_FAILED,
	CODE_REPO_REQUIRED:         EXIT_USAGE,
	CODE_REPO_INVALID:          EXIT_USAGE,
	CODE_KEY_NOT_FOUND:         EXIT_NOT_FOUND,
	CODE_KEY_UNREADABLE:        EXIT_FAILED,
	CODE_KEY_EXISTS:            EXIT_CONFLICT,
	CODE_ENVELOPE_MISSING:      EXIT_NOT_FOUND,
	CODE_ENVELOPE_INVALID:      EXIT_FAILED,
	CODE_ENVELOPE_MISMATCH:     EXIT_FAILED,
	CODE_BASE_URL_REQUIRED:     EXIT_CONFIG,
	CODE_BASE_URL_INVALID:      EXIT_CONFIG,
	CODE_INSECURE_BASE_URL:     EXIT_OK,
	CODE_RUNTIME_UNKNOWN:       EXIT_USAGE,
	CODE_COMMAND_REQUIRED:      EXIT_USAGE,
	CODE_CODEX_COMMAND:         EXIT_USAGE,
	CODE_DECRYPT_FAILED:        EXIT_FAILED,
	CODE_STATE_UNREADABLE:      EXIT_FAILED,
	CODE_STATE_CORRUPT:         EXIT_CONFLICT,
	CODE_STATE_LOCKED:          EXIT_FAILED,
	CODE_CONFIG_UNREADABLE:     EXIT_FAILED,
	CODE_CONFIG_NOT_JSON:       EXIT_FAILED,
	CODE_CONFIG_TOO_LARGE:      EXIT_CONFIG,
	CODE_CONFIG_CONFLICT:       EXIT_CONFLICT,
	CODE_MODELS_REQUIRED:       EXIT_CONFIG,
	CODE_MODELS_UNAVAILABLE:    EXIT_FAILED,
	CODE_OWNED_BY_ANOTHER:      EXIT_CONFLICT,
	CODE_BACKUP_FAILED:         EXIT_FAILED,
	CODE_INSTALL_FAILED:        EXIT_FAILED,
	CODE_NOT_INSTALLED:         EXIT_NOT_FOUND,
	CODE_NOT_OWNED:             EXIT_FAILED,
	CODE_REMOTE_NOT_CONFIGURED: EXIT_CONFIG,
	CODE_REMOTE_UNAVAILABLE:    EXIT_FAILED,
	CODE_PURGE_PENDING:         EXIT_FAILED,
	CODE_EXEC_FAILED:           EXIT_FAILED,
	CODE_USAGE:                 EXIT_USAGE,
}

// Error is a key command failure carrying a stable code. Message is the human
// sentence printed after the code.
type Error struct {
	Code    string
	Message string
	// Wrapped is kept for errors.Is/As; it is never printed on its own, because a
	// low-level error can quote a path or a payload the message deliberately leaves
	// out.
	Wrapped error
}

func (e *Error) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Wrapped }

// Errorf builds a coded error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrapf builds a coded error that remembers its cause for errors.Is while printing
// only the coded message.
func Wrapf(cause error, code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Wrapped: cause}
}

// CodeOf returns the stable code of err, or "" when err is not a coded error.
func CodeOf(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// MessageOf returns the human sentence of err without its code prefix. For a
// non-coded error it returns err.Error(), which is only safe for errors this package
// did not classify deliberately narrow messages around.
func MessageOf(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Message
	}
	return err.Error()
}

// ExitCode maps err to the process exit status a command layer should use.
func ExitCode(err error) int {
	if err == nil {
		return EXIT_OK
	}
	code := CodeOf(err)
	if status, ok := exitCodes[code]; ok {
		return status
	}
	return EXIT_FAILED
}

// WithCode attaches a code to an already-coded error when it has none, keeping the
// original when it does.
func WithCode(err error, code string) error {
	if err == nil {
		return nil
	}
	if CodeOf(err) != "" {
		return err
	}
	return &Error{Code: code, Message: err.Error(), Wrapped: err}
}
