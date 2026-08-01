[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/licenses/MIT) [![Work In Progress](https://img.shields.io/badge/Status-Work%20In%20Progress-yellow)](https://guide.unitvectorylabs.com/bestpractices/status/#work-in-progress) 

# mcp-chromadb-repo-search

An MCP server for semantically searching GitHub repository content indexed in ChromaDB.

Its partner project is [`chromadb-repo-indexer`](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer) which is a GitHub action for loading the repository content into ChromaDB.

The server is read-only. It embeds a search query with an OpenAI-compatible endpoint, retrieves candidates from Chroma's v2 API, removes duplicate content, and favors evidence from different files. It returns concise, source-attributed Markdown designed to be used directly as RAG context. It supports local MCP over stdio and remote MCP over Streamable HTTP.

## Installation

Build locally:

```bash
go build -o mcp-chromadb-repo-search .
```

Or install from source:

```bash
go install github.com/UnitVectorY-Labs/mcp-chromadb-repo-search@latest
```

## Quick start

The Chroma server URL, collection name, embedding API URL, and embedding model are all required. There are deliberately no defaults for deployment-specific endpoints, collection names, or models.

Run locally over stdio:

```bash
export CHROMA_REPO_SEARCH_SERVER_URL=https://chroma.example.com
export CHROMA_REPO_SEARCH_COLLECTION_NAME=repository-content
export CHROMA_REPO_SEARCH_BEARER_TOKEN=your-chroma-token
export CHROMA_REPO_SEARCH_TENANT=default_tenant
export CHROMA_REPO_SEARCH_DATABASE=default_database
export CHROMA_REPO_SEARCH_EMBEDDING_API_URL=https://embeddings.example.com
export CHROMA_REPO_SEARCH_EMBEDDING_MODEL=your-embedding-model
export CHROMA_REPO_SEARCH_EMBEDDING_API_KEY=your-optional-embedding-key

./mcp-chromadb-repo-search
```

An MCP client configuration can launch it directly:

```json
{
  "mcpServers": {
    "mcp-chromadb-repo-search": {
      "command": "/absolute/path/to/mcp-chromadb-repo-search",
      "env": {
        "CHROMA_REPO_SEARCH_SERVER_URL": "https://chroma.example.com",
        "CHROMA_REPO_SEARCH_COLLECTION_NAME": "repository-content",
        "CHROMA_REPO_SEARCH_BEARER_TOKEN": "your-chroma-token",
        "CHROMA_REPO_SEARCH_TENANT": "default_tenant",
        "CHROMA_REPO_SEARCH_DATABASE": "default_database",
        "CHROMA_REPO_SEARCH_EMBEDDING_API_URL": "https://embeddings.example.com",
        "CHROMA_REPO_SEARCH_EMBEDDING_MODEL": "your-embedding-model",
        "CHROMA_REPO_SEARCH_EMBEDDING_API_KEY": "your-optional-embedding-key"
      }
    }
  }
}
```

## Tool

The server exposes one focused tool, `search`. MCP clients normally qualify it with the configured server name—for example, an MCP client configured with the name `mcp-chromadb-repo-search` exposes it as `mcp_chromadb_repo_search_search`.

Use `search` for implementation details, documentation, configuration, symbols, error messages, or examples in indexed repositories. It searches every indexed repository unless `source` narrows the retrieval scope.

| Argument | Required | Default | Description |
|---|---:|---:|---|
| `query` | yes | — | A focused natural-language query. Include identifiers, filenames, or error text when known. |
| `source` | no | all repositories | Repository scope in `owner/repository` or `owner/repository@branch` form. |
| `path` | no | all paths | Repository-relative wildcard pattern such as `docs/*`, `**/*.go`, or `README.md`. |
| `limit` | no | `5` | Maximum excerpts to return, from 1 through 20. |

The compact interface is deliberate:

- `query` controls semantic ranking.
- `source` is the one high-value deterministic filter for repository RAG. Branch selection is included without adding another parameter.
- `path` narrows retrieval before vector ranking. `*` matches any number of characters, including nested folders; `**/` also matches zero or more folders; and `?` matches one character.
- `limit` controls context size. The server retrieves up to three times as many candidates internally, removes duplicate text, and prefers different source files before filling the requested limit.

Raw Chroma distances are not exposed because their scale depends on the collection metric and embedding model. Ranking is preserved, while misleading cross-model score normalization is avoided. Internal record IDs, hashes, schema versions, and chunking metadata are also omitted.

Example arguments:

```json
{
  "query": "Where is the HTTP server address loaded and validated?",
  "source": "example-org/example-repository@main",
  "path": "internal/config/*",
  "limit": 5
}
```

Results are rendered as directly consumable Markdown:

````markdown
Found 1 relevant repository excerpt, ranked by semantic relevance.

---

### 1. `example-org/example-repository@main:internal/config/config.go#L20-L32`

Source: [internal/config/config.go](https://github.com/example-org/example-repository/blob/COMMIT/internal/config/config.go#L20-L32)
Context: symbol `Load`

```go
func Load() error {
    // Relevant source excerpt
}
```
````

The source link uses the indexed commit SHA when available, making attribution reproducible. The deterministic metadata prefix is removed from the excerpt rather than repeated in the content.

## Configuration

Precedence is command-line flags, environment variables, explicitly selected YAML, then built-in defaults. The `CHROMA_REPO_SEARCH_*` names identify this search server.

### Flags and environment variables

| Flag | Environment variable | Default |
|---|---|---|
| `--server-url` | `CHROMA_REPO_SEARCH_SERVER_URL` | required |
| `--collection-name` | `CHROMA_REPO_SEARCH_COLLECTION_NAME` | required |
| `--bearer-token` | `CHROMA_REPO_SEARCH_BEARER_TOKEN` | empty |
| `--tenant` | `CHROMA_REPO_SEARCH_TENANT` | `default_tenant` |
| `--database` | `CHROMA_REPO_SEARCH_DATABASE` | `default_database` |
| `--config` | `CHROMA_REPO_SEARCH_CONFIG_FILE` | empty |
| `--retry-attempts` | `CHROMA_REPO_SEARCH_RETRY_ATTEMPTS` | `3` |
| `--embedding-api-url` | `CHROMA_REPO_SEARCH_EMBEDDING_API_URL` | required |
| `--embedding-model` | `CHROMA_REPO_SEARCH_EMBEDDING_MODEL` | required |
| `--embedding-api-key` | `CHROMA_REPO_SEARCH_EMBEDDING_API_KEY` | empty |
| `--http` | `MCP_CHROMADB_REPO_SEARCH_HTTP` | empty (stdio) |
| `--debug` | `MCP_CHROMADB_REPO_SEARCH_DEBUG` | `false` |
| `--request-timeout` | `MCP_CHROMADB_REPO_SEARCH_REQUEST_TIMEOUT` | `120s` |

`--version` prints the build and Go runtime version. Tokens may be supplied with or without the `Bearer ` prefix. Secrets are never logged; debug output reports operations and endpoints only.

Debug search logs include a generated request ID, elapsed time, and result count for operational correlation without adding tracing fields to the model-facing result.

### YAML configuration

The server accepts version 1 YAML configuration. It reads settings from `chroma`, `sync.retry_attempts`, and `embedding`.

```yaml
version: 1

chroma:
  server_url: https://chroma.example.com
  collection_name: repository-content
  tenant: default_tenant
  database: default_database

sync:
  batch_size: 100
  retry_attempts: 3

embedding:
  api_url: https://embeddings.example.com
  model: your-embedding-model
  api_key: ""
```

Keep the Chroma bearer token outside YAML and provide it through the environment or a flag.

## Streamable HTTP

Supply `--http` to expose remote MCP at `/mcp`. A bare port binds all interfaces; an explicit address can restrict the listener.

```bash
# http://localhost:8080/mcp, listening on all interfaces
./mcp-chromadb-repo-search --http 8080

# http://127.0.0.1:8080/mcp, listening locally only
./mcp-chromadb-repo-search --http 127.0.0.1:8080
```

When `--bearer-token` is configured, that token is always used for Chroma. When it is empty in Streamable HTTP mode, an incoming `Authorization` header is passed through to Chroma, matching `mcp-graphql-forge` behavior. The server does not itself validate that header; deploy an authenticating reverse proxy if the remote MCP endpoint needs access control.

## Direct MCP testing

The stdio transport accepts newline-delimited JSON-RPC messages. This lists tools without requiring any backend request:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"manual-test","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | ./mcp-chromadb-repo-search \
      --server-url https://chroma.example.com \
      --collection-name repository-content \
      --embedding-api-url https://embeddings.example.com \
      --embedding-model your-embedding-model
```

Add a tool call payload to perform a real search:

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"how is configuration loaded?","source":"example-org/example-repository@main","path":"internal/config/*","limit":3}}}
```

For Streamable HTTP, initialize a session first and preserve the returned `Mcp-Session-Id` header for subsequent requests according to the MCP Streamable HTTP protocol.

## Development

```bash
go test ./...
go build ./...
```

Backend calls retry connection failures, HTTP 429 responses, and HTTP 5xx responses with bounded exponential backoff. Authentication and validation errors fail immediately.
