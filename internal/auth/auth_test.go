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

func TestClassifyMCPPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want MCPPathType
	}{
		{name: "standard", path: "/mcp", want: MCPPathStandard},
		{name: "platform", path: "/si1m/mcp", want: MCPPathPlatform},
		{name: "token", path: "/sk_abcdefghijklmnopqrstuvwxyz123456/mcp", want: MCPPathUserToken},
		{name: "token platform", path: "/sk_abcdefghijklmnopqrstuvwxyz123456/si1m/mcp", want: MCPPathUserToken},
		{name: "well known oauth protected resource", path: "/.well-known/oauth-protected-resource/smithery/mcp", want: MCPPathPlatform},
		{name: "too many segments", path: "/a/b/c/mcp", want: MCPPathUnknown},
		{name: "hidden prefix", path: "/.well-known/mcp", want: MCPPathUnknown},
		{name: "wrong suffix", path: "/si1m/tools", want: MCPPathUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyMCPPath(tt.path); got != tt.want {
				t.Fatalf("ClassifyMCPPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractTokenFromRequestSupportsQueryTokens(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "custom query header name",
			url:  "/mcp?X-Talor-Serp-Token=sk_query_custom_123",
			want: "sk_query_custom_123",
		},
		{
			name: "generic token query",
			url:  "/mcp?token=sk_query_generic_456",
			want: "sk_query_generic_456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			got, err := ExtractTokenFromRequest(req)
			if err != nil {
				t.Fatalf("ExtractTokenFromRequest() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ExtractTokenFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTokenFromRequestPrefersHeadersOverQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/mcp?token=sk_query_token_123", nil)
	req.Header.Set(HeaderUserToken, "sk_header_token_456")

	got, err := ExtractTokenFromRequest(req)
	if err != nil {
		t.Fatalf("ExtractTokenFromRequest() error = %v", err)
	}
	if got != "sk_header_token_456" {
		t.Fatalf("ExtractTokenFromRequest() = %q, want header token", got)
	}
}
