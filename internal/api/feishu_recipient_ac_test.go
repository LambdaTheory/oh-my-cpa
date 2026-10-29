package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestAC02FeishuInvalidSavePreservesRecipient(t *testing.T) {
	for _, invalid := range []string{"oc_", "ou_", "synthetic@example.test"} {
		settings := repository.FeishuSettings{ChatID: "oc_existing"}
		if err := applyFeishuWrite(&settings, feishuSettingsWriteDTO{ChatID: &invalid}); err == nil || settings.ChatID != "oc_existing" {
			t.Fatalf("invalid recipient changed saved target: %q", invalid)
		}
	}
}

func TestAC12OAuthGroupConflictWarnsWithoutBlockingSave(t *testing.T) {
	for _, test := range []struct {
		name         string
		recipient    string
		wantConflict bool
	}{
		{"same OAuth group", "oc_synthetic-oauth", true},
		{"different group", "oc_synthetic-notifications", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, repo, _, _ := newFeishuAPITest(t)
			f.overlay = feishu.Overlay{OAuthChatID: "oc_synthetic-oauth"}
			sender := &apiPredictionSender{}
			f.service = feishu.NewService(feishu.Options{
				Overlay:       f.overlay,
				SenderFactory: func(feishu.Resolved) feishu.Sender { return sender },
			})
			t.Cleanup(f.service.Stop)
			response := httptest.NewRecorder()
			f.putSettings(response, httptest.NewRequest(http.MethodPut, "/feishu/settings", strings.NewReader(`{"chat_id":"`+test.recipient+`"}`)))
			if response.Code != http.StatusOK {
				t.Fatalf("recipient warning blocked save: status=%d body=%s", response.Code, response.Body.String())
			}
			saved, found, err := repo.LoadFeishuSettings(context.Background())
			if err != nil || !found || saved.ChatID != test.recipient {
				t.Fatalf("recipient was not persisted: found=%v recipient=%q err=%v", found, saved.ChatID, err)
			}
			status := f.service.StatusSnapshot(context.Background())
			if status.OAuthGroupConflict != test.wantConflict || status.Recipient != test.recipient {
				t.Fatalf("recipient=%q conflict=%v, want recipient=%q conflict=%v", status.Recipient, status.OAuthGroupConflict, test.recipient, test.wantConflict)
			}
			if sender.count() != 0 {
				t.Fatal("saving a recipient sent a notification")
			}
		})
	}
}

func TestAC08FeishuConfirmationRejectsStaleTargetAndPersists(t *testing.T) {
	f, repo, _, _ := newFeishuAPITest(t)
	settings := repository.FeishuSettings{ChatID: "oc_synthetic", AppID: "synthetic-app"}
	if err := repo.SaveFeishuSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		recipient string
		status    int
	}{{"oc_stale", http.StatusConflict}, {"oc_synthetic", http.StatusOK}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/feishu/recipient-confirmation", strings.NewReader(`{"recipient":"`+test.recipient+`"}`))
		f.postRecipientConfirmation(response, request)
		if response.Code != test.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		saved, _, err := repo.LoadFeishuSettings(context.Background())
		if err != nil || saved.ChatID != settings.ChatID {
			t.Fatal("confirmation changed recipient")
		}
		if test.status == http.StatusConflict && saved.ConfirmedRecipient != "" || test.status == http.StatusOK && saved.ConfirmedRecipient != settings.ChatID {
			t.Fatalf("confirmation persisted incorrectly: %q", saved.ConfirmedRecipient)
		}
	}
	web, err := loadFeishuWebConfig(context.Background(), repo)
	if err != nil || web.ConfirmedRecipient != settings.ChatID {
		t.Fatal("confirmation did not survive configuration reload")
	}
}

func TestAC03FeishuDeploymentRecipientCannotBeOverwritten(t *testing.T) {
	f, repo, _, _ := newFeishuAPITest(t)
	recipient := "oc_environment"
	f.overlay = feishu.Overlay{ChatID: &recipient}
	response := httptest.NewRecorder()
	f.putSettings(response, httptest.NewRequest(http.MethodPut, "/feishu/settings", strings.NewReader(`{"chat_id":"ou_other"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, found, err := repo.LoadFeishuSettings(context.Background()); err != nil || found {
		t.Fatal("rejected environment edit persisted settings")
	}
}

func TestAC08ConfirmationRefusesAuditFailure(t *testing.T) {
	f, repo, _, _ := newFeishuAPITest(t)
	if err := repo.SaveFeishuSettings(context.Background(), repository.FeishuSettings{ChatID: "oc_synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SQL().ExecContext(context.Background(), `CREATE TRIGGER synthetic_audit_failure BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	f.postRecipientConfirmation(response, httptest.NewRequest(http.MethodPost, "/feishu/recipient-confirmation", strings.NewReader(`{"recipient":"oc_synthetic"}`)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	saved, _, err := repo.LoadFeishuSettings(context.Background())
	if err != nil || saved.ConfirmedRecipient != "" {
		t.Fatal("failed audit authorized delivery")
	}
}

func TestAC08OrdinarySaveCannotBypassLegacyConfirmation(t *testing.T) {
	f, repo, _, _ := newFeishuAPITest(t)
	if err := repo.SaveFeishuSettings(context.Background(), repository.FeishuSettings{ChatID: "oc_synthetic"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	f.putSettings(response, httptest.NewRequest(http.MethodPut, "/feishu/settings", strings.NewReader(`{"chat_id":"oc_synthetic"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	saved, _, err := repo.LoadFeishuSettings(context.Background())
	if err != nil || saved.ConfirmedRecipient != "" {
		t.Fatal("ordinary settings save bypassed legacy confirmation")
	}
}
