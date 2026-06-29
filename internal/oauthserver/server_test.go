package oauthserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHandleProtectedResourceMetadata(t *testing.T) {
	s := New("talordata-mcp")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/.well-known/oauth-protected-resource/smithery/mcp", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	s.HandleProtectedResourceMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got := payload["resource"]; got != "https://example.com/smithery/mcp" {
		t.Fatalf("resource = %v, want %q", got, "https://example.com/smithery/mcp")
	}
}

func TestAuthorizationCodeFlowReturnsTokens(t *testing.T) {
	s := New("talordata-mcp")

	registerBody := `{"client_name":"smithery","redirect_uris":["https://client.example/callback"],"token_endpoint_auth_method":"none"}`
	registerReq := httptest.NewRequest(http.MethodPost, "http://example.com/register", strings.NewReader(registerBody))
	registerRec := httptest.NewRecorder()
	s.HandleRegister(registerRec, registerReq)
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", registerRec.Code, http.StatusCreated)
	}

	var registerResp map[string]any
	if err := json.NewDecoder(registerRec.Body).Decode(&registerResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	clientID, _ := registerResp["client_id"].(string)
	if clientID == "" {
		t.Fatalf("client_id missing from register response")
	}

	verifier := "test-verifier-value-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"state":                 {"abc123"},
		"scope":                 {"mcp"},
		"resource":              {"/smithery/mcp"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authorizeReq := httptest.NewRequest(http.MethodGet, "http://example.com/authorize?"+query.Encode(), nil)
	authorizeRec := httptest.NewRecorder()
	s.HandleAuthorize(authorizeRec, authorizeReq)
	if authorizeRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d, want %d", authorizeRec.Code, http.StatusFound)
	}

	location := authorizeRec.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse authorize redirect: %v", err)
	}
	code := redirectURL.Query().Get("code")
	if code == "" {
		t.Fatalf("authorization code missing from redirect")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {"https://client.example/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "http://example.com/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	s.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want %d body=%s", tokenRec.Code, http.StatusOK, tokenRec.Body.String())
	}

	var tokenResp map[string]any
	if err := json.NewDecoder(tokenRec.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	accessToken, _ := tokenResp["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("access_token missing from token response")
	}
	refreshToken, _ := tokenResp["refresh_token"].(string)
	if refreshToken == "" {
		t.Fatalf("refresh_token missing from token response")
	}
	if got := tokenResp["token_type"]; got != "Bearer" {
		t.Fatalf("token_type = %v, want %q", got, "Bearer")
	}
}

func TestCleanupExpiredLockedRemovesExpiredRecords(t *testing.T) {
	now := time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC)
	s := New("talordata-mcp")
	s.now = func() time.Time { return now }
	s.lastCleanup = now.Add(-cleanupInterval - time.Minute)

	s.clients["expired-client"] = clientRegistration{
		ClientID:     "expired-client",
		RedirectURIs: []string{"https://client.example/callback"},
		ExpiresAt:    now.Add(-time.Minute),
	}
	s.clients["active-client"] = clientRegistration{
		ClientID:     "active-client",
		RedirectURIs: []string{"https://client.example/callback"},
		ExpiresAt:    now.Add(time.Minute),
	}
	s.authCodes["expired-code"] = authorizationCode{ExpiresAt: now.Add(-time.Minute)}
	s.authCodes["active-code"] = authorizationCode{ExpiresAt: now.Add(time.Minute)}
	s.accessTokens["expired-access"] = issuedToken{ExpiresAt: now.Add(-time.Minute)}
	s.accessTokens["active-access"] = issuedToken{ClientID: "active-client", ExpiresAt: now.Add(time.Minute)}
	s.refreshTokens["expired-refresh"] = refreshToken{ExpiresAt: now.Add(-time.Minute)}
	s.refreshTokens["active-refresh"] = refreshToken{ClientID: "active-client", ExpiresAt: now.Add(time.Minute)}

	s.mu.Lock()
	s.cleanupExpiredLocked()
	s.mu.Unlock()

	if _, ok := s.clients["expired-client"]; ok {
		t.Fatalf("expired client was not cleaned up")
	}
	if _, ok := s.authCodes["expired-code"]; ok {
		t.Fatalf("expired auth code was not cleaned up")
	}
	if _, ok := s.accessTokens["expired-access"]; ok {
		t.Fatalf("expired access token was not cleaned up")
	}
	if _, ok := s.refreshTokens["expired-refresh"]; ok {
		t.Fatalf("expired refresh token was not cleaned up")
	}

	if _, ok := s.clients["active-client"]; !ok {
		t.Fatalf("active client should remain")
	}
	if _, ok := s.authCodes["active-code"]; !ok {
		t.Fatalf("active auth code should remain")
	}
	if _, ok := s.accessTokens["active-access"]; !ok {
		t.Fatalf("active access token should remain")
	}
	if _, ok := s.refreshTokens["active-refresh"]; !ok {
		t.Fatalf("active refresh token should remain")
	}
}
