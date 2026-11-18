package skydropx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"skydropx-automator/internal/config"
)

func TestExpiredTokenIsRenewedAndRetried(t *testing.T) {
	var tokens, quotes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":7200}`, tokens.Add(1))
	})
	mux.HandleFunc("POST /api/v1/quotations", func(w http.ResponseWriter, r *http.Request) {
		quotes.Add(1)
		body, _ := io.ReadAll(r.Body)
		// The first token stopped working before its announced expiry.
		if r.Header.Get("Authorization") == "Bearer tok1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.Contains(string(body), "quotation") {
			t.Errorf("the retry must resend the body, got %q", body)
		}
		_, _ = io.WriteString(w, `{"id":"q1"}`)
	})
	mux.HandleFunc("GET /api/v1/quotations/q1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"is_completed":true,"rates":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(config.Config{SkydropxMode: "live", SkydropxBaseURL: srv.URL, SkydropxClientID: "id", SkydropxClientSecret: "secret"})

	id, _, failMsg, err := c.runQuotation(context.Background(), map[string]any{"quotation": map[string]any{}})
	if err != nil || failMsg != "" || id != "q1" {
		t.Fatalf("runQuotation = %q, %q, %v", id, failMsg, err)
	}
	if tokens.Load() != 2 || quotes.Load() != 2 {
		t.Fatalf("tokens=%d quotes=%d, want a new token and one retry", tokens.Load(), quotes.Load())
	}
}
