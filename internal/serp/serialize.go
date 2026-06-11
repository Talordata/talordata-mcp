package serp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"talordata-mcp/internal/engines"
)

var flightLocationCodePattern = regexp.MustCompile(`^[A-Za-z]{3}$`)

var outputFormatParams = map[string]map[string]any{
	"0":         {"json": "0"},
	"1":         {"json": "1"},
	"2":         {"json": "2"},
	"html":      {"json": "0"},
	"json":      {"json": "1"},
	"json_html": {"json": "2"},
}

func Serialize(schema *engines.EngineSchema, values map[string]any) map[string]any {
	if schema == nil || values == nil {
		return map[string]any{}
	}

	out := cloneMap(values)
	for _, group := range schema.Groups {
		for _, field := range group.Fields {
			value, ok := values[field.Key]
			if !ok {
				continue
			}

			switch field.Type {
			case "date_range":
				if field.RangeKeys == nil {
					continue
				}
				start, end := splitDateRange(value)
				out[field.RangeKeys.Start] = start
				out[field.RangeKeys.End] = end
				delete(out, field.Key)
			case "tags":
				if field.Key == "cr" {
					out[field.Key] = serializeCountryRestrict(value)
				} else {
					out[field.Key] = joinList(value, ",")
				}
			case "switch":
				out[field.Key] = boolString(value)
			case "date":
				out[field.Key] = stringify(value)
			case "time_range":
				out[field.Key] = serializeTimeRange(value)
			case "cascader":
				out[field.Key] = serializeCascader(value)
			case "number":
				if isEmptyValue(value) {
					out[field.Key] = ""
				} else {
					out[field.Key] = stringify(value)
				}
			}
		}
	}

	normalizeEngineParams(schema, out)
	return out
}

func ApplyOutputFormat(payload map[string]any, format string) (map[string]any, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "json"
	}

	params, ok := outputFormatParams[format]
	if !ok {
		return nil, fmt.Errorf("unsupported output format: %s", format)
	}

	out := cloneMap(payload)
	for key, value := range params {
		out[key] = value
	}
	return out, nil
}

func NormalizeResponseMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "complete"
	}
	return mode
}

func CompactResponseData(data any) any {
	root, ok := data.(map[string]any)
	if !ok {
		return data
	}

	out := cloneMap(root)
	for _, key := range []string{
		"search_metadata",
		"search_parameters",
		"search_information",
		"pagination",
		"serpapi_pagination",
	} {
		delete(out, key)
	}
	return out
}

func MarshalPretty(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func normalizeEngineParams(schema *engines.EngineSchema, out map[string]any) {
	if schema == nil || schema.Key != "google_flights" {
		return
	}

	for _, key := range []string{"departure_id", "arrival_id"} {
		if value, ok := out[key]; ok {
			out[key] = normalizeFlightLocationIDs(value)
		}
	}
}

func normalizeFlightLocationIDs(value any) string {
	items := splitList(value)
	for idx, item := range items {
		trimmed := strings.TrimSpace(item)
		if flightLocationCodePattern.MatchString(trimmed) && !strings.HasPrefix(trimmed, "/") {
			items[idx] = strings.ToUpper(trimmed)
		} else {
			items[idx] = trimmed
		}
	}
	return strings.Join(items, ",")
}

func serializeCountryRestrict(value any) string {
	items := splitList(value)
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(trimmed), "country") {
			trimmed = "country" + strings.ToUpper(trimmed)
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "|")
}

func serializeTimeRange(value any) string {
	items := splitList(value)
	if len(items) != 4 {
		return ""
	}

	return fmt.Sprintf(
		"%s%s,%s%s",
		zeroPad(items[0]),
		zeroPad(items[1]),
		zeroPad(items[2]),
		zeroPad(items[3]),
	)
}

func serializeCascader(value any) string {
	items := splitList(value)
	if len(items) == 0 {
		return ""
	}
	return strings.TrimSpace(items[len(items)-1])
}

func splitDateRange(value any) (string, string) {
	items := splitList(value)
	if len(items) == 0 {
		return "", ""
	}
	if len(items) == 1 {
		return strings.TrimSpace(items[0]), ""
	}
	return strings.TrimSpace(items[0]), strings.TrimSpace(items[1])
}

func splitList(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, len(typed))
		copy(out, typed)
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, stringify(item))
		}
		return out
	case string:
		if typed == "" {
			return nil
		}
		return strings.Split(typed, ",")
	default:
		if value == nil {
			return nil
		}
		return []string{stringify(value)}
	}
}

func joinList(value any, separator string) string {
	items := splitList(value)
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, separator)
}

func boolString(value any) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		if normalized == "true" || normalized == "1" || normalized == "yes" || normalized == "on" {
			return "true"
		}
		return "false"
	case float64:
		if typed != 0 {
			return "true"
		}
	case int:
		if typed != 0 {
			return "true"
		}
	}
	return "false"
}

func zeroPad(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		return value[len(value)-2:]
	}
	return "0" + value
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func isEmptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	}
	return false
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
