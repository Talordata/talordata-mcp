package auth

import (
	"net/http/httptest"
	"testing"
)

func TestHTTPContextFuncStoresUserAgent(t *testing.T) {
	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("User-Agent", "Claude Desktop/1.0")

	ctx := HTTPContextFunc(req.Context(), req)
	if _, ok := UserAgentFromContext(ctx); !ok {
		t.Fatalf("UserAgentFromContext() missing user agent")
	}
}

func TestHTTPContextFuncDoesNotInferAgentPlatformFromUserAgent(t *testing.T) {
	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("User-Agent", "Claude Desktop/1.0")

	ctx := HTTPContextFunc(req.Context(), req)
	if got, ok := AgentPlatformFromContext(ctx); ok || got != "" {
		t.Fatalf("AgentPlatformFromContext() = %q, want empty platform", got)
	}
}
