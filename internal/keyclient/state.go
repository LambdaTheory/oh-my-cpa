package keyclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MAX_STATE_BYTES bounds state.json. It holds a few hundred bytes per repository,
// so anything larger is either corruption or an attempt to exhaust memory.
const MAX_STATE_BYTES = 8 << 20

// Store is an open view of state.json. Mutations stay in memory until Save, so a
// command that fails halfway leaves the file as it was.
//
// A store opened without a lock is for reading. A store opened for update holds the
// per-home write lock until Close, which is what makes read-modify-write safe across
// concurrent key commands.
type Store struct {
	home  Home
	state State
	lock  *Lock
	// path is where Save writes. It is the home's state path unless a test or a future
	// command points the store elsewhere.
	path string
}

// OpenStore reads state.json without locking. A missing file is an empty state, not
// an error: a machine that has never generated a key is the normal first run.
func OpenStore(home Home) (*Store, error) {
	store := &Store{home: home, path: home.StatePath()}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// OpenStoreForUpdate takes the per-home write lock and then reads state.json, so the
// read cannot race another writer's rename. Close releases the lock.
func OpenStoreForUpdate(home Home, timeout time.Duration) (*Store, error) {
	if err := home.Ensure(); err != nil {
		return nil, err
	}
	lock, err := AcquireHomeLock(home, timeout)
	if err != nil {
		return nil, err
	}
	store := &Store{home: home, path: home.StatePath(), lock: lock}
	if err := store.load(); err != nil {
		lock.Release()
		return nil, err
	}
	return store, nil
}

// Close releases a lock held by OpenStoreForUpdate. It does not save; a command that
// changed records must call Save first.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.lock.Release()
}

// Home is the store's home directory, for callers that need to reach files beside
// state.json.
func (s *Store) Home() Home { return s.home }

// State returns a copy of the records. The maps are copied so a caller cannot edit
// the store's memory without going through PutRepo or PutRuntime.
func (s *Store) State() State {
	copied := State{
		Version:  s.state.Version,
		Repos:    make(map[string]RepoState, len(s.state.Repos)),
		Runtimes: make(map[Runtime]RuntimeSlot, len(s.state.Runtimes)),
	}
	for repo, record := range s.state.Repos {
		copied.Repos[repo] = record
	}
	for runtime, slot := range s.state.Runtimes {
		copied.Runtimes[runtime] = slot
	}
	return copied
}

// Repos returns the repository records, keyed by identity.
func (s *Store) Repos() map[string]RepoState { return s.State().Repos }

// Repo looks up one repository's record.
func (s *Store) Repo(repoID string) (RepoState, bool) {
	record, ok := s.state.Repos[repoID]
	return record, ok
}

// PutRepo inserts or replaces a repository record. The record's own Repo field is the
// key, so the two cannot disagree.
func (s *Store) PutRepo(record RepoState) error {
	if strings.TrimSpace(record.Repo) == "" {
		return Errorf(CODE_REPO_REQUIRED, "a state record needs a repository identity")
	}
	s.state.Repos[record.Repo] = record
	return nil
}

// RemoveRepo deletes a repository record and reports whether it existed.
func (s *Store) RemoveRepo(repoID string) bool {
	_, existed := s.state.Repos[repoID]
	delete(s.state.Repos, repoID)
	return existed
}

// Runtime looks up one runtime's installed slot.
func (s *Store) Runtime(runtime Runtime) (RuntimeSlot, bool) {
	slot, ok := s.state.Runtimes[runtime]
	return slot, ok
}

// RuntimeForRepo reports the runtime whose slot currently holds repoID.
func (s *Store) RuntimeForRepo(repoID string) (Runtime, bool) {
	for _, runtime := range Runtimes() {
		if slot, ok := s.state.Runtimes[runtime]; ok && slot.Repo == repoID {
			return runtime, true
		}
	}
	return "", false
}

// PutRuntime records that a runtime now holds a repository's value.
func (s *Store) PutRuntime(runtime Runtime, slot RuntimeSlot) error {
	if strings.TrimSpace(slot.Repo) == "" {
		return Errorf(CODE_REPO_REQUIRED, "a runtime slot needs a repository identity")
	}
	s.state.Runtimes[runtime] = slot
	return nil
}

// RemoveRuntime clears a runtime's slot and reports whether it existed.
func (s *Store) RemoveRuntime(runtime Runtime) bool {
	_, existed := s.state.Runtimes[runtime]
	delete(s.state.Runtimes, runtime)
	return existed
}

// Save writes the state atomically: a temporary file in the same directory, flushed
// and narrowed, then renamed over state.json. A reader either sees the old document
// or the new one, never a partial write.
func (s *Store) Save() error {
	s.state.Version = STATE_VERSION
	if s.state.Repos == nil {
		s.state.Repos = map[string]RepoState{}
	}
	if s.state.Runtimes == nil {
		s.state.Runtimes = map[Runtime]RuntimeSlot{}
	}
	encoded, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return Wrapf(err, CODE_STATE_CORRUPT, "cannot encode state: %v", err)
	}
	encoded = append(encoded, '\n')
	if err := writeFileAtomic(s.path, encoded); err != nil {
		return err
	}
	return nil
}

// load reads and validates state.json. A document from a newer version is refused
// rather than reinterpreted, so an older binary cannot truncate fields it does not
// understand.
func (s *Store) load() error {
	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.state = emptyState()
			return nil
		}
		return Wrapf(err, CODE_STATE_UNREADABLE, "cannot read %s: %v", s.path, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, MAX_STATE_BYTES+1))
	if err != nil {
		return Wrapf(err, CODE_STATE_UNREADABLE, "cannot read %s: %v", s.path, err)
	}
	if len(content) > MAX_STATE_BYTES {
		return Errorf(CODE_STATE_CORRUPT, "%s is larger than %d bytes; it is not a state file this tool wrote", s.path, MAX_STATE_BYTES)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		s.state = emptyState()
		return nil
	}
	var decoded State
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Errorf(CODE_STATE_CORRUPT, "%s is not valid state JSON; move it aside to start over: %v", s.path, err)
	}
	if decoded.Version == 0 {
		decoded.Version = STATE_VERSION
	}
	if decoded.Version != STATE_VERSION {
		return Errorf(CODE_STATE_CORRUPT, "%s has schema version %d; this binary reads version %d", s.path, decoded.Version, STATE_VERSION)
	}
	if decoded.Repos == nil {
		decoded.Repos = map[string]RepoState{}
	}
	if decoded.Runtimes == nil {
		decoded.Runtimes = map[Runtime]RuntimeSlot{}
	}
	for repoID, record := range decoded.Repos {
		if strings.TrimSpace(record.Repo) == "" {
			record.Repo = repoID
		}
		decoded.Repos[repoID] = record
	}
	s.state = decoded
	return nil
}

// writeFileAtomic replaces path with content through a temporary file in the same
// directory. The rename is what makes the replacement indivisible; the fsync before
// it is what makes the new content durable once the rename is visible.
func writeFileAtomic(path string, content []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot create a temporary file beside %s: %v", path, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(content); err != nil {
		cleanup()
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot write %s: %v", path, err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot flush %s: %v", path, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot close %s: %v", path, err)
	}
	// Narrow the mode before the rename so the file is never briefly world-readable
	// under its final name.
	if err := restrictFile(temporaryPath); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot replace %s: %v", path, err)
	}
	syncDirectory(directory)
	return nil
}

// ReadConfig reads config.json. A missing file yields an empty config, because the
// gateway address is optional until a command needs one.
func (h Home) ReadConfig() (Config, error) {
	file, err := os.Open(h.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{Version: CONFIG_VERSION}, nil
		}
		return Config{}, Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot read %s: %v", h.ConfigPath(), err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return Config{}, Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot read %s: %v", h.ConfigPath(), err)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return Config{Version: CONFIG_VERSION}, nil
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return Config{}, Errorf(CODE_CONFIG_UNREADABLE, "%s is not valid JSON; fix or remove it: %v", h.ConfigPath(), err)
	}
	if config.Version != 0 && config.Version != CONFIG_VERSION {
		return Config{}, Errorf(CODE_CONFIG_UNREADABLE, "%s has schema version %d; this binary reads version %d", h.ConfigPath(), config.Version, CONFIG_VERSION)
	}
	config.Version = CONFIG_VERSION
	return config, nil
}

// WriteConfig stores the gateway root at config.json, atomically and owner-only. It
// assumes the caller already holds the home lock.
func (h Home) WriteConfig(baseURL string, at time.Time) error {
	config := Config{Version: CONFIG_VERSION, BaseURL: baseURL, UpdatedAt: at.UTC()}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot encode config: %v", err)
	}
	encoded = append(encoded, '\n')
	return writeFileAtomic(h.ConfigPath(), encoded)
}

// NormalizeBaseURL validates a gateway address and reduces it to the root.
//
// Only http and https, with a host and nothing that would smuggle a request
// elsewhere: userinfo, a query and a fragment are refused rather than stripped. A
// trailing slash and one trailing /v1 are removed because callers store the root and
// append their own path; a deeper endpoint such as /v1/messages is refused instead of
// guessed at, since silently truncating it would send requests somewhere the operator
// did not name.
func NormalizeBaseURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", Errorf(CODE_BASE_URL_REQUIRED, "no gateway address; pass --base-url, set OMCPA_CPA_BASE_URL, or save one with `oh-my-cpa key gen --base-url <url>`")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", Errorf(CODE_BASE_URL_INVALID, "%q is not a valid URL", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", Errorf(CODE_BASE_URL_INVALID, "%q must start with http:// or https://", value)
	}
	if parsed.Host == "" {
		return "", Errorf(CODE_BASE_URL_INVALID, "%q has no host", value)
	}
	if parsed.User != nil {
		return "", Errorf(CODE_BASE_URL_INVALID, "%q carries credentials; put the key in the repository key flow instead", value)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", Errorf(CODE_BASE_URL_INVALID, "%q has a query or fragment; name the gateway root only", value)
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if path == "/" {
		path = ""
	}
	// A "v1" that is not the last segment means the caller pasted an endpoint such as
	// /v1/messages. Truncating would quietly redirect requests, so it is refused; a
	// gateway deployed under a sub-path stays accepted.
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, segment := range segments {
		if segment == "v1" && index != len(segments)-1 {
			return "", Errorf(CODE_BASE_URL_INVALID, "%q points at an endpoint, not the gateway root; use the root such as http://host:8317", value)
		}
	}
	if len(segments) > 0 && segments[len(segments)-1] == "v1" {
		segments = segments[:len(segments)-1]
		path = ""
		if len(segments) > 0 {
			path = "/" + strings.Join(segments, "/")
		}
	}
	root := parsed.Scheme + "://" + parsed.Host + path
	return root, nil
}

// IsInsecureBaseURL reports whether root is plain http on a host that is not obviously
// this machine, which the caller warns about: a private Docker network uses exactly that
// shape, so it is allowed, but traffic to a remote host would then be cleartext.
//
// Only the literal loopback addresses and "localhost" count as local. Resolving the name
// would make a warning depend on DNS, and a name that happens to resolve to 127.0.0.1
// today is not a promise it will tomorrow.
func IsInsecureBaseURL(root string) bool {
	parsed, err := url.Parse(root)
	if err != nil || parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

// ResolveBaseURL applies the fixed precedence: the flag, then OMCPA_CPA_BASE_URL,
// then $OMC_HOME/config.json. It never guesses a default address.
func ResolveBaseURL(flagValue, environmentValue string, config Config) (BaseURL, error) {
	if strings.TrimSpace(flagValue) != "" {
		root, err := NormalizeBaseURL(flagValue)
		if err != nil {
			return BaseURL{}, err
		}
		return BaseURL{Root: root, Source: "flag", Insecure: IsInsecureBaseURL(root)}, nil
	}
	if strings.TrimSpace(environmentValue) != "" {
		root, err := NormalizeBaseURL(environmentValue)
		if err != nil {
			return BaseURL{}, err
		}
		return BaseURL{Root: root, Source: "env", Insecure: IsInsecureBaseURL(root)}, nil
	}
	if strings.TrimSpace(config.BaseURL) != "" {
		root, err := NormalizeBaseURL(config.BaseURL)
		if err != nil {
			return BaseURL{}, err
		}
		return BaseURL{Root: root, Source: "config", Insecure: IsInsecureBaseURL(root)}, nil
	}
	return BaseURL{}, Errorf(CODE_BASE_URL_REQUIRED, "no gateway address; pass --base-url <url>, set %s, or save one with `oh-my-cpa key gen --base-url <url>`", ENV_CPA_BASE_URL)
}
