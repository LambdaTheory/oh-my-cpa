package management

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func newQuotaProviderClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := newV8Server(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "management-secret", time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// The gateway's wrapped document is decoded field by field; the literal space
// inside a provider key is part of the key and must arrive unchanged.
func TestQuotaProvidersDecodesTheWrappedShape(t *testing.T) {
	client := newQuotaProviderClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", request.Method)
		}
		if request.URL.Path != "/v8/management/credentials/quota/providers" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer management-secret" {
			t.Fatalf("authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"providers":[
			{"plugin_id":"ollama-quota","provider":"ollama","display_name":"Ollama Quota","supported_providers":["ollama"],"supports_reset":true},
			{"plugin_id":"bulk-quota","provider":"openai-compatible-command code","display_name":"Command Code Quota","supported_providers":["openai-compatible-command code","openai-compatible-opencode"]}
		]}`))
	})

	providers, err := client.QuotaProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %#v, want two entries", providers)
	}
	first := providers[0]
	if first.PluginID != "ollama-quota" || first.Provider != "ollama" || first.DisplayName != "Ollama Quota" || !first.SupportsReset {
		t.Errorf("first entry = %#v", first)
	}
	if len(first.SupportedProviders) != 1 || first.SupportedProviders[0] != "ollama" {
		t.Errorf("first supported = %#v", first.SupportedProviders)
	}
	if got := providers[1].SupportedProviders[0]; got != "openai-compatible-command code" {
		t.Errorf("second supported[0] = %q, want the literal key", got)
	}
}

// Some hosts answer with the bare array: the same entries, no wrapper.
func TestQuotaProvidersAcceptsABareArray(t *testing.T) {
	client := newQuotaProviderClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"plugin_id":"opencode-quota","provider":"openai-compatible-opencode","supported_providers":["openai-compatible-opencode"]}]`))
	})

	providers, err := client.QuotaProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].PluginID != "opencode-quota" || providers[0].SupportsReset {
		t.Fatalf("providers = %#v, want the single opencode entry", providers)
	}
}

// An empty registry is an empty list, not a nil one: callers range over it.
func TestQuotaProvidersReturnsAnEmptyList(t *testing.T) {
	for name, body := range map[string]string{
		"wrapped": `{"providers":[]}`,
		"bare":    `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			client := newQuotaProviderClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(body))
			})
			providers, err := client.QuotaProviders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if providers == nil || len(providers) != 0 {
				t.Fatalf("providers = %#v, want a non-nil empty list", providers)
			}
		})
	}
}

// A host that does not serve the route reports its HTTP failure as such; the
// quota service turns that into the fallback error instead of a target.
func TestQuotaProvidersReportsTransportFailure(t *testing.T) {
	client := newQuotaProviderClient(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"error":"plugin_host_unavailable"}`))
	})

	_, err := client.QuotaProviders(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("err = %v, want the 502 HTTP error", err)
	}
}
