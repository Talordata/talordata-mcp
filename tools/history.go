package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"talordata-mcp/internal/auth"
	"talordata-mcp/internal/serp"
)

func (t *ToolSet) HistoryTool() mcp.Tool {
	return mcp.Tool{
		Name:        "history",
		Description: "Query SERP usage history from /accounts/v1/serp/history.",
		Annotations: mcp.ToolAnnotation{
			Title:        "History",
			ReadOnlyHint: boolPtr(true),
		},
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number, default 1.",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Page size, commonly 20/50/100.",
				},
				"search_query": map[string]any{
					"type":        "string",
					"description": "Filter by search query keyword.",
				},
				"search_engine": map[string]any{
					"type":        "string",
					"description": "Filter by search engine display value used by dashboard history.",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "History status filter.",
					"enum":        []string{"all", "success", "error"},
				},
				"start_time": map[string]any{
					"type":        "integer",
					"description": "Start unix timestamp in seconds.",
				},
				"end_time": map[string]any{
					"type":        "integer",
					"description": "End unix timestamp in seconds.",
				},
				"timezone": map[string]any{
					"type":        "string",
					"description": "Optional timezone header value for accounts API, for example Asia/Shanghai or +08:00.",
				},
			},
		},
		OutputSchema: mcp.ToolOutputSchema{
			Type: "object",
			Properties: map[string]any{
				"ok":      map[string]any{"type": "boolean", "description": "Whether the request succeeded"},
				"status":  map[string]any{"type": "integer", "description": "HTTP status code"},
				"tool":    map[string]any{"type": "string", "description": "Tool name"},
				"request": map[string]any{"type": "object", "description": "Request metadata"},
				"data":    map[string]any{"description": "History data returned by the upstream API"},
				"raw":     map[string]any{"type": "string", "description": "Raw upstream response text when structured data is unavailable"},
			},
		},
	}
}

func (t *ToolSet) HandleHistory(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userToken, err := requireUserToken(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := map[string]any{
		"page":      request.GetInt("page", 1),
		"page_size": request.GetInt("page_size", 20),
		"status":    strings.TrimSpace(request.GetString("status", "all")),
	}
	copyOptionalStringArg(request, params, "search_query")
	copyOptionalStringArg(request, params, "search_engine")
	copyOptionalIntArg(request, params, "start_time")
	copyOptionalIntArg(request, params, "end_time")
	params["api_token_id"] = userToken

	resp, err := t.client.ExecuteGET(ctx, t.historyEndpoint, userToken, params, map[string]string{
		"X-Time-Zone": resolveTimeZoneHeader(request),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return buildGETToolResult("history", resp), nil
}

func requireUserToken(ctx context.Context) (string, error) {
	if err := auth.ValidateContextToken(ctx); err != nil {
		return "", fmt.Errorf("missing user token, provide Authorization: Bearer <token> or call /{token}/mcp")
	}
	userToken, _ := auth.TokenFromContext(ctx)
	return userToken, nil
}

func copyOptionalStringArg(request mcp.CallToolRequest, params map[string]any, key string) {
	value := strings.TrimSpace(request.GetString(key, ""))
	if value != "" {
		params[key] = value
	}
}

func copyOptionalIntArg(request mcp.CallToolRequest, params map[string]any, key string) {
	value := request.GetInt(key, 0)
	if value > 0 {
		params[key] = value
	}
}

func resolveTimeZoneHeader(request mcp.CallToolRequest) string {
	value := strings.TrimSpace(request.GetString("timezone", ""))
	if value == "" {
		return "UTC"
	}
	return value
}

func buildGETToolResult(name string, resp *serp.Response) *mcp.CallToolResult {
	result := map[string]any{
		"ok":      resp.OK,
		"status":  resp.Status,
		"tool":    name,
		"request": resp.Request,
	}
	if resp.Data != nil {
		result["data"] = resp.Data
	} else {
		result["raw"] = resp.Text
	}

	body := serp.MarshalPretty(result)
	if !resp.OK {
		return mcp.NewToolResultError(body)
	}
	return mcp.NewToolResultStructured(result, body)
}
