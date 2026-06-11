package engines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const ResourcePrefix = "talor://engines"

type Category struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	EngineCount int    `json:"engine_count"`
}

type EngineRef struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Category string `json:"category"`
	File     string `json:"file"`
}

type Index struct {
	DefaultEngine string      `json:"default_engine"`
	Count         int         `json:"count"`
	Categories    []Category  `json:"categories"`
	Engines       []EngineRef `json:"engines"`
}

type Option struct {
	Value    any      `json:"value"`
	Label    string   `json:"label"`
	Children []Option `json:"children,omitempty"`
	Options  []Option `json:"options,omitempty"`
}

type RangeKeys struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type Field struct {
	Key          string     `json:"key"`
	Type         string     `json:"type"`
	Required     bool       `json:"required"`
	Label        string     `json:"label"`
	Help         string     `json:"help"`
	DefaultValue any        `json:"default_value"`
	Options      []Option   `json:"options"`
	RangeKeys    *RangeKeys `json:"range_keys"`
}

type Group struct {
	Key    string  `json:"key"`
	Title  string  `json:"title"`
	Fields []Field `json:"fields"`
}

type EngineSchema struct {
	Key             string   `json:"key"`
	Name            string   `json:"name"`
	QueryField      string   `json:"query_field"`
	Groups          []Group  `json:"groups"`
	Category        Category `json:"category"`
	IsDefaultEngine bool     `json:"is_default_engine"`
}

type Registry struct {
	root       string
	index      Index
	schemas    map[string]*EngineSchema
	rawJSON    map[string]string
	engineKeys []string
}

func LoadRegistry(root string) (*Registry, error) {
	indexPath := filepath.Join(root, "engines", "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("read engine index: %w", err)
	}

	var idx Index
	if err := json.Unmarshal(indexData, &idx); err != nil {
		return nil, fmt.Errorf("parse engine index: %w", err)
	}

	registry := &Registry{
		root:    root,
		index:   idx,
		schemas: make(map[string]*EngineSchema, len(idx.Engines)),
		rawJSON: make(map[string]string, len(idx.Engines)),
	}

	for _, ref := range idx.Engines {
		enginePath := filepath.Join(root, "engines", ref.File)
		data, err := os.ReadFile(enginePath)
		if err != nil {
			return nil, fmt.Errorf("read engine schema %s: %w", ref.Key, err)
		}

		var schema EngineSchema
		if err := json.Unmarshal(data, &schema); err != nil {
			return nil, fmt.Errorf("parse engine schema %s: %w", ref.Key, err)
		}

		if schema.Key == "" {
			schema.Key = ref.Key
		}
		if schema.Name == "" {
			schema.Name = ref.Name
		}
		schema.IsDefaultEngine = schema.Key == idx.DefaultEngine

		registry.schemas[schema.Key] = &schema
		registry.rawJSON[schema.Key] = compactJSON(data)
	}

	keys := make([]string, 0, len(registry.schemas))
	for key := range registry.schemas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	registry.engineKeys = keys

	return registry, nil
}

func (r *Registry) Root() string {
	return r.root
}

func (r *Registry) DefaultEngine() string {
	return r.index.DefaultEngine
}

func (r *Registry) Index() Index {
	return r.index
}

func (r *Registry) Engine(key string) (*EngineSchema, bool) {
	schema, ok := r.schemas[key]
	return schema, ok
}

func (r *Registry) RawJSON(key string) (string, bool) {
	content, ok := r.rawJSON[key]
	return content, ok
}

func (r *Registry) EngineKeys() []string {
	return r.engineKeys
}

func (r *Registry) ResourceURIForEngine(key string) string {
	return ResourcePrefix + "/" + key
}

func (r *Registry) ResourceIndexURI() string {
	return ResourcePrefix
}

func (r *Registry) IndexDocument() map[string]any {
	engineDocs := make([]map[string]any, 0, len(r.index.Engines))
	for _, engine := range r.index.Engines {
		engineDocs = append(engineDocs, map[string]any{
			"key":          engine.Key,
			"name":         engine.Name,
			"category":     engine.Category,
			"resource_uri": r.ResourceURIForEngine(engine.Key),
		})
	}

	return map[string]any{
		"default_engine": r.index.DefaultEngine,
		"count":          len(engineDocs),
		"categories":     r.index.Categories,
		"engines":        engineDocs,
		"resource_uri":   r.ResourceIndexURI(),
	}
}

func compactJSON(data []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}
