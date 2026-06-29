package oauthserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	ProtectedResourceMetadataPath = "/.well-known/oauth-protected-resource"
	AuthorizationServerPath       = "/.well-known/oauth-authorization-server"
	OpenIDConfigurationPath       = "/.well-known/openid-configuration"
	RegisterPath                  = "/register"
	AuthorizePath                 = "/authorize"
	TokenPath                     = "/token"
	clientTTL                     = 30 * 24 * time.Hour
	authCodeTTL                   = 5 * time.Minute
	accessTokenTTL                = time.Hour
	refreshTokenTTL               = 30 * 24 * time.Hour
	cleanupInterval               = 10 * time.Minute
)

type Server struct {
	serverName string
	now        func() time.Time

	mu            sync.Mutex
	lastCleanup   time.Time
	clients       map[string]clientRegistration
	authCodes     map[string]authorizationCode
	accessTokens  map[string]issuedToken
	refreshTokens map[string]refreshToken
}

type clientRegistration struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
	ExpiresAt    time.Time
}

type authorizationCode struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Resource      string
	Scope         string
	ExpiresAt     time.Time
}

type issuedToken struct {
	ClientID  string
	ExpiresAt time.Time
}

type refreshToken struct {
	ClientID  string
	ExpiresAt time.Time
}

type registerRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func New(serverName string) *Server {
	return &Server{
		serverName:    serverName,
		now:           time.Now,
		lastCleanup:   time.Now(),
		clients:       make(map[string]clientRegistration),
		authCodes:     make(map[string]authorizationCode),
		accessTokens:  make(map[string]issuedToken),
		refreshTokens: make(map[string]refreshToken),
	}
}

func (s *Server) HandleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	resourcePath := resourcePathFromMetadataRequest(r.URL.Path)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              absoluteURL(r, resourcePath),
		"authorization_servers": []string{absoluteURL(r, "")},
		"scopes_supported":      []string{"mcp"},
		"bearer_methods_supported": []string{
			"header",
		},
	})
}

func (s *Server) HandleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, s.authorizationServerMetadata(r))
}

func (s *Server) HandleOpenIDConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	metadata := s.authorizationServerMetadata(r)
	metadata["subject_types_supported"] = []string{"public"}
	metadata["id_token_signing_alg_values_supported"] = []string{"none"}
	metadata["claims_supported"] = []string{}
	writeJSON(w, http.StatusOK, metadata)
}

func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	var request registerRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid registration payload")
		return
	}
	if len(request.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}

	authMethod := strings.TrimSpace(request.TokenEndpointAuthMethod)
	if authMethod == "" {
		authMethod = "none"
	}
	if authMethod != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only token_endpoint_auth_method=none is supported")
		return
	}

	clientID := randomToken(24)
	s.mu.Lock()
	s.cleanupExpiredLocked()
	s.clients[clientID] = clientRegistration{
		ClientID:     clientID,
		ClientName:   strings.TrimSpace(request.ClientName),
		RedirectURIs: append([]string(nil), request.RedirectURIs...),
		ExpiresAt:    s.now().Add(clientTTL),
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        s.now().Unix(),
		"redirect_uris":              request.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      "mcp",
		"client_name":                strings.TrimSpace(request.ClientName),
	})
}

func (s *Server) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.completeAuthorize(w, r)
	case http.MethodPost:
		s.completeAuthorize(w, r)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form payload")
		return
	}

	switch strings.TrimSpace(r.Form.Get("grant_type")) {
	case "authorization_code":
		s.exchangeAuthorizationCode(w, r)
	case "refresh_token":
		s.exchangeRefreshToken(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

func (s *Server) completeAuthorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form payload", http.StatusBadRequest)
		return
	}
	params, err := s.readAuthorizeParams(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code := randomToken(32)
	s.mu.Lock()
	s.cleanupExpiredLocked()
	s.authCodes[code] = authorizationCode{
		ClientID:      params.ClientID,
		RedirectURI:   params.RedirectURI,
		CodeChallenge: params.CodeChallenge,
		Resource:      params.Resource,
		Scope:         firstNonEmpty(params.Scope, "mcp"),
		ExpiresAt:     s.now().Add(authCodeTTL),
	}
	s.mu.Unlock()

	redirectURI, _ := url.Parse(params.RedirectURI)
	query := redirectURI.Query()
	query.Set("code", code)
	if params.State != "" {
		query.Set("state", params.State)
	}
	query.Set("iss", absoluteURL(r, ""))
	redirectURI.RawQuery = query.Encode()

	http.Redirect(w, r, redirectURI.String(), http.StatusFound)
}

func (s *Server) exchangeAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.Form.Get("code"))
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	redirectURI := strings.TrimSpace(r.Form.Get("redirect_uri"))
	codeVerifier := strings.TrimSpace(r.Form.Get("code_verifier"))

	if code == "" || clientID == "" || redirectURI == "" || codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code, client_id, redirect_uri and code_verifier are required")
		return
	}

	s.mu.Lock()
	s.cleanupExpiredLocked()
	record, ok := s.authCodes[code]
	if ok {
		delete(s.authCodes, code)
	}
	s.mu.Unlock()
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if record.ClientID != clientID || record.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code does not match the client")
		return
	}
	if !verifyCodeChallenge(codeVerifier, record.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match code_challenge")
		return
	}

	s.issueTokenResponse(w, record.ClientID, record.Scope)
}

func (s *Server) exchangeRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshValue := strings.TrimSpace(r.Form.Get("refresh_token"))
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	if refreshValue == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token and client_id are required")
		return
	}

	s.mu.Lock()
	s.cleanupExpiredLocked()
	record, ok := s.refreshTokens[refreshValue]
	s.mu.Unlock()
	if !ok || record.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}

	s.issueTokenResponse(w, record.ClientID, "mcp")
}

func (s *Server) issueTokenResponse(w http.ResponseWriter, clientID string, scope string) {
	accessValue := randomToken(32)
	refreshValue := randomToken(32)

	s.mu.Lock()
	s.cleanupExpiredLocked()
	s.accessTokens[accessValue] = issuedToken{
		ClientID:  clientID,
		ExpiresAt: s.now().Add(accessTokenTTL),
	}
	s.refreshTokens[refreshValue] = refreshToken{
		ClientID:  clientID,
		ExpiresAt: s.now().Add(refreshTokenTTL),
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  accessValue,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": refreshValue,
		"scope":         firstNonEmpty(scope, "mcp"),
	})
}

func (s *Server) authorizationServerMetadata(r *http.Request) map[string]any {
	return map[string]any{
		"issuer":                   absoluteURL(r, ""),
		"authorization_endpoint":   absoluteURL(r, AuthorizePath),
		"token_endpoint":           absoluteURL(r, TokenPath),
		"registration_endpoint":    absoluteURL(r, RegisterPath),
		"response_types_supported": []string{"code"},
		"grant_types_supported":    []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{
			"none",
		},
		"code_challenge_methods_supported": []string{"S256"},
		"scopes_supported":                 []string{"mcp"},
	}
}

type authorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	State               string
	Scope               string
	Resource            string
	CodeChallenge       string
	CodeChallengeMethod string
}

func (s *Server) readAuthorizeParams(r *http.Request) (authorizeParams, error) {
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		values = r.Form
	}

	params := authorizeParams{
		ResponseType:        strings.TrimSpace(values.Get("response_type")),
		ClientID:            strings.TrimSpace(values.Get("client_id")),
		RedirectURI:         strings.TrimSpace(values.Get("redirect_uri")),
		State:               strings.TrimSpace(values.Get("state")),
		Scope:               strings.TrimSpace(values.Get("scope")),
		Resource:            strings.TrimSpace(values.Get("resource")),
		CodeChallenge:       strings.TrimSpace(values.Get("code_challenge")),
		CodeChallengeMethod: strings.TrimSpace(values.Get("code_challenge_method")),
	}

	if params.ResponseType == "" {
		params.ResponseType = "code"
	}
	if params.ResponseType != "code" {
		return authorizeParams{}, fmt.Errorf("unsupported response_type")
	}
	if params.ClientID == "" {
		return authorizeParams{}, fmt.Errorf("client_id is required")
	}
	client, ok := s.lookupClient(params.ClientID)
	if !ok {
		return authorizeParams{}, fmt.Errorf("unknown client_id")
	}
	if params.RedirectURI == "" {
		if len(client.RedirectURIs) != 1 {
			return authorizeParams{}, fmt.Errorf("redirect_uri is required")
		}
		params.RedirectURI = client.RedirectURIs[0]
	}
	if !contains(client.RedirectURIs, params.RedirectURI) {
		return authorizeParams{}, fmt.Errorf("redirect_uri is not registered")
	}
	if params.CodeChallenge == "" || !strings.EqualFold(params.CodeChallengeMethod, "S256") {
		return authorizeParams{}, fmt.Errorf("PKCE with S256 is required")
	}
	if params.Resource == "" {
		params.Resource = "/mcp"
	}
	return params, nil
}

func (s *Server) lookupClient(clientID string) (clientRegistration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredLocked()
	client, ok := s.clients[clientID]
	if ok {
		client.ExpiresAt = s.now().Add(clientTTL)
		s.clients[clientID] = client
	}
	return client, ok
}

func (s *Server) cleanupExpiredLocked() {
	now := s.now()
	if !s.lastCleanup.IsZero() && now.Sub(s.lastCleanup) < cleanupInterval {
		return
	}

	for id, client := range s.clients {
		if now.After(client.ExpiresAt) {
			delete(s.clients, id)
		}
	}
	for code, record := range s.authCodes {
		if now.After(record.ExpiresAt) {
			delete(s.authCodes, code)
		}
	}
	for token, record := range s.accessTokens {
		if now.After(record.ExpiresAt) {
			delete(s.accessTokens, token)
		}
	}
	for token, record := range s.refreshTokens {
		if now.After(record.ExpiresAt) {
			delete(s.refreshTokens, token)
		}
	}

	s.lastCleanup = now
}

func resourcePathFromMetadataRequest(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, ProtectedResourceMetadataPath) {
		return "/mcp"
	}
	resourcePath := strings.TrimPrefix(path, ProtectedResourceMetadataPath)
	resourcePath = strings.TrimSpace(resourcePath)
	if resourcePath == "" {
		return "/mcp"
	}
	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}
	return resourcePath
}

func absoluteURL(r *http.Request, path string) string {
	if r == nil {
		return path
	}
	scheme := "http"
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.ToLower(strings.Split(forwarded, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return path
	}
	if path == "" {
		return scheme + "://" + host
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return scheme + "://" + host + path
}

func verifyCodeChallenge(verifier string, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	actual := base64.RawURLEncoding.EncodeToString(sum[:])
	return actual == challenge
}

func randomToken(size int) string {
	if size <= 0 {
		size = 32
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	if len(methods) > 0 {
		w.Header().Set("Allow", strings.Join(methods, ", "))
	}
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOAuthError(w http.ResponseWriter, status int, code string, description string) {
	writeJSON(w, status, map[string]any{
		"error":             code,
		"error_description": description,
	})
}
