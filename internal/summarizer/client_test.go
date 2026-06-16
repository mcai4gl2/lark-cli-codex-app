package summarizer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yjwong/lark-cli/internal/summarizer"
)

func TestClient_Summarize_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/summarize" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "abc",
			"object":  "summary",
			"model":   "local-summarizer",
			"summary": "Compressed reply.",
		})
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL, MaxTokens: 64})
	result, err := client.Summarize(context.Background(), "This is a long LLM reply that should be compressed.")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "Compressed reply." {
		t.Errorf("unexpected summary: %q", result)
	}
}

func TestClient_Summarize_emptyText(t *testing.T) {
	client := summarizer.NewClient(summarizer.Config{URL: "http://unused"})
	result, err := client.Summarize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("expected no error for empty input, got %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result for empty input, got %q", result)
	}
}

func TestClient_Summarize_upstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL})
	_, err := client.Summarize(context.Background(), "some text")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestClient_Available_healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL})
	if !client.Available(context.Background()) {
		t.Error("expected Available to return true for healthy server")
	}
}

func TestClient_Available_unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "loading", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL})
	if client.Available(context.Background()) {
		t.Error("expected Available to return false for unhealthy server")
	}
}
