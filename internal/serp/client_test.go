package serp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"talordata-mcp/internal/auth"
)

func TestExecuteForwardsUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "Cursor/0.45.0" {
			t.Fatalf("User-Agent = %q, want %q", got, "Cursor/0.45.0")
		}
		if got := r.Header.Get(auth.HeaderAgentPlatform); got != "" {
			t.Fatalf("agent-platform = %q, want empty platform", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	ctx := auth.WithUserAgent(context.Background(), "Cursor/0.45.0")

	resp, err := client.Execute(ctx, "token", map[string]any{"engine": "google_web", "q": "golang"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !resp.OK {
		t.Fatalf("Execute() OK = false, want true")
	}
}

func TestExecuteGETForwardsUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "Claude Desktop/1.0" {
			t.Fatalf("User-Agent = %q, want %q", got, "Claude Desktop/1.0")
		}
		if got := r.Header.Get(auth.HeaderAgentPlatform); got != "" {
			t.Fatalf("agent-platform = %q, want empty platform", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(DefaultEndpoint, time.Second)
	ctx := auth.WithUserAgent(context.Background(), "Claude Desktop/1.0")

	resp, err := client.ExecuteGET(ctx, server.URL, "token", map[string]any{"page": 1}, nil)
	if err != nil {
		t.Fatalf("ExecuteGET() error = %v", err)
	}
	if !resp.OK {
		t.Fatalf("ExecuteGET() OK = false, want true")
	}
}
