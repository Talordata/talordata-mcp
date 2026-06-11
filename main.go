package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"talordata-mcp/internal/auth"
	"talordata-mcp/internal/engines"
	"talordata-mcp/internal/serp"
	"talordata-mcp/tools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

const (
	serverName              = "talordata-mcp"
	serverVersion           = "0.1.0"
	defaultUpstreamEndpoint = "https://serpapi.talordata.net/serp/v1/request"
)

type config struct {
	RootDir            string
	ListenAddr         string
	Endpoint           string
	HistoryEndpoint    string
	StatisticsEndpoint string
	Timeout            time.Duration
	ShutdownWait       time.Duration
	LogPrefix          string
}

type fileConfig struct {
	RootDir            string `yaml:"root_dir"`
	ListenAddr         string `yaml:"listen_addr"`
	Endpoint           string `yaml:"upstream_endpoint"`
	HistoryEndpoint    string `yaml:"history_endpoint"`
	StatisticsEndpoint string `yaml:"statistics_endpoint"`
	TimeoutMS          int    `yaml:"timeout_ms"`
	ShutdownTimeoutMS  int    `yaml:"shutdown_timeout_ms"`
	LogPrefix          string `yaml:"log_prefix"`
}

type app struct {
	cfg      config
	registry *engines.Registry
	client   *serp.Client
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	registry, err := engines.LoadRegistry(cfg.RootDir)
	if err != nil {
		log.Fatal(err)
	}

	application := &app{
		cfg:      cfg,
		registry: registry,
		client:   serp.NewClient(cfg.Endpoint, cfg.Timeout),
	}
	toolSet := tools.New(registry, application.client, cfg.HistoryEndpoint, cfg.StatisticsEndpoint)

	s := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithRecovery(),
	)

	s.AddTool(application.listEnginesTool(), application.handleListEngines)
	s.AddTool(toolSet.SearchTool(), toolSet.HandleSearch)
	s.AddTool(toolSet.HistoryTool(), toolSet.HandleHistory)
	s.AddTool(toolSet.StatisticsTool(), toolSet.HandleStatistics)

	indexResource := mcp.NewResource(
		engines.ResourcePrefix,
		"Talor SERP engines",
		mcp.WithResourceDescription("Index of supported Talor SERP engines and their schema resource URIs."),
		mcp.WithMIMEType("application/json"),
	)
	s.AddResource(indexResource, application.handleEnginesIndex)

	for _, key := range registry.EngineKeys() {
		schema, ok := registry.Engine(key)
		if !ok {
			continue
		}

		resource := mcp.NewResource(
			registry.ResourceURIForEngine(key),
			fmt.Sprintf("%s schema", key),
			mcp.WithResourceDescription(fmt.Sprintf("Talor SERP engine schema for %s.", schema.Name)),
			mcp.WithMIMEType("application/json"),
		)
		s.AddResource(resource, application.handleSingleEngine(key))
	}

	httpTransport := server.NewStreamableHTTPServer(
		s,
		server.WithStateLess(true),
		server.WithDisableStreaming(true),
		server.WithHTTPContextFunc(auth.HTTPContextFunc),
	)

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: application.routes(httpTransport),
	}

	log.Printf("%s listening on %s with %d engines", cfg.LogPrefix, cfg.ListenAddr, len(registry.EngineKeys()))

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	if err := waitForShutdown(httpServer, cfg.ShutdownWait); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (config, error) {
	rootDir, err := resolveRootDir()
	if err != nil {
		return config{}, err
	}

	raw, err := loadFileConfig(rootDir)
	if err != nil {
		return config{}, err
	}

	configuredRootDir, err := resolveConfiguredRootDir(rootDir, raw.RootDir)
	if err != nil {
		return config{}, err
	}
	if !hasEngineIndex(configuredRootDir) {
		return config{}, fmt.Errorf("configured root_dir does not contain engines/index.json: %s", configuredRootDir)
	}

	timeout := durationFromMS(raw.TimeoutMS, serp.DefaultTimeoutMS)
	shutdownWait := durationFromMS(raw.ShutdownTimeoutMS, 10000)

	return config{
		RootDir:            configuredRootDir,
		ListenAddr:         defaultString(raw.ListenAddr, ":8080"),
		Endpoint:           defaultString(raw.Endpoint, defaultUpstreamEndpoint),
		HistoryEndpoint:    defaultString(raw.HistoryEndpoint, serp.DefaultHistoryEndpoint),
		StatisticsEndpoint: defaultString(raw.StatisticsEndpoint, serp.DefaultStatisticsEndpoint),
		Timeout:            timeout,
		ShutdownWait:       shutdownWait,
		LogPrefix:          defaultString(raw.LogPrefix, "[talordata-mcp]"),
	}, nil
}

func resolveRootDir() (string, error) {
	cwd, err := os.Getwd()
	if err == nil && hasEngineIndex(cwd) {
		return cwd, nil
	}

	executable, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(executable)
		if hasEngineIndex(execDir) {
			return execDir, nil
		}
	}

	return "", fmt.Errorf("unable to locate project root containing engines/index.json")
}

func loadFileConfig(rootDir string) (fileConfig, error) {
	path := filepath.Join(rootDir, "configs", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("read config file %s: %w", path, err)
	}

	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	return cfg, nil
}

func resolveConfiguredRootDir(projectRoot string, configuredRoot string) (string, error) {
	value := strings.TrimSpace(configuredRoot)
	if value == "" {
		return filepath.Clean(projectRoot), nil
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Clean(filepath.Join(projectRoot, value)), nil
}

func durationFromMS(value int, fallbackMS int) time.Duration {
	if value <= 0 {
		value = fallbackMS
	}
	return time.Duration(value) * time.Millisecond
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func hasEngineIndex(root string) bool {
	path := filepath.Join(root, "engines", "index.json")
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (a *app) listEnginesTool() mcp.Tool {
	return mcp.NewTool(
		"list_engines",
		mcp.WithDescription("List supported Talor SERP engines, categories, default engine, and schema resource URIs."),
	)
}

func (a *app) handleListEngines(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	_ = request
	return mcp.NewToolResultText(serp.MarshalPretty(a.registry.IndexDocument())), nil
}

func (a *app) handleEnginesIndex(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	_ = ctx
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      request.Params.URI,
			MIMEType: "application/json",
			Text:     serp.MarshalPretty(a.registry.IndexDocument()),
		},
	}, nil
}

func (a *app) handleSingleEngine(engineKey string) func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		_ = ctx
		content, ok := a.registry.RawJSON(engineKey)
		if !ok {
			return nil, fmt.Errorf("resource not found for engine %s", engineKey)
		}

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      request.Params.URI,
				MIMEType: "application/json",
				Text:     content,
			},
		}, nil
	}
}

func (a *app) routes(mcpHandler http.Handler) http.Handler {
	protectedMCP := auth.RequireToken(mcpHandler)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			a.handleRoot(w, r)
		case auth.HealthPath(r.URL.Path):
			a.handleHealth(w, r)
		case auth.MCPPath(r.URL.Path):
			protectedMCP.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (a *app) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":        serverName,
		"version":     serverVersion,
		"transport":   "streamable-http",
		"mcp_path":    "/mcp",
		"health_path": "/healthz",
		"auth": map[string]any{
			"required": true,
			"accepted": []string{
				"Authorization: Bearer <user-token>",
				"X-Talor-Serp-Token: <user-token>",
				"/{user-token}/mcp",
			},
			"platform_resolution": "agent-platform is not inferred from inbound requests",
		},
		"engine_count": len(a.registry.EngineKeys()),
	})
}

func (a *app) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"name":         serverName,
		"version":      serverVersion,
		"engine_count": len(a.registry.EngineKeys()),
	})
}

func waitForShutdown(httpServer *http.Server, timeout time.Duration) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return httpServer.Shutdown(ctx)
}
