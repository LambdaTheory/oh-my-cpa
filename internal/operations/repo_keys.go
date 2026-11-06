package operations

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/oh-my-cpa/oh-my-cpa/internal/auditpush"
	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// Repository-owned caller API keys (PRD plans/prd/omc-repo-api-keys).
//
// The wrapper answers the question the console and the agents both ask: which repository owns
// which key, who has been given it, and is it usable right now. It composes three things that
// are each tested on their own: CPA's key list (the only place a key's value exists), the
// repository's metadata (`internal/repository`), and the delivery envelope
// (`internal/repokey`).
//
// Two rules shape everything below. A key value is never stored, logged or returned — only its
// gateway fingerprint, its display mask and an envelope the caller decrypts. And every write
// goes to the audit trail in the same transaction as the change it describes.

const (
	repoKeyInstanceID = "default"
	// RepoKeyConsoleDevice is the device recorded for an application an operator filed. The
	// console renders it as 「控制台代提」; storing a stable token instead of the prose keeps
	// the record comparable across languages and versions (PRD 4.1).
	RepoKeyConsoleDevice = "console"
	// repoKeyApprovalLifetime is how long a one-time approval link works. The application
	// itself never expires (AC-8).
	repoKeyApprovalLifetime = 24 * time.Hour
)

// Audit actions. One stable name per event type the PRD lists in 4.8; the eight are spread
// across apply/approve/reject/deliver/reveal/stop/reissue/reenable.
const (
	AuditRepoKeyApply    = "repo_key.apply"
	AuditRepoKeyApprove  = "repo_key.approve"
	AuditRepoKeyReject   = "repo_key.reject"
	AuditRepoKeyDeliver  = "repo_key.deliver"
	AuditRepoKeyReveal   = "repo_key.reveal"
	AuditRepoKeyStop     = "repo_key.stop"
	AuditRepoKeyReissue  = "repo_key.reissue"
	AuditRepoKeyReenable = "repo_key.reenable"
	// AuditRepoKeyApprovalLink records the console re-issuing a pending application's
	// one-time link. The old link stops working in the same write (AC-8).
	AuditRepoKeyApprovalLink = "repo_key.approval_link"
)

// repoKeyPush builds the team audit platform's copy of one recorded action (PRD 4.8).
//
// The copy is queued in the same transaction as the console's audit row, so the two
// cannot disagree. A build error is a programming error - the action and trace are
// compile-time constants and the status is always OK here - and it must not undo the
// business step, so it returns nil and the console record remains the source of truth
// (AC-32: push failure never changes a business result).
func repoKeyPush(action, traceID, repoID, summary, channel string, tags []string) *repository.AuditPushEvent {
	built, payload, err := auditpush.Build(auditpush.Description{
		Action: action, TraceID: traceID, RepoID: repoID, Summary: summary, Channel: channel, Tags: tags,
	})
	if err != nil {
		return nil
	}
	return &repository.AuditPushEvent{
		Payload: payload, TraceID: built.TraceID, SpanID: built.SpanID,
		ToolName: built.ToolName, Event: built.Event,
	}
}

// repoKeyChannelFor names where a delivery came from: an operator present means the
// console hand-out, otherwise the caller fetched its own envelope over MCP.
func repoKeyChannelFor(operator string) string {
	if strings.TrimSpace(operator) == "" {
		return auditpush.ChannelMCP
	}
	return auditpush.ChannelConsole
}

// repoKeyChannelFromSource maps an application's recorded source onto the platform's
// channel field. The application stores "agent" for the MCP path and "console" for the
// console's proxy application; the platform reads the latter as the console.
func repoKeyChannelFromSource(source string) string {
	if strings.TrimSpace(source) == "console" {
		return auditpush.ChannelConsole
	}
	return auditpush.ChannelMCP
}

// repoKeyTags puts the caller's agent and device on the platform event so the audit
// platform can filter by them without parsing the summary (PRD 4.8).
func repoKeyTags(agent, device string) []string {
	tags := make([]string, 0, 2)
	if agent = strings.TrimSpace(agent); agent != "" {
		tags = append(tags, "agent:"+agent)
	}
	if device = strings.TrimSpace(device); device != "" {
		tags = append(tags, "device:"+device)
	}
	return tags
}

// RepoKeyQueryInput asks whether a repository has a usable key (AC-3).
type RepoKeyQueryInput struct {
	Repo string `json:"repo" jsonschema:"the repository, as a remote identity such as org/repo or a local directory name"`
}

// RepoKeyQueryResult is the three-way answer plus what the caller's next application would
// become. It names no key value: only the mask (PRD 4.1, AC-5).
type RepoKeyQueryResult struct {
	Repo                string `json:"repo"`
	RepoHasRemote       bool   `json:"repo_has_remote"`
	State               string `json:"state"`
	KeyMask             string `json:"key_mask,omitempty"`
	KeyCreatedAtMS      int64  `json:"key_created_at_ms,omitempty"`
	StoppedAtMS         int64  `json:"stopped_at_ms,omitempty"`
	StopCategory        string `json:"stop_category,omitempty"`
	CallerAgent         string `json:"caller_agent,omitempty"`
	CallerDevice        string `json:"caller_device,omitempty"`
	PublicKeyRegistered bool   `json:"public_key_registered"`
	CallerRegistered    bool   `json:"caller_registered"`
	NextKind            string `json:"next_kind"`
	AutoApprove         bool   `json:"auto_approve"`
}

// RepoKeyApplyInput is an agent's application. The agent and device come from the transport,
// not from the arguments: they are the caller's identity, and a caller that cannot state one
// cannot be given a key (AC-2).
type RepoKeyApplyInput struct {
	Repo      string `json:"repo" jsonschema:"the repository this key is for, as a remote identity such as org/repo or a local directory name"`
	Purpose   string `json:"purpose" jsonschema:"one sentence on what the key will be used for; the operator reads it to decide"`
	PublicKey string `json:"public_key,omitempty" jsonschema:"the caller's X25519 public key, x25519:<base64>; required on the first application, later applications reuse the registered one"`
}

// RepoKeyApplyResult is what the applicant keeps: the application id for status and ciphertext
// calls, and the one-time approval link to put in front of a person.
type RepoKeyApplyResult struct {
	ApplicationID    string `json:"application_id"`
	Status           string `json:"status"`
	Kind             string `json:"kind"`
	Repo             string `json:"repo"`
	RepoDisplay      string `json:"repo_display"`
	RepoHasRemote    bool   `json:"repo_has_remote"`
	Agent            string `json:"agent"`
	Device           string `json:"device"`
	KeyMask          string `json:"key_mask,omitempty"`
	ApprovalToken    string `json:"approval_token,omitempty"`
	ApprovalURL      string `json:"approval_url,omitempty"`
	ApprovalExpires  int64  `json:"approval_expires_at_ms,omitempty"`
	NextActionDetail string `json:"next_action,omitempty"`
}

// RepoKeyStatusResult answers "what happened to my application" without ever naming the key.
type RepoKeyStatusResult struct {
	ApplicationID    string `json:"application_id"`
	Status           string `json:"status"`
	Kind             string `json:"kind"`
	Repo             string `json:"repo"`
	RepoDisplay      string `json:"repo_display"`
	Agent            string `json:"agent"`
	Device           string `json:"device"`
	Purpose          string `json:"purpose"`
	KeyMask          string `json:"key_mask,omitempty"`
	RejectReason     string `json:"reject_reason,omitempty"`
	DecidedAtMS      int64  `json:"decided_at_ms,omitempty"`
	FirstDeliveredMS int64  `json:"first_delivered_at_ms,omitempty"`
	CiphertextReady  bool   `json:"ciphertext_ready"`
	CreatedAtMS      int64  `json:"created_at_ms"`
}

// RepoKeyCiphertextResult is the delivery: an envelope the caller opens with its private key.
type RepoKeyCiphertextResult struct {
	ApplicationID string `json:"application_id"`
	Kind          string `json:"kind"`
	KeyMask       string `json:"key_mask"`
	Algorithm     string `json:"algorithm"`
	Ciphertext    string `json:"ciphertext"`
	Instructions  string `json:"instructions"`
}

type repoKeyApplyParams struct {
	Repo                 string
	Purpose              string
	PublicKey            string
	PublicKeyFingerprint string
	Agent                string
	Device               string
	Source               string
	Operator             string
	AutoApproveAllowed   bool
}

// QueryRepoKey reports whether the repository has a usable key, only stopped keys, or none
// (AC-3), and what the caller's next application would be.
func (s *Service) QueryRepoKey(ctx context.Context, input RepoKeyQueryInput) (RepoKeyQueryResult, error) {
	repoID, hasRemote, err := repokey.NormalizeRepo(input.Repo)
	if err != nil {
		return RepoKeyQueryResult{}, invalidParameters{detail: "The repo must be a remote identity such as org/repo, or a local directory name."}
	}
	status, err := s.Repo.GetRepoKeyRepoStatus(ctx, repoKeyInstanceID, repoID)
	if err != nil {
		return RepoKeyQueryResult{}, err
	}
	result := RepoKeyQueryResult{Repo: repoID, RepoHasRemote: hasRemote, State: status.State}
	switch status.State {
	case "active":
		result.KeyMask = status.Binding.KeyMask
		result.KeyCreatedAtMS = status.Binding.CreatedAtMS
		result.NextKind = "reuse"
	case "stopped":
		result.StoppedAtMS = derefInt64(status.Binding.StoppedAtMS)
		result.StopCategory = status.Binding.StopCategory
		result.NextKind = "reissue"
	default:
		result.NextKind = "issue"
	}
	if caller, ok := capability.CallerFrom(ctx); ok {
		result.CallerAgent = strings.TrimSpace(caller.Agent)
		result.CallerDevice = strings.TrimSpace(caller.Device)
		if result.CallerAgent != "" && result.CallerDevice != "" {
			callerRow, callerErr := s.Repo.GetRepoKeyCaller(ctx, repoKeyInstanceID, result.CallerAgent, result.CallerDevice)
			result.PublicKeyRegistered = callerErr == nil
			if callerErr == nil {
				result.CallerRegistered, err = s.wasRepoKeyCallerApproved(ctx, repoID, result.CallerAgent, result.CallerDevice, callerRow.PublicKeyFingerprint)
				if err != nil {
					return RepoKeyQueryResult{}, err
				}
			}
			if callerErr != nil && !errors.Is(callerErr, repository.ErrRepoKeyNotFound) {
				return RepoKeyQueryResult{}, callerErr
			}
		}
	}
	result.AutoApprove = result.State == "active" && result.CallerRegistered
	return result, nil
}

// ApplyRepoKey files an agent's application from the MCP entry point (AC-1).
func (s *Service) ApplyRepoKey(ctx context.Context, input RepoKeyApplyInput) (RepoKeyApplyResult, error) {
	caller, ok := capability.CallerFrom(ctx)
	if !ok {
		return RepoKeyApplyResult{}, errors.New("caller_identity_required")
	}
	return s.applyRepoKey(ctx, repoKeyApplyParams{
		Repo: input.Repo, Purpose: input.Purpose, PublicKey: input.PublicKey,
		Agent: caller.Agent, Device: caller.Device, Source: "agent", AutoApproveAllowed: true,
	})
}

// ProxyRepoKeyApplication files the same application from the console (AC-29). The device is
// the console marker, and an operator-filed application never skips approval: the person who
// would approve it is already here.
func (s *Service) ProxyRepoKeyApplication(ctx context.Context, repo, purpose, publicKey, publicKeyFingerprint, agent, operator string) (RepoKeyApplyResult, error) {
	return s.applyRepoKey(ctx, repoKeyApplyParams{
		Repo: repo, Purpose: purpose, PublicKey: publicKey, PublicKeyFingerprint: publicKeyFingerprint,
		Agent: agent, Device: RepoKeyConsoleDevice, Source: "console", Operator: operator,
	})
}

func (s *Service) applyRepoKey(ctx context.Context, params repoKeyApplyParams) (RepoKeyApplyResult, error) {
	repoID, hasRemote, err := repokey.NormalizeRepo(params.Repo)
	if err != nil {
		return RepoKeyApplyResult{}, invalidParameters{detail: "The repo must be a remote identity such as org/repo, or a local directory name."}
	}
	repoDisplay, err := repokey.RepoDisplay(params.Repo)
	if err != nil {
		return RepoKeyApplyResult{}, err
	}
	agent := strings.TrimSpace(params.Agent)
	purpose := strings.TrimSpace(params.Purpose)
	if agent == "" || len(agent) > 128 {
		return RepoKeyApplyResult{}, invalidParameters{detail: "The agent name is required and must be at most 128 characters."}
	}
	device := strings.TrimSpace(params.Device)
	if device == "" || len(device) > 128 {
		return RepoKeyApplyResult{}, invalidParameters{detail: "The device name could not be determined; reconnect with a device header."}
	}
	if purpose == "" || len(purpose) > 500 {
		return RepoKeyApplyResult{}, invalidParameters{detail: "The purpose is required and must be at most 500 characters."}
	}

	release, err := s.lock(ctx)
	if err != nil {
		return RepoKeyApplyResult{}, err
	}
	defer release()
	pending, pendingErr := s.Repo.PendingRepoKeyApplication(ctx, repoKeyInstanceID, repoID, agent)
	if pendingErr != nil && !errors.Is(pendingErr, repository.ErrRepoKeyApplicationNotFound) {
		return RepoKeyApplyResult{}, pendingErr
	}
	if pendingErr == nil && pending.DeviceName == device && strings.TrimSpace(params.PublicKey) == "" && strings.TrimSpace(params.PublicKeyFingerprint) == "" {
		params.PublicKey = pending.PublicKey
	}
	publicKey, fingerprint, err := s.resolveRepoKeyPublicKey(ctx, agent, device, params.PublicKey, params.PublicKeyFingerprint)
	if err != nil {
		return RepoKeyApplyResult{}, err
	}
	previous, previousErr := s.Repo.GetRepoKeyCaller(ctx, repoKeyInstanceID, agent, device)
	if previousErr != nil && !errors.Is(previousErr, repository.ErrRepoKeyNotFound) {
		return RepoKeyApplyResult{}, previousErr
	}
	// A changed public key is exactly the case the operator must look at again, even for a
	// caller this repository already accepted (AC-34).
	publicKeyChanged := previousErr != nil || previous.PublicKeyFingerprint != fingerprint
	nowMS := time.Now().UnixMilli()

	status, err := s.Repo.GetRepoKeyRepoStatus(ctx, repoKeyInstanceID, repoID)
	if err != nil {
		return RepoKeyApplyResult{}, err
	}
	kind := "issue"
	switch status.State {
	case "active":
		kind = "reuse"
	case "stopped":
		kind = "reissue"
	}

	token, err := repokey.RandomToken()
	if err != nil {
		return RepoKeyApplyResult{}, err
	}
	application := repository.RepoKeyApplication{
		// The id is chosen here rather than inside the repository so the platform event
		// can use it as the trace that ties apply, approval and delivery together.
		ID:         uuid.NewString(),
		InstanceID: repoKeyInstanceID, RepoID: repoID, RepoHasRemote: hasRemote, RepoDisplay: repoDisplay,
		AgentName: agent, DeviceName: device, Purpose: purpose, Kind: kind,
		PublicKey: publicKey, PublicKeyFingerprint: fingerprint, KeyAlgorithm: "x25519",
		Source: params.Source, AuthorizationTokenHash: repokey.TokenFingerprint(token),
		TokenExpiresAtMS: nowMS + repoKeyApprovalLifetime.Milliseconds(),
	}

	// 免审: a caller this repository already accepted, asking for the key it already uses, with
	// the public key it registered. Everything else waits for a person (PRD 4.2 q01).
	if params.AutoApproveAllowed && kind == "reuse" && !publicKeyChanged && errors.Is(pendingErr, repository.ErrRepoKeyApplicationNotFound) {
		if recipient, recipientErr := s.wasRepoKeyCallerApproved(ctx, repoID, agent, device, fingerprint); recipientErr == nil && recipient {
			approved, approveErr := s.autoApproveRepoKeyApplication(ctx, status.Binding, application, publicKey, nowMS)
			if approveErr == nil {
				return s.repoKeyApplyResult(ctx, approved, token), nil
			}
			// The gateway could not hand the key out right now; the application falls through
			// to the queue rather than failing the caller.
		}
	}

	stored, _, err := s.Repo.CreateRepoKeyApplication(ctx, application, nowMS, repository.AuditEvent{
		Action: AuditRepoKeyApply, SourceSummary: params.Source,
		Details: map[string]any{
			"repo": repoID, "agent": agent, "device": device, "kind": kind,
			"public_key_fingerprint": fingerprint, "public_key_changed": publicKeyChanged,
			"operator": params.Operator,
		},
		Push: repoKeyPush(AuditRepoKeyApply, application.ID, repoID,
			fmt.Sprintf("applied (%s) for %s by %s@%s; waiting for approval", kind, repoID, agent, device),
			repoKeyChannelFromSource(params.Source), repoKeyTags(agent, device)),
	})
	if err != nil {
		return RepoKeyApplyResult{}, err
	}
	// A waiting application keeps the token this attempt handed out: the repository rotated
	// the stored fingerprint, so the link opens the row that is actually queued (AC-28, AC-8).
	return s.repoKeyApplyResult(ctx, stored, token), nil
}

func (s *Service) wasRepoKeyCallerApproved(ctx context.Context, repoID, agent, device, fingerprint string) (bool, error) {
	recipient, err := s.Repo.IsRepoKeyRecipient(ctx, repoKeyInstanceID, repoID, agent, device)
	if err != nil || !recipient {
		return false, err
	}
	approved, err := s.Repo.LatestApprovedRepoKeyApplicationForCaller(ctx, repoKeyInstanceID, repoID, agent, device, "")
	if errors.Is(err, repository.ErrRepoKeyApplicationNotFound) {
		return false, nil
	}
	return err == nil && approved.PublicKeyFingerprint == fingerprint, err
}

func (s *Service) autoApproveRepoKeyApplication(ctx context.Context, binding *repository.RepoKeyBinding, application repository.RepoKeyApplication, publicKey string, nowMS int64) (repository.RepoKeyApplication, error) {
	if binding == nil {
		return repository.RepoKeyApplication{}, errors.New("repo_key_missing")
	}
	secret, err := s.repoKeySecret(ctx, binding.KeyFingerprint)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	raw, err := repokey.ParsePublicKey(publicKey)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	envelope, err := repokey.Encrypt(raw, secret)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	application.Kind = "reuse"
	application.BindingID = binding.ID
	application.KeyFingerprint = binding.KeyFingerprint
	application.KeyMask = binding.KeyMask
	application.Ciphertext = envelope
	return s.Repo.CreateApprovedRepoKeyApplication(ctx, application, nowMS, repository.AuditEvent{
		Action: AuditRepoKeyApply, SourceSummary: application.Source,
		Details: map[string]any{
			"repo": application.RepoID, "agent": application.AgentName, "device": application.DeviceName,
			"kind": "reuse", "auto_approved": true, "key_mask": binding.KeyMask,
			"public_key_fingerprint": application.PublicKeyFingerprint,
		},
		Push: repoKeyPush(AuditRepoKeyApply, application.ID, application.RepoID,
			fmt.Sprintf("applied (reuse) for %s by %s@%s; auto-approved, key %s",
				application.RepoID, application.AgentName, application.DeviceName, binding.KeyMask),
			repoKeyChannelFromSource(application.Source), repoKeyTags(application.AgentName, application.DeviceName)),
	})
}

// repoKeyApplyResult is the applicant's answer. The one-time approval link is included only
// while the application is actually waiting: an auto-approved reuse has nothing to approve,
// and handing back a token that opens nothing was the review's F4. The link is built from the
// console address this call came through, so it works from the caller's own deployment.
func (s *Service) repoKeyApplyResult(ctx context.Context, application repository.RepoKeyApplication, token string) RepoKeyApplyResult {
	result := RepoKeyApplyResult{
		ApplicationID: application.ID, Status: application.Status, Kind: application.Kind,
		Repo: application.RepoID, RepoHasRemote: application.RepoHasRemote, RepoDisplay: application.RepoDisplay,
		Agent: application.AgentName, Device: application.DeviceName,
		KeyMask: application.KeyMask,
	}
	switch application.Status {
	case "approved":
		result.NextActionDetail = "This repository already had a usable key and this caller was accepted for it before, so no human decision was needed. Fetch the ciphertext next and open it locally with your private key."
	default:
		result.ApprovalToken = token
		result.ApprovalExpires = application.TokenExpiresAtMS
		if base := capability.ConsoleURLFrom(ctx); base != "" {
			result.ApprovalURL = base + "/repo-key-apply/" + token
		}
		result.NextActionDetail = "Give the approval link to a person who can sign in to the console. The application stays queued until they decide."
	}
	return result
}

// resolveRepoKeyPublicKey prefers the key sent with this application, then a fingerprint the
// console operator pasted, then the key already registered for this agent and device.
func (s *Service) resolveRepoKeyPublicKey(ctx context.Context, agent, device, publicKey, fingerprint string) (string, string, error) {
	if strings.TrimSpace(publicKey) != "" {
		raw, err := repokey.ParsePublicKey(publicKey)
		if err != nil {
			return "", "", invalidParameters{detail: "The public key must be x25519: followed by the 32-byte key in base64."}
		}
		return "x25519:" + base64.StdEncoding.EncodeToString(raw), repokey.PublicKeyFingerprint(raw), nil
	}
	if strings.TrimSpace(fingerprint) != "" {
		caller, err := s.Repo.FindRepoKeyCallerByFingerprint(ctx, repoKeyInstanceID, fingerprint, agent)
		if err != nil {
			return "", "", invalidParameters{detail: "No registered public key matches that fingerprint for this agent."}
		}
		return caller.PublicKey, caller.PublicKeyFingerprint, nil
	}
	caller, err := s.Repo.GetRepoKeyCaller(ctx, repoKeyInstanceID, agent, device)
	if err == nil {
		return caller.PublicKey, caller.PublicKeyFingerprint, nil
	}
	if !errors.Is(err, repository.ErrRepoKeyNotFound) {
		return "", "", err
	}
	// A previously supplied key can seed another manual application without becoming
	// an approved registration. In particular, rejection must never grant auto-approval.
	previous, err := s.Repo.LatestRepoKeyApplicationForCaller(ctx, repoKeyInstanceID, agent, device)
	if err == nil && previous.PublicKey != "" {
		return previous.PublicKey, previous.PublicKeyFingerprint, nil
	}
	return "", "", invalidParameters{detail: "A public key is required on the first application; generate a key pair locally and send the public half."}
}

// RepoKeyApplicationByToken resolves a one-time approval link. Only the fingerprint of the
// token is stored, so the database never holds a credential the console accepts (AC-8). An
// expired link is refused here rather than silently deciding a stale request.
func (s *Service) RepoKeyApplicationByToken(ctx context.Context, token string) (repository.RepoKeyApplication, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return repository.RepoKeyApplication{}, repository.ErrRepoKeyApplicationNotFound
	}
	application, err := s.Repo.GetRepoKeyApplicationByToken(ctx, token)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if application.Status == "pending" && application.TokenExpiresAtMS > 0 &&
		application.TokenExpiresAtMS < time.Now().UnixMilli() {
		return repository.RepoKeyApplication{}, errors.New("approval_link_expired")
	}
	return application, nil
}

// IssueRepoKeyApprovalLink hands an operator a fresh one-time link for an application that
// is still waiting. The database keeps only token fingerprints, so the link an applicant was
// given cannot be shown again; a new one is issued and the previous one stops opening
// anything in the same write (AC-8). The link itself is never stored, logged or audited.
func (s *Service) IssueRepoKeyApprovalLink(ctx context.Context, applicationID, operator string) (repository.RepoKeyApplication, string, error) {
	applicationID = strings.TrimSpace(applicationID)
	application, err := s.Repo.GetRepoKeyApplication(ctx, applicationID)
	if err != nil {
		return repository.RepoKeyApplication{}, "", err
	}
	if application.Status != "pending" {
		return repository.RepoKeyApplication{}, "", repository.ErrRepoKeyConflict
	}
	token, err := repokey.RandomToken()
	if err != nil {
		return repository.RepoKeyApplication{}, "", err
	}
	nowMS := time.Now().UnixMilli()
	rotated, err := s.Repo.RotateRepoKeyApprovalToken(ctx, application.ID,
		repokey.TokenFingerprint(token), nowMS+repoKeyApprovalLifetime.Milliseconds(), nowMS,
		repository.AuditEvent{
			Action: AuditRepoKeyApprovalLink, SourceSummary: "console",
			Details: map[string]any{"operator": operator},
			Push: repoKeyPush(AuditRepoKeyApprovalLink, application.ID, application.RepoID,
				fmt.Sprintf("issued a fresh approval link for %s by %s@%s; the previous link is void",
					application.RepoID, application.AgentName, application.DeviceName),
				auditpush.ChannelConsole, repoKeyTags(application.AgentName, application.DeviceName)),
		})
	if err != nil {
		return repository.RepoKeyApplication{}, "", err
	}
	return rotated, token, nil
}

// RepoKeyStatus reports an application's state (AC-24).
func (s *Service) RepoKeyStatus(ctx context.Context, applicationID string) (RepoKeyStatusResult, error) {
	application, err := s.Repo.GetRepoKeyApplication(ctx, applicationID)
	if err != nil {
		return RepoKeyStatusResult{}, err
	}
	result := RepoKeyStatusResult{
		ApplicationID: application.ID, Status: application.Status, Kind: application.Kind,
		Repo: application.RepoID, Agent: application.AgentName, Device: application.DeviceName, RepoDisplay: application.RepoDisplay,
		Purpose: application.Purpose, KeyMask: application.KeyMask,
		RejectReason: application.RejectReason, CreatedAtMS: application.CreatedAtMS,
		DecidedAtMS: derefInt64(application.DecidedAtMS), FirstDeliveredMS: derefInt64(application.FirstDeliveredAtMS),
	}
	result.CiphertextReady = application.Status == "approved" && application.Ciphertext != ""
	return result, nil
}

// FetchRepoKeyCiphertext hands the envelope back. The same envelope may be fetched again: it
// opens only with the caller's private key, so it is not a one-time secret; each hand-out is
// still recorded (AC-33).
func (s *Service) FetchRepoKeyCiphertext(ctx context.Context, applicationID, operator string) (RepoKeyCiphertextResult, error) {
	application, err := s.Repo.GetRepoKeyApplication(ctx, applicationID)
	if err != nil {
		return RepoKeyCiphertextResult{}, err
	}
	if application.Status != "approved" || strings.TrimSpace(application.Ciphertext) == "" {
		return RepoKeyCiphertextResult{}, errors.New("application_not_approved")
	}
	nowMS := time.Now().UnixMilli()
	deliveryKind := "issue"
	if application.Kind == "reuse" {
		deliveryKind = "reuse"
	}
	deliveredBy := "approval"
	if strings.TrimSpace(application.DecidedBy) == "" {
		deliveredBy = "auto"
	}
	if operator != "" {
		deliveredBy = "console_reveal"
	}
	if _, err = s.Repo.RecordRepoKeyDelivery(ctx, repository.RepoKeyDelivery{
		InstanceID: repoKeyInstanceID, ApplicationID: application.ID, BindingID: application.BindingID,
		KeyFingerprint: application.KeyFingerprint, KeyMask: application.KeyMask,
		AgentName: application.AgentName, DeviceName: application.DeviceName,
		PublicKeyFingerprint: application.PublicKeyFingerprint,
		Kind:                 deliveryKind, Operator: deliveredBy, DeliveredAtMS: nowMS,
	}, repository.AuditEvent{
		Action: AuditRepoKeyDeliver, SourceSummary: deliveredBy,
		Details: map[string]any{
			"repo": application.RepoID, "agent": application.AgentName, "device": application.DeviceName,
			"kind": deliveryKind, "key_mask": application.KeyMask,
			"public_key_fingerprint": application.PublicKeyFingerprint, "operator": operator,
		},
		Push: repoKeyPush(AuditRepoKeyDeliver, application.ID, application.RepoID,
			fmt.Sprintf("delivered the %s ciphertext of key %s to %s@%s",
				deliveryKind, application.KeyMask, application.AgentName, application.DeviceName),
			repoKeyChannelFor(operator), repoKeyTags(application.AgentName, application.DeviceName)),
	}); err != nil {
		return RepoKeyCiphertextResult{}, err
	}
	return RepoKeyCiphertextResult{
		ApplicationID: application.ID, Kind: deliveryKind, KeyMask: application.KeyMask,
		Algorithm: repokey.Algorithm, Ciphertext: application.Ciphertext,
		Instructions: "Open this envelope with the private key that matches the public key you registered, then use the key locally. The plaintext never passes through this interface.",
	}, nil
}

// repoKeySecret finds the plaintext of one key in the gateway. It is called only inside a
// write that is about to hand that key to a caller, and the value never leaves that call.
func (s *Service) repoKeySecret(ctx context.Context, fingerprint string) (string, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return "", err
	}
	keys, err := client.ClientAPIKeys(ctx)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		actual, err := s.Repo.UsageClientKeyFingerprint(strings.TrimSpace(key))
		if err != nil {
			return "", err
		}
		if actual == fingerprint {
			return strings.TrimSpace(key), nil
		}
	}
	return "", errors.New("repo_key_missing_in_gateway")
}

func (s *Service) repoKeyIndex(keys []string, fingerprint string) int {
	for index, key := range keys {
		actual, err := s.Repo.UsageClientKeyFingerprint(strings.TrimSpace(key))
		if err == nil && actual == fingerprint {
			return index
		}
	}
	return -1
}

// ApproveRepoKeyApplication is the one decision, whether it is taken in the console queue or
// from the one-time link. It issues or reuses the key, seals it to the applicant's public key,
// and records the recipient so this caller needs no further approval for this repository.
//
// A re-enable request is decided by this same call (AC-31): it puts the stopped key back,
// never issues a new one, and records the approver and their reason.
func (s *Service) ApproveRepoKeyApplication(ctx context.Context, applicationID, operator, reason string) (repository.RepoKeyApplication, error) {
	release, err := s.lock(ctx)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	defer release()
	application, err := s.Repo.GetRepoKeyApplication(ctx, applicationID)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if application.Status != "pending" {
		// Already decided, by the other entry point or an earlier click (AC-8).
		return application, nil
	}
	if application.RequestKind == "reenable" {
		return s.approveRepoKeyReenable(ctx, application, operator, reason)
	}
	client, err := s.Client(ctx)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	keys, err := client.ClientAPIKeys(ctx)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	nowMS := time.Now().UnixMilli()
	binding, secret, kind, err := s.repoKeyForApproval(ctx, client, keys, application.RepoID, application.RepoHasRemote, operator, nowMS)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	var newBinding *repository.RepoKeyBinding
	if kind != "reuse" {
		newBinding = &binding
	}
	raw, err := repokey.ParsePublicKey(application.PublicKey)
	if err != nil {
		if newBinding != nil {
			err = restoreRepoKeyGateway(ctx, client, keys, err)
		}
		return repository.RepoKeyApplication{}, err
	}
	envelope, err := repokey.Encrypt(raw, secret)
	if err != nil {
		if newBinding != nil {
			err = restoreRepoKeyGateway(ctx, client, keys, err)
		}
		return repository.RepoKeyApplication{}, err
	}
	decided, err := s.Repo.DecideRepoKeyApplication(ctx, repository.ApplicationDecision{
		ApplicationID: application.ID, Approve: true, DecidedBy: operator,
		DecisionReason: strings.TrimSpace(reason),
		Kind:           kind, BindingID: binding.ID, KeyFingerprint: binding.KeyFingerprint,
		KeyMask: binding.KeyMask, Ciphertext: envelope,
		NewBinding: newBinding,
		BindingAudit: repository.AuditEvent{
			Action: AuditRepoKeyReissue, SourceSummary: "console",
			Details: map[string]any{"repo": binding.RepoID, "key_mask": binding.KeyMask, "new_key": true},
			Push: repoKeyPush(AuditRepoKeyReissue, binding.ID, binding.RepoID,
				fmt.Sprintf("issued a new key %s for %s while approving an application", binding.KeyMask, binding.RepoID),
				auditpush.ChannelConsole, nil),
		},
	}, nowMS, repository.AuditEvent{
		Action: AuditRepoKeyApprove, SourceSummary: "console",
		Details: map[string]any{
			"repo": application.RepoID, "agent": application.AgentName, "device": application.DeviceName,
			"kind": kind, "key_mask": binding.KeyMask, "binding_id": binding.ID,
			"public_key_fingerprint": application.PublicKeyFingerprint,
		},
		Push: repoKeyPush(AuditRepoKeyApprove, application.ID, application.RepoID,
			fmt.Sprintf("approved (%s) for %s by %s@%s; key %s", kind, application.RepoID,
				application.AgentName, application.DeviceName, binding.KeyMask),
			auditpush.ChannelConsole, repoKeyTags(application.AgentName, application.DeviceName)),
	})
	if newBinding != nil && (err != nil || decided.Status != "approved") {
		err = restoreRepoKeyGateway(ctx, client, keys, err)
	}
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	return decided, nil
}

// repoKeyForApproval resolves, at decision time, which key this approval hands out. A pending
// application that waited while the repository changed underneath it is decided against the
// repository as it is now, never against the state the applicant saw.
func (s *Service) repoKeyForApproval(ctx context.Context, client interface {
	UpdateClientAPIKeys(context.Context, []string) error
}, keys []string, repoID string, repoHasRemote bool, createdBy string, nowMS int64) (repository.RepoKeyBinding, string, string, error) {
	status, err := s.Repo.GetRepoKeyRepoStatus(ctx, repoKeyInstanceID, repoID)
	if err != nil {
		return repository.RepoKeyBinding{}, "", "", err
	}
	if status.State == "active" {
		secret, secretErr := s.repoKeySecret(ctx, status.Binding.KeyFingerprint)
		if secretErr != nil {
			return repository.RepoKeyBinding{}, "", "", secretErr
		}
		return *status.Binding, secret, "reuse", nil
	}
	kind := "issue"
	if status.State == "stopped" {
		kind = "reissue"
	}
	secret, err := repokey.GenerateKeyValue()
	if err != nil {
		return repository.RepoKeyBinding{}, "", "", err
	}
	fingerprint, err := s.Repo.UsageClientKeyFingerprint(secret)
	if err != nil {
		return repository.RepoKeyBinding{}, "", "", err
	}
	sealed, err := s.sealRepoKey(secret)
	if err != nil {
		return repository.RepoKeyBinding{}, "", "", err
	}
	binding := repository.RepoKeyBinding{
		ID: uuid.NewString(), InstanceID: repoKeyInstanceID, RepoID: repoID, RepoHasRemote: repoHasRemote,
		KeyFingerprint: fingerprint, KeyMask: security.MaskSecret(secret), CreatedBy: createdBy,
		KeySealed: sealed, Status: "active", CreatedAtMS: nowMS,
	}
	// CPA has no prepare/commit operation. Keep ownership provisional until the same
	// SQLite transaction accepts the approval, caller registration and audit record.
	if err = client.UpdateClientAPIKeys(ctx, append(append([]string{}, keys...), secret)); err != nil {
		return repository.RepoKeyBinding{}, "", "", configWriteOutcome(err)
	}
	return binding, secret, kind, nil
}

// ErrRepoKeyGatewayRestoreFailed means the local decision was refused but CPA could not
// be restored. Callers must not report that nothing changed in this state.
var ErrRepoKeyGatewayRestoreFailed = errors.New("repo_key_gateway_restore_failed")

func restoreRepoKeyGateway(ctx context.Context, client interface {
	UpdateClientAPIKeys(context.Context, []string) error
}, keys []string, cause error) error {
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := client.UpdateClientAPIKeys(restoreCtx, keys); err != nil {
		return errors.Join(cause, ErrRepoKeyGatewayRestoreFailed)
	}
	return cause
}

func (s *Service) removeRepoKeyFromGateway(ctx context.Context, client interface {
	UpdateClientAPIKeys(context.Context, []string) error
}, keys []string, fingerprint string) (bool, error) {
	index := s.repoKeyIndex(keys, fingerprint)
	if index < 0 {
		return false, nil
	}
	remaining := append(append([]string{}, keys[:index]...), keys[index+1:]...)
	return true, client.UpdateClientAPIKeys(ctx, remaining)
}

// RejectRepoKeyApplication records the operator's refusal and the reason the applicant reads
// back (AC-7).
func (s *Service) RejectRepoKeyApplication(ctx context.Context, applicationID, operator, reason string) (repository.RepoKeyApplication, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return repository.RepoKeyApplication{}, invalidParameters{detail: "A rejection must say why, so the applicant can correct the application."}
	}
	application, err := s.Repo.GetRepoKeyApplication(ctx, applicationID)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if application.Status != "pending" {
		return application, nil
	}
	return s.Repo.DecideRepoKeyApplication(ctx, repository.ApplicationDecision{
		ApplicationID: application.ID, Approve: false, DecidedBy: operator, RejectReason: reason,
	}, time.Now().UnixMilli(), repository.AuditEvent{
		Action: AuditRepoKeyReject, SourceSummary: "console",
		Details: map[string]any{
			"repo": application.RepoID, "agent": application.AgentName, "device": application.DeviceName,
			"kind": application.Kind, "reject_reason": reason,
		},
		Push: repoKeyPush(AuditRepoKeyReject, application.ID, application.RepoID,
			fmt.Sprintf("rejected the application for %s from %s@%s: %s", application.RepoID,
				application.AgentName, application.DeviceName, reason),
			auditpush.ChannelConsole, repoKeyTags(application.AgentName, application.DeviceName)),
	})
}

// StopRepoKeyBinding removes the key from the gateway and keeps every record about it. CPA has
// no disabled state, so removal is what stops it (AC-10, AC-11).
func (s *Service) StopRepoKeyBinding(ctx context.Context, bindingID, operator, category, reason string) (repository.RepoKeyBinding, error) {
	reason = strings.TrimSpace(reason)
	switch category {
	case "leaked", "rotate", "misuse", "other":
	default:
		return repository.RepoKeyBinding{}, repository.ErrRepoKeyInvalid
	}
	if reason == "" {
		return repository.RepoKeyBinding{}, repository.ErrRepoKeyInvalid
	}
	release, err := s.lock(ctx)
	if err != nil {
		return repository.RepoKeyBinding{}, err
	}
	defer release()
	binding, err := s.Repo.GetRepoKeyBindingByID(ctx, bindingID)
	if err != nil {
		return repository.RepoKeyBinding{}, err
	}
	if binding.Status != "active" {
		return repository.RepoKeyBinding{}, repository.ErrRepoKeyConflict
	}
	client, err := s.Client(ctx)
	if err != nil {
		return repository.RepoKeyBinding{}, err
	}
	keys, err := client.ClientAPIKeys(ctx)
	if err != nil {
		return repository.RepoKeyBinding{}, err
	}
	removed, err := s.removeRepoKeyFromGateway(ctx, client, keys, binding.KeyFingerprint)
	if err != nil {
		return repository.RepoKeyBinding{}, configWriteOutcome(err)
	}
	nowMS := time.Now().UnixMilli()
	stopped, err := s.Repo.StopRepoKeyBinding(ctx, binding.ID, operator, category, reason, nowMS, repository.AuditEvent{
		Action: AuditRepoKeyStop, SourceSummary: "console",
		Details: map[string]any{
			"repo": binding.RepoID, "key_mask": binding.KeyMask,
			"category": category, "reason": reason,
		},
		Push: repoKeyPush(AuditRepoKeyStop, binding.ID, binding.RepoID,
			fmt.Sprintf("stopped key %s for %s (%s): %s", binding.KeyMask, binding.RepoID, category, reason),
			auditpush.ChannelConsole, nil),
	})
	if err != nil && removed {
		err = restoreRepoKeyGateway(ctx, client, keys, err)
	}
	return stopped, err
}

// RequestRepoKeyReenable files a request to put a stopped key back in service (AC-31).
//
// It is a request, not a state change: it waits in the same approval queue as a key
// application and is approved by a second, deliberate action. The operator states why, which
// is both the request's purpose and, unless the approver writes another, the reason recorded
// on the re-enable. A leaked key has no re-enable path at all.
func (s *Service) RequestRepoKeyReenable(ctx context.Context, bindingID, operator, reason string) (repository.RepoKeyApplication, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return repository.RepoKeyApplication{}, invalidParameters{detail: "Re-enabling needs a reason; it is recorded on the request and in the audit trail."}
	}
	binding, err := s.Repo.GetRepoKeyBindingByID(ctx, bindingID)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if binding.Status != "stopped" {
		return repository.RepoKeyApplication{}, errors.New("repo_key_not_stopped")
	}
	if binding.StopCategory == "leaked" {
		return repository.RepoKeyApplication{}, errors.New("repo_key_leaked_never_reenabled")
	}
	if strings.TrimSpace(binding.KeySealed) == "" {
		return repository.RepoKeyApplication{}, errors.New("repo_key_not_reenable")
	}
	token, err := repokey.RandomToken()
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	nowMS := time.Now().UnixMilli()
	application := repository.RepoKeyApplication{
		ID:         uuid.NewString(),
		InstanceID: repoKeyInstanceID, RepoID: binding.RepoID, RepoHasRemote: binding.RepoHasRemote,
		AgentName: RepoKeyConsoleDevice, DeviceName: RepoKeyConsoleDevice,
		Purpose: reason, Kind: "reuse",
		KeyFingerprint: binding.KeyFingerprint, KeyMask: binding.KeyMask,
		Source: "console", RequestKind: "reenable", TargetBindingID: binding.ID,
		AuthorizationTokenHash: repokey.TokenFingerprint(token),
		TokenExpiresAtMS:       nowMS + repoKeyApprovalLifetime.Milliseconds(),
	}
	stored, _, err := s.Repo.CreateRepoKeyApplication(ctx, application, nowMS, repository.AuditEvent{
		Action: AuditRepoKeyApply, SourceSummary: "console",
		Details: map[string]any{
			"repo": binding.RepoID, "key_mask": binding.KeyMask, "binding_id": binding.ID,
			"request_kind": "reenable", "reason": reason, "operator": operator,
		},
		Push: repoKeyPush(AuditRepoKeyApply, application.ID, binding.RepoID,
			fmt.Sprintf("requested re-enabling key %s for %s: %s", binding.KeyMask, binding.RepoID, reason),
			auditpush.ChannelConsole, nil),
	})
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	return stored, nil
}

// approveRepoKeyReenable applies an approved re-enable request. The key comes back from the
// sealed copy kept when it was issued - the same key, not a new one (AC-31) - and is written
// back into the gateway before the records say it is in service.
func (s *Service) approveRepoKeyReenable(ctx context.Context, application repository.RepoKeyApplication, operator, reason string) (repository.RepoKeyApplication, error) {
	binding, err := s.Repo.GetRepoKeyBindingByID(ctx, application.TargetBindingID)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if binding.Status != "stopped" {
		return repository.RepoKeyApplication{}, errors.New("repo_key_not_stopped")
	}
	if binding.StopCategory == "leaked" {
		return repository.RepoKeyApplication{}, errors.New("repo_key_leaked_never_reenabled")
	}
	if _, activeErr := s.Repo.GetActiveRepoKeyBinding(ctx, binding.InstanceID, binding.RepoID); activeErr == nil {
		return repository.RepoKeyApplication{}, repository.ErrRepoKeyConflict
	} else if !errors.Is(activeErr, repository.ErrRepoKeyNotFound) {
		return repository.RepoKeyApplication{}, activeErr
	}
	secret, err := s.unsealRepoKey(binding.KeySealed)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	fingerprint, err := s.Repo.UsageClientKeyFingerprint(secret)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if fingerprint != binding.KeyFingerprint {
		return repository.RepoKeyApplication{}, errors.New("repo_key_mismatch")
	}
	client, err := s.Client(ctx)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	keys, err := client.ClientAPIKeys(ctx)
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	if s.repoKeyIndex(keys, fingerprint) >= 0 {
		return repository.RepoKeyApplication{}, errors.New("repo_key_already_in_gateway")
	}
	if err = client.UpdateClientAPIKeys(ctx, append(append([]string{}, keys...), secret)); err != nil {
		return repository.RepoKeyApplication{}, configWriteOutcome(err)
	}
	if strings.TrimSpace(reason) == "" {
		reason = application.Purpose
	}
	nowMS := time.Now().UnixMilli()
	approved, err := s.Repo.ApproveRepoKeyReenable(ctx, application.ID, operator, reason, nowMS, repository.AuditEvent{
		Action: AuditRepoKeyReenable, SourceSummary: "console",
		Details: map[string]any{
			"repo": binding.RepoID, "key_mask": binding.KeyMask,
			"previous_category": binding.StopCategory, "reason": reason,
		},
		Push: repoKeyPush(AuditRepoKeyReenable, binding.ID, binding.RepoID,
			fmt.Sprintf("re-enabled key %s for %s: %s", binding.KeyMask, binding.RepoID, reason),
			auditpush.ChannelConsole, nil),
	})
	if err != nil || approved.Status != "approved" {
		err = restoreRepoKeyGateway(ctx, client, keys, err)
	}
	if err != nil {
		return repository.RepoKeyApplication{}, err
	}
	return approved, nil
}

// sealRepoKey keeps a copy of the key under the deployment cipher. It is what makes
// "re-enable restores this key" possible after the gateway no longer has it (AC-31); the
// stored form is a ciphertext, and a leaked key's copy is dropped at the stop.
func (s *Service) sealRepoKey(secret string) (string, error) {
	if s.Cipher == nil {
		return "", errors.New("cipher_unavailable")
	}
	ciphertext, nonce, err := s.Cipher.Encrypt([]byte(secret))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext) + ":" + base64.StdEncoding.EncodeToString(nonce), nil
}

func (s *Service) unsealRepoKey(sealed string) (string, error) {
	if s.Cipher == nil {
		return "", errors.New("cipher_unavailable")
	}
	parts := strings.Split(sealed, ":")
	if len(parts) != 2 {
		return "", errors.New("repo_key_sealed_unreadable")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("repo_key_sealed_unreadable")
	}
	nonce, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("repo_key_sealed_unreadable")
	}
	plain, err := s.Cipher.Decrypt(ciphertext, nonce)
	if err != nil {
		return "", errors.New("repo_key_sealed_unreadable")
	}
	return strings.TrimSpace(string(plain)), nil
}

// RevealRepoKeyPlaintext is the console's documented fallback: one deliberate click by a signed
// in operator, recorded with the reason they gave (AC-36). It is not part of any agent path.
func (s *Service) RevealRepoKeyPlaintext(ctx context.Context, bindingID, operator, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", invalidParameters{detail: "Write why the plaintext is needed; the reason is recorded in the audit trail."}
	}
	binding, err := s.Repo.GetRepoKeyBindingByID(ctx, bindingID)
	if err != nil {
		return "", err
	}
	if binding.Status != "active" {
		return "", errors.New("repo_key_not_active")
	}
	secret, err := s.repoKeySecret(ctx, binding.KeyFingerprint)
	if err != nil {
		return "", err
	}
	operator = strings.TrimSpace(operator)
	if operator == "" {
		operator = "console"
	}
	// The reveal is a hand-out like any other: it is recorded as a delivery to the console,
	// so the key's 领取记录 shows it next to the envelopes handed to callers (AC-20).
	if _, err = s.Repo.RecordRepoKeyDelivery(ctx, repository.RepoKeyDelivery{
		InstanceID: repoKeyInstanceID, BindingID: binding.ID,
		KeyFingerprint: binding.KeyFingerprint, KeyMask: binding.KeyMask,
		AgentName: RepoKeyConsoleDevice, DeviceName: RepoKeyConsoleDevice,
		Kind: "reuse", Operator: "console_reveal", DeliveredAtMS: time.Now().UnixMilli(),
	}, repository.AuditEvent{
		Action: AuditRepoKeyReveal, SourceSummary: "console",
		Details: map[string]any{
			"repo": binding.RepoID, "key_mask": binding.KeyMask, "operator": operator, "reason": reason,
		},
		Push: repoKeyPush(AuditRepoKeyReveal, binding.ID, binding.RepoID,
			fmt.Sprintf("console reveal of key %s for %s: %s", binding.KeyMask, binding.RepoID, reason),
			auditpush.ChannelConsole, nil),
	}); err != nil {
		return "", err
	}
	return secret, nil
}

// registerRepoKeys declares the four MCP capabilities (AC-24). They are mcp-only: the console
// uses its own routes, and no capability hands out a plaintext or approves anything (AC-25).
func (s *Service) registerRepoKeys(registry *capability.Registry) error {
	mcpOnly := func(metadata capability.Metadata) capability.Metadata {
		metadata.Adapters = []string{"mcp"}
		return metadata
	}
	metadata := mcpOnly(Meta("repo_key_query", "Ask whether a repository already has a usable client key, and what applying next would do. Returns the key mask, never the key.", "read", "low"))
	if err := capability.Register(registry, metadata, nil, func(ctx context.Context, input RepoKeyQueryInput, _, _ string) (RepoKeyQueryResult, error) {
		return s.QueryRepoKey(ctx, input)
	}); err != nil {
		return err
	}
	metadata = mcpOnly(Meta("repo_key_apply", "Apply for the repository's client key. A person approves the first application; while it is pending the answer carries a one-time approval link (approval_url) to put in front of them. The answer never carries the key itself.", "write", "low"))
	metadata.Invalidates = []string{"repo-keys", "client-api-keys"}
	if err := capability.Register(registry, metadata, nil, func(ctx context.Context, input RepoKeyApplyInput, _, _ string) (RepoKeyApplyResult, error) {
		return s.ApplyRepoKey(ctx, input)
	}); err != nil {
		return err
	}
	type statusInput struct {
		ApplicationID string `json:"application_id" jsonschema:"the application_id returned when the key was applied for"`
	}
	metadata = mcpOnly(Meta("repo_key_status", "Read the state of one key application: pending, approved, or rejected with the reason.", "read", "low"))
	if err := capability.Register(registry, metadata, nil, func(ctx context.Context, input statusInput, _, _ string) (RepoKeyStatusResult, error) {
		return s.RepoKeyStatus(ctx, input.ApplicationID)
	}); err != nil {
		return err
	}
	metadata = mcpOnly(Meta("repo_key_ciphertext", "Fetch the ciphertext of an approved application. It opens only with the private key you registered; each fetch is recorded.", "read", "low"))
	if err := capability.Register(registry, metadata, nil, func(ctx context.Context, input statusInput, _, _ string) (RepoKeyCiphertextResult, error) {
		return s.FetchRepoKeyCiphertext(ctx, input.ApplicationID, "")
	}); err != nil {
		return err
	}
	return nil
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
