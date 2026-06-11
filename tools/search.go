package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"talordata-mcp/internal/auth"
	"talordata-mcp/internal/engines"
	"talordata-mcp/internal/serp"
)

type ToolSet struct {
	registry           *engines.Registry
	client             *serp.Client
	historyEndpoint    string
	statisticsEndpoint string
}

func New(registry *engines.Registry, client *serp.Client, historyEndpoint string, statisticsEndpoint string) *ToolSet {
	return &ToolSet{
		registry:           registry,
		client:             client,
		historyEndpoint:    historyEndpoint,
		statisticsEndpoint: statisticsEndpoint,
	}
}

func (t *ToolSet) SearchTool() mcp.Tool {
	return mcp.Tool{
		Name:        "search",
		Description: "Execute a Talor SERP request. Use resources under talor://engines to inspect engine-specific parameters before calling this tool.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"engine": map[string]any{
					"type":        "string",
					"description": "Engine key, for example google_web, google_images, bing_images.",
				},
				"q": map[string]any{
					"type":        "string",
					"description": "Search query keyword.",
				},
				"json": map[string]any{
					"type":        "string",
					"description": "Response format: \"1\" = structured JSON data only, \"2\" = JSON + HTML, \"3\" = HTML only. Default is \"1\".",
					"default":     "1",
					"enum":        []string{"1", "2", "3"},
				},
				"params": map[string]any{
					"type":                 "object",
					"description":          "Additional engine-specific parameters. Use resources under talor://engines/{engine} to see all available parameters.",
					"additionalProperties": true,
				},
				"response_mode": map[string]any{
					"type":        "string",
					"description": "complete returns the full response envelope, compact removes common metadata fields when possible.",
					"enum":        []string{"complete", "compact"},
				},
			},
		},
	}
}

func (t *ToolSet) HandleSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := auth.ValidateContextToken(ctx); err != nil {
		return mcp.NewToolResultError("missing user token, provide Authorization: Bearer <token> or call /{token}/mcp"), nil
	}

	engineKey := strings.TrimSpace(request.GetString("engine", ""))
	params := coerceMap(request.GetArguments()["params"])
	if params == nil {
		params = map[string]any{}
	}

	// Inject top-level q and json into params
	if q := strings.TrimSpace(request.GetString("q", "")); q != "" {
		params["q"] = q
	}
	if jsonVal := strings.TrimSpace(request.GetString("json", "")); jsonVal != "" {
		params["json"] = jsonVal
	} else if strings.TrimSpace(toString(params["json"])) == "" {
		params["json"] = "1"
	}

	if engineKey == "" {
		engineKey = strings.TrimSpace(toString(params["engine"]))
	}
	if engineKey == "" {
		engineKey = t.registry.DefaultEngine()
	}

	schema, ok := t.registry.Engine(engineKey)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("unknown engine %q, call list_engines or inspect talor://engines first", engineKey)), nil
	}

	params["engine"] = engineKey
	serialized := serp.Serialize(schema, params)

	userToken, _ := auth.TokenFromContext(ctx)
	upstreamResp, err := t.client.Execute(ctx, userToken, serialized)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	responseMode := serp.NormalizeResponseMode(request.GetString("response_mode", "complete"))
	if responseMode != "complete" && responseMode != "compact" {
		return mcp.NewToolResultError("response_mode must be complete or compact"), nil
	}

	result := map[string]any{
		"ok":      upstreamResp.OK,
		"status":  upstreamResp.Status,
		"engine":  engineKey,
		"request": upstreamResp.Request,
	}

	if upstreamResp.Data != nil {
		data := upstreamResp.Data
		if responseMode == "compact" {
			data = serp.CompactResponseData(data)
		}
		result["data"] = data
	} else {
		result["raw"] = upstreamResp.Text
	}

	body := serp.MarshalPretty(result)
	if !upstreamResp.OK {
		return mcp.NewToolResultError(body), nil
	}

	return mcp.NewToolResultText(body), nil
}

func coerceMap(value any) map[string]any {
	if value == nil {
		return nil
	}

	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = item
		}
		return out
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil
		}
		return out
	}
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}
