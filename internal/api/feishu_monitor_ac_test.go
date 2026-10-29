package api

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
)

func TestAC26MonitorFailureEvidenceSurvivesSQLiteReload(t *testing.T) {
	_, repo, _, _ := newFeishuAPITest(t)
	store := feishuStore{repo: repo}
	state := feishu.EmptyState()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	state.ChannelTracks["codex"] = feishu.ChannelTrack{ConsecutiveFailures: 3, LastAttempt: now, FirstSeen: now.Add(-time.Hour), LastValid: now.Add(-30 * time.Minute)}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	track := loaded.ChannelTracks["codex"]
	if track.ConsecutiveFailures != 3 || !track.LastAttempt.Equal(now) || !track.LastValid.Equal(state.ChannelTracks["codex"].LastValid) {
		t.Fatalf("lost monitor evidence: %+v", track)
	}
}
