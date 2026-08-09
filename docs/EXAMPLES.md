---
layout: default
title: Examples
nav_order: 5
permalink: /examples
---

# Examples
{: .no_toc }

## Table of Contents
{: .no_toc .text-delta }

- TOC
{:toc}

---

## Basic Stdio Configuration

Configure a Chroma collection populated by [chromadb-repo-indexer](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer) and an embedding endpoint compatible with the collection:

```bash
export CHROMA_REPO_SEARCH_SERVER_URL=https://chroma.example.com
export CHROMA_REPO_SEARCH_COLLECTION_NAME=repository-content
export CHROMA_REPO_SEARCH_BEARER_TOKEN=your-chroma-token
export CHROMA_REPO_SEARCH_EMBEDDING_API_URL=https://embeddings.example.com
export CHROMA_REPO_SEARCH_EMBEDDING_MODEL=your-embedding-model
export CHROMA_REPO_SEARCH_EMBEDDING_API_KEY=your-embedding-key

mcp-chromadb-repo-search
```

Default Chroma tenant and database values are `default_tenant` and `default_database`.

## Shared Indexer Configuration

The search server can read relevant connection and embedding values from the same version 1 YAML file used by the indexer:

```yaml
version: 1

chroma:
  server_url: https://chroma.example.com
  collection_name: repository-content
  tenant: default_tenant
  database: default_database

files:
  include_paths:
    - "**"
  exclude_paths:
    - "vendor/**"

chunking:
  chunk_size: 512
  chunk_overlap: 64

sync:
  batch_size: 100
  retry_attempts: 3

embedding:
  api_url: https://embeddings.example.com
  model: your-embedding-model
  api_key: ""
```

```bash
export CHROMA_REPO_SEARCH_BEARER_TOKEN=your-chroma-token
mcp-chromadb-repo-search --config /path/to/config.yml
```

File selection and chunking fields are used by the indexer and safely accepted by the search server, but they do not alter query behavior.

## Search Every Indexed Repository

Omit `source` when the relevant repository is unknown:

```json
{
  "query": "Where is exponential retry backoff implemented?"
}
```

## Search One Repository

```json
{
  "query": "How is the application configuration validated?",
  "source": "UnitVectorY-Labs/mcp-chromadb-repo-search",
  "limit": 5
}
```

This searches all indexed branches for that repository.

## Search One Branch

```json
{
  "query": "How does the MCP server select its transport?",
  "source": "UnitVectorY-Labs/mcp-chromadb-repo-search@main"
}
```

## Filter by Path

Search Go files anywhere in the repository:

```json
{
  "query": "How are duplicate search results removed?",
  "source": "UnitVectorY-Labs/mcp-chromadb-repo-search@main",
  "path": "**/*.go",
  "limit": 3
}
```

Search documentation under `docs`:

```json
{
  "query": "What environment variables configure reranking?",
  "source": "UnitVectorY-Labs/mcp-chromadb-repo-search@main",
  "path": "docs/*"
}
```

Search one exact file:

```json
{
  "query": "What is this project for?",
  "path": "README.md"
}
```

## Enable Reranking

```bash
export CHROMA_REPO_SEARCH_RERANK_API_URL=https://rerank.example.com
export CHROMA_REPO_SEARCH_RERANK_MODEL=your-reranker-model
export CHROMA_REPO_SEARCH_RERANK_API_KEY=your-reranking-key
export CHROMA_REPO_SEARCH_RERANK_CANDIDATE_MULTIPLIER=3
export CHROMA_REPO_SEARCH_RERANK_MAX_CANDIDATES=100

mcp-chromadb-repo-search
```

For a search with `limit: 5`, the default settings retrieve up to 15 Chroma candidates, rerank them, and return up to five unique, file-diverse excerpts.

## Streamable HTTP

Start the server on the loopback interface:

```bash
mcp-chromadb-repo-search --http 127.0.0.1:8080
```

Configure a remote-capable MCP client to use:

```text
http://127.0.0.1:8080/mcp
```

If no static Chroma bearer token is configured, an `Authorization` header received at this endpoint is forwarded to Chroma. The endpoint itself must be protected separately for non-local deployments.

## Direct MCP Testing

### List Tools over Stdio

The stdio transport accepts newline-delimited JSON-RPC messages. Listing tools validates server startup without making a backend request:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"manual-test","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | mcp-chromadb-repo-search \
      --server-url https://chroma.example.com \
      --collection-name repository-content \
      --embedding-api-url https://embeddings.example.com \
      --embedding-model your-embedding-model
```

### Call `search` over Stdio

Add this JSON-RPC message after initialization to perform a backend search:

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"how is configuration loaded?","source":"example-org/example-repository@main","path":"internal/config/*","limit":3}}}
```

### Test Streamable HTTP

Send an MCP `initialize` request to `/mcp`, then call `tools/call` according to the MCP Streamable HTTP protocol. This server is stateless, so it does not require a server-maintained MCP session between requests.

## Example Result

````markdown
Found 1 relevant repository excerpt, ranked by semantic relevance.

---

### 1

**Repo:** `example-org/example-repository@main`
**File:** `internal/config/config.go#L20-L32`

Source: [internal/config/config.go](https://github.com/example-org/example-repository/blob/COMMIT/internal/config/config.go#L20-L32)
Context: symbol `Load`

```go
func Load() error {
    // Relevant source excerpt
}
```
````

The source link uses the indexed commit SHA when available, making the result reproducible even after the branch advances.
