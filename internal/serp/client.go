package serp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"talordata-mcp/internal/auth"
)

const maxBodyBytes = 10 << 20 // 10 MB

const (
	DefaultEndpoint           = "https://serpapi.talordata.net/serp/v1/request"
	DefaultHistoryEndpoint    = "https://api.talordata.com/accounts/v1/serp/mcp/history"
	DefaultStatisticsEndpoint = "https://api.talordata.com/pay_package_view/v1/serp/mcp/statistics"
	DefaultTimeoutMS          = 150000
)

type Client struct {
	endpoint   string
	httpClient *http.Client
}

type Response struct {
	OK      bool              `json:"ok"`
	Status  int               `json:"status"`
	Request map[string]string `json:"request"`
	Text    string            `json:"text,omitempty"`
	Data    any               `json:"data,omitempty"`
}

func NewClient(endpoint string, timeout time.Duration) *Client {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	if timeout <= 0 {
		timeout = DefaultTimeoutMS * time.Millisecond
	}

	return &Client{
		endpoint: strings.TrimSpace(endpoint),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Execute(ctx context.Context, token string, params map[string]any) (*Response, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("missing user token in request context")
	}

	form, requestPayload := buildForm(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Set Origin based on request source (pass through detected origin)
	origin := "mcp"
	if source, ok := auth.SourceFromContext(ctx); ok {
		origin = source
	}
	req.Header.Set("Origin", origin)

	if userAgent, ok := auth.UserAgentFromContext(ctx); ok {
		req.Header.Set("User-Agent", userAgent)
	}
	selectedPlatform := ""
	if platform, ok := auth.AgentPlatformFromContext(ctx); ok {
		selectedPlatform = platform
		req.Header.Set(auth.HeaderAgentPlatform, platform)
	}

	// #region debug-point C:upstream-platform-header
	func() {
		envData, err := os.ReadFile(".dbg/python-platform-miss.env")
		if err != nil {
			return
		}
		debugServerURL := ""
		debugSessionID := ""
		for _, line := range strings.Split(string(envData), "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "DEBUG_SERVER_URL="); ok {
				debugServerURL = strings.TrimSpace(value)
			}
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "DEBUG_SESSION_ID="); ok {
				debugSessionID = strings.TrimSpace(value)
			}
		}
		if debugServerURL == "" || debugSessionID == "" {
			return
		}
		payload, err := json.Marshal(map[string]any{
			"sessionId":    debugSessionID,
			"runId":        "pre-fix",
			"hypothesisId": "C",
			"location":     "internal/serp/client.go:67",
			"msg":          "[DEBUG] upstream request platform header prepared",
			"data": map[string]any{
				"context_platform":  selectedPlatform,
				"outgoing_platform": req.Header.Get(auth.HeaderAgentPlatform),
				"engine":            requestPayload["engine"],
			},
			"ts": time.Now().UnixMilli(),
		})
		if err != nil {
			return
		}
		debugReq, err := http.NewRequest(http.MethodPost, debugServerURL, bytes.NewReader(payload))
		if err != nil {
			return
		}
		debugReq.Header.Set("Content-Type", "application/json")
		go func() {
			resp, err := http.DefaultClient.Do(debugReq)
			if err == nil && resp != nil {
				resp.Body.Close()
			}
		}()
	}()
	// #endregion

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute upstream request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	return buildResponse(resp.StatusCode, requestPayload, body), nil
}

func (c *Client) ExecuteGET(ctx context.Context, endpoint string, token string, params map[string]any, extraHeaders map[string]string) (*Response, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("missing user token in request context")
	}

	query, requestPayload := buildForm(params)
	requestURL := strings.TrimSpace(endpoint)
	if requestURL == "" {
		return nil, fmt.Errorf("missing GET endpoint")
	}

	if encoded := query.Encode(); encoded != "" {
		if strings.Contains(requestURL, "?") {
			requestURL += "&" + encoded
		} else {
			requestURL += "?" + encoded
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build upstream GET request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Set Origin based on request source (pass through detected origin)
	origin := "mcp"
	if source, ok := auth.SourceFromContext(ctx); ok {
		origin = source
	}
	req.Header.Set("Origin", origin)

	if userAgent, ok := auth.UserAgentFromContext(ctx); ok {
		req.Header.Set("User-Agent", userAgent)
	}
	if platform, ok := auth.AgentPlatformFromContext(ctx); ok {
		req.Header.Set(auth.HeaderAgentPlatform, platform)
	}
	for key, value := range extraHeaders {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute upstream GET request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read upstream GET response: %w", err)
	}

	return buildResponse(resp.StatusCode, requestPayload, body), nil
}

func buildResponse(statusCode int, requestPayload map[string]string, body []byte) *Response {
	result := &Response{
		OK:      statusCode >= 200 && statusCode < 300,
		Status:  statusCode,
		Request: requestPayload,
		Text:    string(body),
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		result.Data = parsed
	}

	return result
}

func buildForm(params map[string]any) (url.Values, map[string]string) {
	form := make(url.Values, len(params))
	requestPayload := make(map[string]string, len(params))

	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := params[key]
		text := stringifyRequestValue(value)
		if text == "" {
			continue
		}
		form.Set(key, text)
		requestPayload[key] = text
	}

	return form, requestPayload
}

func stringifyRequestValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case []string:
		return strings.Join(typed, ",")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			text := stringifyRequestValue(item)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ",")
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}
