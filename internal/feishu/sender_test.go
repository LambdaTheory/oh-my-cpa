package feishu

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

type captureFeishuTransport struct {
	t          *testing.T
	recipients []string
}

func (transport *captureFeishuTransport) Do(request *http.Request) (*http.Response, error) {
	transport.t.Helper()
	body := ""
	if strings.HasSuffix(request.URL.Path, "/auth/v3/tenant_access_token/internal") {
		body = "{\"code\":0,\"tenant_access_token\":\"synthetic-token\",\"expire\":7200}"
	} else if strings.HasSuffix(request.URL.Path, "/im/v1/messages") {
		if request.URL.Query().Get("receive_id_type") != "open_id" {
			transport.t.Fatal("notification did not address a person")
		}
		payload, _ := io.ReadAll(request.Body)
		transport.recipients = append(transport.recipients, string(payload))
		body = "{\"code\":0,\"data\":{\"message_id\":\"synthetic-message\"}}"
	} else {
		transport.t.Fatalf("unexpected request %s", request.URL.Path)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestSDKSenderAddressesOneOpenIDThroughFakeTransport(t *testing.T) {
	transport := &captureFeishuTransport{t: t}
	sender := &larkSender{client: lark.NewClient("synthetic-app", "synthetic-secret", lark.WithHttpClient(transport))}
	if err := sender.SendCard(context.Background(), "ou_synthetic", map[string]any{"schema": "2.0"}); err != nil {
		t.Fatal(err)
	}
	if len(transport.recipients) != 1 || !strings.Contains(transport.recipients[0], "ou_synthetic") {
		t.Fatalf("recipients=%v", transport.recipients)
	}
}
