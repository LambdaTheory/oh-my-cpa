package keyclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// modelsTestKey is a stand-in gateway key. Every failure test asserts it never reaches
// an error message, which is the same rule the rest of the package follows.
const modelsTestKey = "sk-cpa-models-test-9999-8888-7777"

func TestParseModelListTrimsDeduplicatesAndSorts(t *testing.T) {
	models, err := ParseModelList(" gpt-6.2-sol, claude-sonnet-5-5 ,gpt-6.2-sol, ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "claude-sonnet-5-5,gpt-6.2-sol" {
		t.Fatalf("models = %v, want them sorted and deduplicated", models)
	}
}

func TestParseModelListEmptyIsNoList(t *testing.T) {
	for _, value := range []string{"", "   ", "\t"} {
		models, err := ParseModelList(value)
		if err != nil || models != nil {
			t.Fatalf("%q: models = %v (err=%v), want no list and no error", value, models, err)
		}
	}
}

func TestParseModelListRejectsAValueWithNoIDs(t *testing.T) {
	_, err := ParseModelList(" , , ")
	if CodeOf(err) != CODE_MODELS_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_MODELS_REQUIRED, err)
	}
	if !strings.Contains(MessageOf(err), "--models") {
		t.Fatalf("the message does not name the flag: %q", MessageOf(err))
	}
}

func TestNormalizeModelIDsRejectsControlCharacters(t *testing.T) {
	_, err := NormalizeModelIDs([]string{"gpt\n6.2-sol"})
	if CodeOf(err) != CODE_MODELS_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_MODELS_REQUIRED, err)
	}
}

func TestFetchModelIDsReadsTheGatewayList(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != MODELS_PATH {
			t.Errorf("path = %q, want %q", request.URL.Path, MODELS_PATH)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+modelsTestKey {
			t.Errorf("Authorization = %q, want the repository key as a bearer token", got)
		}
		if strings.Contains(request.URL.RawQuery, modelsTestKey) {
			t.Errorf("the key reached the query string: %q", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"object":"list","data":[{"id":"gpt-6.2-sol"},{"id":"claude-sonnet-5-5"},{"id":"gpt-6.2-sol"},{"id":""}]}`))
	}))
	defer server.Close()

	models, err := FetchModelIDs(context.Background(), server.URL, modelsTestKey)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one", requests)
	}
	if strings.Join(models, ",") != "claude-sonnet-5-5,gpt-6.2-sol" {
		t.Fatalf("models = %v, want the gateway's ids sorted and deduplicated", models)
	}
}

func TestFetchModelIDsReportsHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusNotFound} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte(`{"error":"nope"}`))
		}))
		_, err := FetchModelIDs(context.Background(), server.URL, modelsTestKey)
		server.Close()
		if CodeOf(err) != CODE_MODELS_UNAVAILABLE {
			t.Fatalf("HTTP %d: code = %q, want %q (err=%v)", status, CodeOf(err), CODE_MODELS_UNAVAILABLE, err)
		}
		if ExitCode(err) != EXIT_FAILED {
			t.Fatalf("HTTP %d: exit = %d, want %d", status, ExitCode(err), EXIT_FAILED)
		}
		if !strings.Contains(MessageOf(err), "--models") {
			t.Fatalf("HTTP %d: the message does not name the way forward: %q", status, MessageOf(err))
		}
		if strings.Contains(err.Error(), modelsTestKey) {
			t.Fatalf("HTTP %d: the error carries the key: %q", status, err.Error())
		}
	}
}

func TestFetchModelIDsReportsMalformedBodies(t *testing.T) {
	bodies := []string{"not json", `{"data":"nope"}`, ``}
	for _, body := range bodies {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(body))
		}))
		_, err := FetchModelIDs(context.Background(), server.URL, modelsTestKey)
		server.Close()
		if CodeOf(err) != CODE_MODELS_UNAVAILABLE {
			t.Fatalf("body %q: code = %q, want %q (err=%v)", body, CodeOf(err), CODE_MODELS_UNAVAILABLE, err)
		}
		if strings.Contains(err.Error(), modelsTestKey) {
			t.Fatalf("body %q: the error carries the key: %q", body, err.Error())
		}
	}
}

func TestFetchModelIDsReportsAnEmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer server.Close()
	_, err := FetchModelIDs(context.Background(), server.URL, modelsTestKey)
	if CodeOf(err) != CODE_MODELS_UNAVAILABLE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_MODELS_UNAVAILABLE, err)
	}
	if !strings.Contains(MessageOf(err), "--models") {
		t.Fatalf("the message does not name the way forward: %q", MessageOf(err))
	}
}

func TestFetchModelIDsReportsAnUnreachableGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))
	address := server.URL
	server.Close()
	_, err := FetchModelIDs(context.Background(), address, modelsTestKey)
	if CodeOf(err) != CODE_MODELS_UNAVAILABLE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_MODELS_UNAVAILABLE, err)
	}
	if strings.Contains(err.Error(), modelsTestKey) {
		t.Fatalf("the error carries the key: %q", err.Error())
	}
}

func TestFetchModelIDsRequiresAGatewayAddress(t *testing.T) {
	_, err := FetchModelIDs(context.Background(), "  ", modelsTestKey)
	if CodeOf(err) != CODE_BASE_URL_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_BASE_URL_REQUIRED, err)
	}
}
