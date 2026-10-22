package ai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/config"
)

func TestGoogleCompatibleDoesNotLeakAPIKeyInURLOrError(t *testing.T) {
	const secretKey = "AIzaSyDUMMY_SECRET_KEY_FOR_TEST_999"
	var capturedQuery string
	var capturedHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedHeader = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"halo"}]}}]}`))
	}))
	defer srv.Close()

	client, err := ai.NewLLMClient("google_compatible", secretKey, srv.URL, &config.AIConfig{
		Model:     "gemini-2.5-flash",
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatalf("NewLLMClient error: %v", err)
	}

	_, err = client.Ask(context.Background(), "system", "Player", "hi")
	if err != nil {
		t.Fatalf("Ask error: %v", err)
	}
	if strings.Contains(capturedQuery, secretKey) {
		t.Fatalf("API key leaked in URL query string: %q", capturedQuery)
	}
	if capturedHeader != secretKey {
		t.Fatalf("expected x-goog-api-key header %q, got %q", secretKey, capturedHeader)
	}
}
