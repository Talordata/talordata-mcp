package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (t *ToolSet) StatisticsTool() mcp.Tool {
	return mcp.Tool{
		Name:        "statistics",
		Description: "Query SERP statistics from /pay_package_view/v1/serp/statistics.",
		Annotations: mcp.ToolAnnotation{
			Title:        "Statistics",
			ReadOnlyHint: boolPtr(true),
		},
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"start_date": map[string]any{
					"type":        "string",
					"description": "Start date in YYYY-MM-DD.",
				},
				"end_date": map[string]any{
					"type":        "string",
					"description": "End date in YYYY-MM-DD.",
				},
				"engines": map[string]any{
					"description": "Comma-separated engines string or string array. Dashboard sends all or selected values joined by comma.",
					"oneOf": []map[string]any{
						{"type": "string"},
						{"type": "array", "items": map[string]any{"type": "string"}},
					},
				},
				"timezone": map[string]any{
					"type":        "string",
					"description": "Timezone offset like +00:00, +08:00, -05:00.",
				},
			},
			Required: []string{"start_date", "end_date"},
		},
		OutputSchema: mcp.ToolOutputSchema{
			Type: "object",
			Properties: map[string]any{
				"ok":      map[string]any{"type": "boolean", "description": "Whether the request succeeded"},
				"status":  map[string]any{"type": "integer", "description": "HTTP status code"},
				"tool":    map[string]any{"type": "string", "description": "Tool name"},
				"request": map[string]any{"type": "object", "description": "Request metadata"},
				"data":    map[string]any{"description": "Statistics data returned by the upstream API"},
				"raw":     map[string]any{"type": "string", "description": "Raw upstream response text when structured data is unavailable"},
			},
		},
	}
}

func (t *ToolSet) HandleStatistics(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	userToken, err := requireUserToken(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := map[string]any{
		"start_date": strings.TrimSpace(request.GetString("start_date", "")),
		"end_date":   strings.TrimSpace(request.GetString("end_date", "")),
	}
	if params["start_date"] == "" || params["end_date"] == "" {
		return mcp.NewToolResultError("start_date and end_date are required"), nil
	}

	if engines := normalizeEnginesArgument(request.GetArguments()["engines"]); engines != "" {
		params["engines"] = engines
	}
	copyOptionalStringArg(request, params, "timezone")
	params["api_token_id"] = userToken

	resp, err := t.client.ExecuteGET(ctx, t.statisticsEndpoint, userToken, params, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return buildGETToolResult("statistics", resp), nil
}

func normalizeEnginesArgument(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []string:
		return strings.Join(typed, ",")
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprintf("%v", item))
			if text != "" {
				items = append(items, text)
			}
		}
		return strings.Join(items, ",")
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}
