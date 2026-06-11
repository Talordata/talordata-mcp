package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const (
	HeaderAuthorization = "Authorization"
	HeaderUserToken     = "X-Talor-Serp-Token"
	HeaderAgentPlatform = "agent-platform"
)

type contextKey string

const tokenContextKey contextKey = "talor-user-token"
const agentPlatformContextKey contextKey = "talor-agent-platform"
const userAgentContextKey contextKey = "talor-user-agent"

var ErrMissingToken = errors.New("missing user token")

func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenContextKey, strings.TrimSpace(token))
}

func WithAgentPlatform(ctx context.Context, platform string) context.Context {
	platform = normalizeAgentPlatform(platform)
	if platform == "" {
		return ctx
	}
	return context.WithValue(ctx, agentPlatformContextKey, platform)
}

func WithUserAgent(ctx context.Context, userAgent string) context.Context {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return ctx
	}
	return context.WithValue(ctx, userAgentContextKey, userAgent)
}

func TokenFromContext(ctx context.Context) (string, bool) {
	value := ctx.Value(tokenContextKey)
	token, ok := value.(string)
	if !ok {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func AgentPlatformFromContext(ctx context.Context) (string, bool) {
	value := ctx.Value(agentPlatformContextKey)
	platform, ok := value.(string)
	if !ok {
		return "", false
	}
	platform = normalizeAgentPlatform(platform)
	return platform, platform != ""
}

func UserAgentFromContext(ctx context.Context) (string, bool) {
	value := ctx.Value(userAgentContextKey)
	userAgent, ok := value.(string)
	if !ok {
		return "", false
	}
	userAgent = strings.TrimSpace(userAgent)
	return userAgent, userAgent != ""
}

func ExtractTokenFromRequest(r *http.Request) (string, error) {
	if r == nil {
		return "", ErrMissingToken
	}

	if token := parseAuthorizationHeader(r.Header.Get(HeaderAuthorization)); token != "" {
		return token, nil
	}
	if token := strings.TrimSpace(r.Header.Get(HeaderUserToken)); token != "" {
		return token, nil
	}
	if token := tokenFromPath(r.URL.Path); token != "" {
		return token, nil
	}

	return "", ErrMissingToken
}

func RequireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}

		token, err := ExtractTokenFromRequest(r)
		if err != nil {
			writeUnauthorized(w, "missing bearer token")
			return
		}

		next.ServeHTTP(w, r.WithContext(WithToken(r.Context(), token)))
	})
}

func HTTPContextFunc(ctx context.Context, r *http.Request) context.Context {
	if token, ok := TokenFromContext(r.Context()); ok {
		ctx = WithToken(ctx, token)
	} else if token, err := ExtractTokenFromRequest(r); err == nil {
		ctx = WithToken(ctx, token)
	}
	if userAgent := strings.TrimSpace(r.Header.Get("User-Agent")); userAgent != "" {
		ctx = WithUserAgent(ctx, userAgent)
	}
	return ctx
}

func ValidateContextToken(ctx context.Context) error {
	if token, ok := TokenFromContext(ctx); ok && token != "" {
		return nil
	}
	return ErrMissingToken
}

func MCPPath(path string) bool {
	path = cleanPath(path)
	if path == "/mcp" {
		return true
	}
	return strings.HasSuffix(path, "/mcp")
}

func HealthPath(path string) bool {
	return cleanPath(path) == "/healthz"
}

func tokenFromPath(path string) string {
	path = strings.Trim(cleanPath(path), "/")
	if path == "" {
		return ""
	}

	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "mcp" {
		return ""
	}

	return strings.TrimSpace(parts[0])
}

func parseAuthorizationHeader(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}

	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func isPublicRoute(r *http.Request) bool {
	if r == nil {
		return false
	}
	path := r.URL.Path
	return path == "/" || HealthPath(path)
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

func normalizeAgentPlatform(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
