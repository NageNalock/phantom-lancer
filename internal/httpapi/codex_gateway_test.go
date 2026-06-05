package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"phantom-lancer/internal/codexgateway"
	"phantom-lancer/internal/storage"
)

func TestCodexGatewayPublicModelsRequiresTokenAndEnabled(t *testing.T) {
	server, store, token := newCodexGatewayHTTPTest(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	server.handleCodexGatewayPublicModels(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	server.handleCodexGatewayPublicModels(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	setCodexGatewayEnabled(t, store, true)
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	server.handleCodexGatewayPublicModels(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("enabled status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body codexgateway.ModelList
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if len(body.Data) == 0 || body.Data[0].Object != "model" {
		t.Fatalf("models body = %#v", body)
	}
}

func TestCodexGatewayPublicModelHonorsDisabled(t *testing.T) {
	server, _, token := newCodexGatewayHTTPTest(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-5-codex", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	server.handleCodexGatewayPublicModel(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestCodexGatewayChatCompletionsAuthenticatesBeforeJSON(t *testing.T) {
	server, _, token := newCodexGatewayHTTPTest(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{"))
	server.handleCodexGatewayChatCompletions(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized invalid JSON status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{"))
	request.Header.Set("Authorization", "Bearer "+token)
	server.handleCodexGatewayChatCompletions(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("authorized invalid JSON status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func newCodexGatewayHTTPTest(t *testing.T) (*Server, *storage.Store, string) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	service := codexgateway.NewService(store, nil)
	if err := service.Ensure(ctx); err != nil {
		t.Fatalf("ensure gateway: %v", err)
	}
	_, token, err := service.CreateAPIKey(ctx, "test")
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}
	return &Server{store: store, codexGateway: service}, store, token
}

func setCodexGatewayEnabled(t *testing.T, store *storage.Store, enabled bool) {
	t.Helper()
	ctx := context.Background()
	settings, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	settings.Enabled = enabled
	if _, err := store.UpdateCodexGatewaySettings(ctx, settings); err != nil {
		t.Fatalf("update settings: %v", err)
	}
}
