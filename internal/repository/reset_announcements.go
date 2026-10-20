package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrInvalidResetAnnouncement = errors.New("invalid reset announcement")
var RESET_ANNOUNCEMENT_PROVIDER = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type ResetAnnouncementInput struct {
	Provider          string
	Source            string
	ExpectedResetAtMS *int64
	Scope             string
	Model             string
	AuthIndex         string
	Confidence        string
	OriginalText      string
}

type ResetAnnouncement struct {
	ResetAnnouncementInput
	ID             string
	RegisteredAtMS int64
	CreatedAtMS    int64
	Status         string
}

type ResetAnnouncementFilter struct {
	Status string
	Limit  int
	Offset int
}

// Source admission belongs to ingestion. Future adapters persist this same
// evidence shape; readers never use the transport as a confidence signal.
func (r *Repository) CreateResetAnnouncement(ctx context.Context, input ResetAnnouncementInput, nowMS int64) (ResetAnnouncement, error) {
	if !RESET_ANNOUNCEMENT_PROVIDER.MatchString(input.Provider) || input.Source != "manual" || nowMS <= 0 ||
		(input.ExpectedResetAtMS != nil && (*input.ExpectedResetAtMS <= nowMS || *input.ExpectedResetAtMS > 253402300799999)) ||
		(input.Confidence != "confirmed" && input.Confidence != "probable" && input.Confidence != "unverified") ||
		strings.TrimSpace(input.OriginalText) == "" || !utf8.ValidString(input.OriginalText) || strings.ContainsRune(input.OriginalText, 0) || utf8.RuneCountInString(input.OriginalText) > 4000 {
		return ResetAnnouncement{}, ErrInvalidResetAnnouncement
	}
	switch input.Scope {
	case "all":
		if input.Model != "" || input.AuthIndex != "" {
			return ResetAnnouncement{}, ErrInvalidResetAnnouncement
		}
	case "model":
		if strings.TrimSpace(input.Model) == "" || len(input.Model) > 256 || input.AuthIndex != "" {
			return ResetAnnouncement{}, ErrInvalidResetAnnouncement
		}
	case "credential":
		if strings.TrimSpace(input.AuthIndex) == "" || len(input.AuthIndex) > 256 || input.Model != "" {
			return ResetAnnouncement{}, ErrInvalidResetAnnouncement
		}
	default:
		return ResetAnnouncement{}, ErrInvalidResetAnnouncement
	}
	item := ResetAnnouncement{ResetAnnouncementInput: input, ID: uuid.NewString(), RegisteredAtMS: nowMS, CreatedAtMS: nowMS, Status: "active"}
	_, err := r.SQL().ExecContext(ctx, `INSERT INTO reset_announcements
 (id, provider, source, expected_reset_at_ms, scope, model, auth_index, confidence, original_text, registered_at_ms, created_at_ms)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.Provider, item.Source, item.ExpectedResetAtMS, item.Scope, item.Model, item.AuthIndex, item.Confidence, item.OriginalText, nowMS, nowMS)
	if err != nil {
		return ResetAnnouncement{}, fmt.Errorf("create reset announcement: %w", err)
	}
	return item, nil
}

// A single reader clock determines both membership and status, including the
// exact boundary. Expiration never mutates stored evidence.
func (r *Repository) ListResetAnnouncements(ctx context.Context, filter ResetAnnouncementFilter, nowMS int64) ([]ResetAnnouncement, int, error) {
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || (filter.Status != "" && filter.Status != "active" && filter.Status != "expired") {
		return nil, 0, ErrInvalidResetAnnouncement
	}
	where := " WHERE 1=1"
	args := []any{}
	if filter.Status == "active" {
		where += " AND (expected_reset_at_ms IS NULL OR expected_reset_at_ms > ?)"
		args = append(args, nowMS)
	}
	if filter.Status == "expired" {
		where += " AND expected_reset_at_ms <= ?"
		args = append(args, nowMS)
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("begin announcement read: %w", err)
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM reset_announcements"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reset announcements: %w", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, provider, source, expected_reset_at_ms, scope, model, auth_index, confidence, original_text, registered_at_ms, created_at_ms FROM reset_announcements"+where+" ORDER BY registered_at_ms DESC, id DESC LIMIT ? OFFSET ?", append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list reset announcements: %w", err)
	}
	defer rows.Close()
	items := []ResetAnnouncement{}
	for rows.Next() {
		var item ResetAnnouncement
		if err = rows.Scan(&item.ID, &item.Provider, &item.Source, &item.ExpectedResetAtMS, &item.Scope, &item.Model, &item.AuthIndex, &item.Confidence, &item.OriginalText, &item.RegisteredAtMS, &item.CreatedAtMS); err != nil {
			return nil, 0, fmt.Errorf("scan reset announcement: %w", err)
		}
		item.Status = "active"
		if item.ExpectedResetAtMS != nil && *item.ExpectedResetAtMS <= nowMS {
			item.Status = "expired"
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read reset announcements: %w", err)
	}
	if err = rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close announcement read: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("finish announcement read: %w", err)
	}
	return items, total, nil
}
