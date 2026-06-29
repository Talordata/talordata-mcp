# talordata-mcp

<div align="center">
  <h1>Talor SERP MCP Server</h1>
  <p>
    <strong>Give MCP clients access to Talor SERP search, history, and statistics</strong>
  </p>
  <p>
    <a href="https://smithery.ai/@turnbaibian/talordata1">
      <img src="https://smithery.ai/badge/@turnbaibian/talordata1" alt="Smithery" />
    </a>
  </p>
  <p>
    Built with <code>mark3labs/mcp-go</code>, backed by local engine schemas, and designed for
    streamable HTTP deployment.
  </p>
  <p>
    <a href="#-quick-start">Quick Start</a> •
    <a href="#-features">Features</a> •
    <a href="#-configuration">Configuration</a> •
    <a href="#-tools">Tools</a> •
    <a href="#-resources">Resources</a> •
    <a href="#-development">Development</a>
  </p>
</div>

---

## Overview

`talordata-mcp` is an MCP server for Talor SERP.

It exposes:

- A primary `search` tool for live SERP requests
- Supporting `history` and `statistics` tools
- A `list_engines` tool for discovery
- Engine schema resources under `talor://engines` and `talor://engines/<engine>`

The server reuses the parameter definitions from `engines/*.json` in this repository and follows
the serialization behavior used by the `talor-webui-dashboard` playground.

---

## Quick Start

### Run locally

```bash
git clone https://github.com/Talordata/talordata-mcp
cd talordata-mcp
```

```bash
go mod tidy
go run .
```

By default, configuration is loaded from `configs/config.yaml`.

The sample config in this repository is:

```yaml
root_dir: .
listen_addr: ":8800"
upstream_endpoint: "https://serpapi.talordata.net/serp/v1/request"
history_endpoint: "https://api.talordata.com/accounts/v1/serp/mcp/history"
statistics_endpoint: "https://api.talordata.com/pay_package_view/v1/serp/mcp/statistics"
timeout_ms: 150000
shutdown_timeout_ms: 10000
log_prefix: "[talordata-mcp]"
```

After startup, the server exposes:

- `GET /`
- `GET /healthz`
- `POST | GET | DELETE /mcp`
- `POST | GET | DELETE /{user-token}/mcp`

### Compile only

```bash
go build ./...
```

### Example MCP client config

Recommended remote HTTP MCP setup:

```json
{
  "mcpServers": {
    "talordata": {
      "url": "https://your-domain.com:8800/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_USER_TOKEN"
      }
    }
  }
}
```

For clients that cannot send custom headers:

```json
{
  "mcpServers": {
    "talordata": {
      "url": "https://your-domain.com:8800/YOUR_USER_TOKEN/mcp"
    }
  }
}
```

---

## Features

- Dynamically loads all engine schemas from local `engines/*.json`
- Exposes engine index and raw engine schemas through MCP resources
- Proxies Talor SERP search requests through the `search` tool
- Proxies usage history through the `history` tool
- Proxies usage statistics through the `statistics` tool
- Supports response format control via upstream `json`
- Supports schema-aware parameter serialization rules
- Supports per-request user token forwarding instead of storing a fixed upstream token

### Supported serialization behavior

- `date_range`
- `tags`
- `cr`
- `switch`
- `time_range`
- `cascader`
- `number`
- `google_flights` airport code normalization

### Response format mapping

- `json=1` → structured JSON only
- `json=2` → JSON + HTML
- `json=3` → HTML only

---

## Runtime Model

- Designed for cloud deployment with `streamable-http`
- The server does not persist a shared upstream token
- Each user provides their own Talor SERP token per MCP request
- The server authenticates the incoming request and forwards the user token upstream

---

## Authentication

### User token

Supported token delivery methods:

- Recommended: `Authorization: Bearer <user-token>`
- Compatible: `X-Talor-Serp-Token: <user-token>`
- Compatible: `/{user-token}/mcp`

### Notes

- For `/mcp`, sending the token in headers is recommended
- `/{user-token}/mcp` is useful for clients that cannot customize headers
- Query string token passing is intentionally not supported
- The server does not infer `agent-platform` from inbound requests

---

## Configuration

Service configuration is loaded from `configs/config.yaml`.

### Fields

| Field | Description |
|------|-------------|
| `root_dir` | Project root directory; supports relative paths and must contain `engines/index.json` |
| `listen_addr` | Service listen address |
| `upstream_endpoint` | Talor SERP search endpoint |
| `history_endpoint` | Talor SERP history endpoint |
| `statistics_endpoint` | Talor SERP statistics endpoint |
| `timeout_ms` | Upstream timeout in milliseconds |
| `shutdown_timeout_ms` | Graceful shutdown timeout in milliseconds |
| `log_prefix` | Log prefix used by the service |

---

## Tools

Business tools are implemented under the `tools` directory:

- `tools/search.go`
- `tools/history.go`
- `tools/statistics.go`

### `list_engines`

Returns:

- Default engine
- Category list
- Engine list
- Schema resource URI for each engine

### `search`

Executes a Talor SERP search request.

**Parameters**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `engine` | No | Engine key such as `google_web`, `google_images`, `bing_images` |
| `q` | No | Search query |
| `json` | No | Response format: `1`, `2`, or `3` |
| `params` | No | Engine-specific parameters |
| `response_mode` | No | `complete` or `compact` |

**Recommended flow**

1. Read `talor://engines`
2. Read `talor://engines/<engine>`
3. Build `params` according to the schema
4. Call `search`

### `history`

Queries Talor SERP usage history.

**Parameters**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `page` | No | Page number, default `1` |
| `page_size` | No | Page size, commonly `20`, `50`, `100` |
| `search_query` | No | Search query filter |
| `search_engine` | No | Search engine filter |
| `status` | No | `all`, `success`, or `error` |
| `start_time` | No | Start time in Unix seconds |
| `end_time` | No | End time in Unix seconds |
| `timezone` | No | Forwarded as `X-Time-Zone` |

### `statistics`

Queries Talor SERP usage statistics.

**Parameters**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `start_date` | Yes | Start date in `YYYY-MM-DD` |
| `end_date` | Yes | End date in `YYYY-MM-DD` |
| `engines` | No | Comma-separated string or string array |
| `timezone` | No | Timezone offset such as `+08:00` |

---

## Resources

| Resource | Description |
|----------|-------------|
| `talor://engines` | Engine index |
| `talor://engines/<engine>` | Raw schema loaded from `engines/<engine>.json` |

---

## Parameter Serialization Rules

When building upstream form parameters:

- `engine` must be set to the engine key
- `json` must be included when needed by the upstream endpoint
- `date_range` fields are expanded to `{field}_start` and `{field}_end`
- `tags` values are joined with commas
- `switch` values are serialized as `"true"` / `"false"`
- `cascader` uses the last selected value
- `number` values are serialized as strings
- `time_range` values are formatted as `HHmm,HHmm`

---

## Project Structure

| Path | Responsibility |
|------|----------------|
| `main.go` | Service startup, MCP registration, HTTP routing, graceful shutdown |
| `tools/search.go` | `search` tool definition and handler |
| `tools/history.go` | `history` tool definition and handler |
| `tools/statistics.go` | `statistics` tool definition and handler |
| `internal/auth/auth.go` | Token extraction, auth middleware, MCP context injection |
| `internal/engines/registry.go` | Engine index and schema loading |
| `internal/serp/client.go` | Upstream HTTP requests |
| `internal/serp/serialize.go` | Parameter serialization logic |

---

## Development

### Local checks

```bash
go build ./...
```

### Health endpoints

- `GET /` returns service metadata
- `GET /healthz` returns health status

---

## Notes

- The implementation is inspired by `serpapi/serpapi-mcp`
- The project reuses local engine schema definitions from `engines/*.json`
- Platform identification depends on the inbound `User-Agent` when it matches a known client rule

## 🎁 Get Started for Free

Try TalorData SERP API with **1,000 free searches** and start building AI agents, SEO tools, and search-driven applications today.

- No infrastructure to manage
- Multi-engine search access
- Real-time structured results
- Developer-friendly integration

👉 [Start Free](https://talordata.com/?campaignid=hiy46bmdwF990Hqs&utm_source=Github29&utm_term=Github29)

---

## 🤝 Connect With Us

Have questions or want to collaborate? Reach out through any of the following channels:

- 📧 **Email:** [support@talordata.com](mailto:support@talordata.com)  
- 🌐 **Website:** [https://talordata.com](	https://talordata.com/?campaignid=hiy46bmdwF990Hqs&utm_source=Github29&utm_term=Github29)   
- 📱 **WhatsApp:** [+852 5628 3471](https://wa.me/85256283471)  
- 💼 **LinkedIn:** [TalorData](linkedin.com/company/talordata)

---

> **TalorData empowers developers and AI agents with fast, reliable search-data access through a single multi-engine SERP API.**