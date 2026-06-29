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
	HeaderOrigin        = "Origin"
)

type contextKey string

const tokenContextKey contextKey = "talor-user-token"
const agentPlatformContextKey contextKey = "talor-agent-platform"
const userAgentContextKey contextKey = "talor-user-agent"
const sourceContextKey contextKey = "talor-source"

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

func WithSource(ctx context.Context, source string) context.Context {
	source = strings.TrimSpace(source)
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, sourceContextKey, source)
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

func SourceFromContext(ctx context.Context) (string, bool) {
	value := ctx.Value(sourceContextKey)
	source, ok := value.(string)
	if !ok {
		return "", false
	}
	source = strings.TrimSpace(source)
	return source, source != ""
}

func ExtractTokenFromRequest(r *http.Request) (string, error) {
	token, _, err := extractTokenFromRequest(r)
	return token, err
}

func extractTokenFromRequest(r *http.Request) (string, string, error) {
	if r == nil {
		return "", "", ErrMissingToken
	}

	if token := strings.TrimSpace(r.Header.Get(HeaderUserToken)); token != "" {
		return token, "x-talor-serp-token", nil
	}
	if token := parseAuthorizationHeader(r.Header.Get(HeaderAuthorization)); token != "" {
		return strings.TrimSpace(token), "authorization", nil
	}
	if token := tokenFromQuery(r); token != "" {
		return token, "query", nil
	}
	if token := tokenFromPath(r.URL.Path); token != "" {
		return token, "path", nil
	}

	return "", "", ErrMissingToken
}

func RequireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}

		token, _, err := extractTokenFromRequest(r)
		if err != nil {
			writeUnauthorized(w, r, "missing bearer token")
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
	// Detect origin from Origin/Referer header
	if origin := DetectOrigin(r); origin != "" {
		ctx = WithSource(ctx, origin)
	}
	return ctx
}

// DetectOrigin extracts the origin value:
// 1. Path identifier from /token/{identifier}/mcp (highest priority)
// 2. HTTP Origin header
// 3. Referer header
func DetectOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}

	// Check path for identifier: /token/{identifier}/mcp
	if id := ExtractPathIdentifier(r.URL.Path); id != "" {
		return id
	}

	// Check Origin header
	origin := strings.TrimSpace(r.Header.Get(HeaderOrigin))
	if origin != "" {
		return origin
	}

	// Fallback to Referer header
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer != "" {
		if idx := strings.Index(referer, "://"); idx > 0 {
			rest := referer[idx+3:]
			if slashIdx := strings.Index(rest, "/"); slashIdx > 0 {
				return rest[:slashIdx]
			}
			return rest
		}
	}

	return ""
}

// ExtractPathIdentifier extracts the platform/instance identifier from the path
// /sk_xxx/si1m/mcp -> "si1m"       (token in path, identifier in middle)
// /sk_xxx/mcp       -> ""          (token in path, no identifier)
// /si1m/mcp         -> "si1m"      (no token in path, identifier as prefix)
// /sm/mcp           -> "sm"        (no token in path, short identifier)
// /mcp              -> ""          (no identifier)
func ExtractPathIdentifier(path string) string {
	path = strings.Trim(cleanPath(path), "/")
	if path == "" {
		return ""
	}

	parts := strings.Split(path, "/")
	// /mcp -> no identifier
	if len(parts) == 1 && parts[0] == "mcp" {
		return ""
	}

	// Must end with "mcp"
	if parts[len(parts)-1] != "mcp" {
		return ""
	}

	firstPart := strings.TrimSpace(parts[0])

	// First part is token: /sk_xxx/si1m/mcp -> "si1m", /sk_xxx/mcp -> ""
	if looksLikeToken(firstPart) {
		return strings.Join(parts[1:len(parts)-1], "/")
	}

	// First part is not token: /si1m/mcp -> "si1m", /sm/mcp -> "sm"
	return strings.Join(parts[:len(parts)-1], "/")
}

// MCPPathType represents the type of MCP path
type MCPPathType int

const (
	MCPPathUnknown   MCPPathType = iota
	MCPPathStandard              // /mcp
	MCPPathPlatform              // /{platform}/mcp
	MCPPathUserToken             // /{token}/mcp
)

// ClassifyMCPPath determines the type of MCP path
func ClassifyMCPPath(path string) MCPPathType {
	path = cleanPath(path)

	if path == "/mcp" {
		return MCPPathStandard
	}

	if !strings.HasSuffix(path, "/mcp") {
		return MCPPathUnknown
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] != "mcp" {
		return MCPPathUnknown
	}

	// Preserve Smithery OAuth protected resource probe routing so it can still
	// reach the MCP handler when the upstream gateway appends /mcp here.
	if len(parts) == 4 &&
		parts[0] == ".well-known" &&
		parts[1] == "oauth-protected-resource" &&
		strings.TrimSpace(parts[2]) != "" {
		return MCPPathPlatform
	}

	prefixParts := parts[:len(parts)-1]
	if len(prefixParts) == 0 || len(prefixParts) > 2 {
		return MCPPathUnknown
	}

	firstPart := strings.TrimSpace(prefixParts[0])
	if firstPart == "" || strings.HasPrefix(firstPart, ".") {
		return MCPPathUnknown
	}

	if len(prefixParts) == 1 {
		// /{token}/mcp
		if looksLikeToken(firstPart) {
			return MCPPathUserToken
		}
		// /{platform}/mcp
		return MCPPathPlatform
	}

	secondPart := strings.TrimSpace(prefixParts[1])
	if secondPart == "" || strings.HasPrefix(secondPart, ".") {
		return MCPPathUnknown
	}

	// /{token}/{platform}/mcp is supported for token-in-path deployments.
	if looksLikeToken(firstPart) {
		return MCPPathUserToken
	}

	return MCPPathUnknown
}

// looksLikeToken checks if a string looks like a user token based on common patterns
// Token patterns: sk_xxx, long alphanumeric strings, etc.
func looksLikeToken(s string) bool {
	s = strings.TrimSpace(s)

	// Empty string is not a token
	if s == "" {
		return false
	}

	// Pattern 1: Starts with common token prefixes (sk_, api_, tok_, etc.)
	tokenPrefixes := []string{"sk_", "api_", "tok_", "key_", "auth_"}
	lower := strings.ToLower(s)
	for _, prefix := range tokenPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Pattern 2: Long string with mixed case and numbers (typical API token)
	// Tokens are usually > 20 chars with letters and numbers
	if len(s) > 20 {
		hasLetter := false
		hasNumber := false
		for _, c := range s {
			if c >= 'a' && c <= 'z' {
				hasLetter = true
			} else if c >= 'A' && c <= 'Z' {
				hasLetter = true
			} else if c >= '0' && c <= '9' {
				hasNumber = true
			}
		}
		// Long string with both letters and numbers is likely a token
		if hasLetter && hasNumber {
			return true
		}
	}

	return false
}

// MCPPath checks if the path is a valid MCP endpoint
func MCPPath(path string) bool {
	return ClassifyMCPPath(path) != MCPPathUnknown
}

// IsMCPPath checks if the path is a valid MCP endpoint (alias for MCPPath)
func IsMCPPath(path string) bool {
	return MCPPath(path)
}

func ValidateContextToken(ctx context.Context) error {
	if token, ok := TokenFromContext(ctx); ok && token != "" {
		return nil
	}
	return ErrMissingToken
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
	// Must end with "mcp"
	if parts[len(parts)-1] != "mcp" {
		return ""
	}

	firstPart := strings.TrimSpace(parts[0])
	if firstPart == "" {
		return ""
	}

	// If it looks like a token, extract it
	if looksLikeToken(firstPart) {
		return firstPart
	}

	return ""
}

func tokenFromQuery(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}

	for _, key := range []string{HeaderUserToken, "token"} {
		if token := strings.TrimSpace(r.URL.Query().Get(key)); token != "" {
			return token
		}
	}

	return ""
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

func writeUnauthorized(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("Content-Type", "application/json")
	if resource := protectedResourceURL(r); resource != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="talordata-mcp", resource="`+resource+`"`)
	}
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func protectedResourceURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.ToLower(strings.Split(forwarded, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}
	path := cleanPath(r.URL.Path)
	return scheme + "://" + host + path
}
