---
layout: default
title: Usage
nav_order: 3
permalink: /usage
---

# Usage
{: .no_toc }

## Table of Contents
{: .no_toc .text-delta }

- TOC
{:toc}

---

## Configuration Precedence

Configuration is resolved in the following order, from highest to lowest priority:

1. Explicit command-line flags
2. Environment variables
3. An explicitly selected YAML configuration file
4. Built-in defaults

There is no automatically discovered configuration file. Select one with `--config` or `CHROMA_REPO_SEARCH_CONFIG_FILE`.

## Command-Line Flags and Environment Variables

```text
mcp-chromadb-repo-search [flags]
```

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--server-url` | `CHROMA_REPO_SEARCH_SERVER_URL` | required | Full HTTP(S) Chroma server origin, without a path, query, credentials, or fragment |
| `--collection-name` | `CHROMA_REPO_SEARCH_COLLECTION_NAME` | required | Chroma collection containing indexed repository chunks |
| `--bearer-token` | `CHROMA_REPO_SEARCH_BEARER_TOKEN` | empty | Chroma bearer token; may include or omit the `Bearer ` prefix |
| `--tenant` | `CHROMA_REPO_SEARCH_TENANT` | `default_tenant` | Chroma tenant |
| `--database` | `CHROMA_REPO_SEARCH_DATABASE` | `default_database` | Chroma database |
| `--config` | `CHROMA_REPO_SEARCH_CONFIG_FILE` | empty | Path to a version 1, chromadb-repo-indexer-compatible YAML file |
| `--retry-attempts` | `CHROMA_REPO_SEARCH_RETRY_ATTEMPTS` | `3` | Attempts for transient Chroma, embedding, and reranking failures; must be positive |
| `--embedding-api-url` | `CHROMA_REPO_SEARCH_EMBEDDING_API_URL` | required | Full origin for an OpenAI-compatible embeddings API |
| `--embedding-model` | `CHROMA_REPO_SEARCH_EMBEDDING_MODEL` | required | Embedding model name |
| `--embedding-api-key` | `CHROMA_REPO_SEARCH_EMBEDDING_API_KEY` | empty | Optional embeddings API key |
| `--rerank-api-url` | `CHROMA_REPO_SEARCH_RERANK_API_URL` | empty | Full origin for an OpenAI-compatible reranking API |
| `--rerank-model` | `CHROMA_REPO_SEARCH_RERANK_MODEL` | empty | Reranking model name |
| `--rerank-api-key` | `CHROMA_REPO_SEARCH_RERANK_API_KEY` | empty | Optional reranking API key |
| `--rerank-candidate-multiplier` | `CHROMA_REPO_SEARCH_RERANK_CANDIDATE_MULTIPLIER` | `3` | Chroma candidates retrieved per requested result when reranking; minimum `2` |
| `--rerank-max-candidates` | `CHROMA_REPO_SEARCH_RERANK_MAX_CANDIDATES` | `100` | Maximum candidates sent to the reranker; minimum `2` when reranking |
| `--rerank-max-document-bytes` | `CHROMA_REPO_SEARCH_RERANK_MAX_DOCUMENT_BYTES` | `0` | Maximum UTF-8 bytes in one reranking document, including its source header; `0` is unlimited |
| `--rerank-max-request-bytes` | `CHROMA_REPO_SEARCH_RERANK_MAX_REQUEST_BYTES` | `0` | Maximum UTF-8 bytes across reranking request documents; `0` is unlimited |
| `--http` | `MCP_CHROMADB_REPO_SEARCH_HTTP` | empty | Run Streamable HTTP on an address or port; empty uses stdio |
| `--debug` | `MCP_CHROMADB_REPO_SEARCH_DEBUG` | `false` | Enable debug logging |
| `--request-timeout` | `MCP_CHROMADB_REPO_SEARCH_REQUEST_TIMEOUT` | `120s` | Timeout for each backend HTTP request, expressed as a Go duration such as `30s` or `2m` |
| `--help` | — | — | Print command-line usage and exit |
| `--version` | — | `false` | Print version, Go runtime, operating system, and architecture, then exit |

The collection name must be 3–512 characters, begin and end with a letter or number, and contain only letters, numbers, `.`, `_`, or `-`.

## YAML Configuration

The server accepts the same version 1 YAML shape used by **chromadb-repo-indexer**. It reads `chroma`, `sync.retry_attempts`, and `embedding`; indexer-only file selection, chunking, and batch settings may be present but do not affect search behavior.

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

Run with the file explicitly selected:

```bash
mcp-chromadb-repo-search --config /path/to/chromadb-repo-indexer.yml
```

The Chroma bearer token, MCP transport settings, and reranking settings are not read from this YAML schema. Supply them through flags or environment variables. Prefer environment variables or a secret manager over command-line flags and YAML for secrets.

## Search Workflow

For each `search` call, the server:

1. Prefixes the query with `query: ` and requests one vector from the configured `/v1/embeddings` endpoint.
2. Resolves the configured collection through Chroma's v2 API.
3. Applies optional repository, branch, and path filters and retrieves candidates using the query vector. More candidates are retrieved than requested to allow for reranking.
4. Optionally sends the candidates to `/v1/rerank` and sorts them by returned relevance score limiting to the requested `limit`.
5. Removes empty and duplicate excerpts, initially limits each source file to two results to favor file diversity, and fills any remaining slots from deferred same-file matches.
6. Renders up to the requested `limit` as source-attributed Markdown.

Raw Chroma distances and reranker scores are not exposed. Distance scales depend on the collection metric and embedding model, and the returned ordering is the meaningful output.

See [MCP Tool Reference](TOOLS.md) for the complete request and response contract.

## Optional Reranking

Reranking is enabled only when both the API URL and model are configured:

```bash
export CHROMA_REPO_SEARCH_RERANK_API_URL=https://rerank.example.com
export CHROMA_REPO_SEARCH_RERANK_MODEL=your-reranker-model
export CHROMA_REPO_SEARCH_RERANK_API_KEY=your-optional-key
```

The server retrieves `min(limit × candidate multiplier, max candidates)` records. With the defaults, a call with `limit: 10` retrieves and reranks 30 candidates. Each reranking document contains a `Repository:` line, a `File:` line with its indexed line range, and the complete indexed chunk body.

The companion indexer stores chunks rather than entire source files (512 tokens with 64-token overlap by default). Byte limits protect reranking services with smaller request limits. If a configured document or request limit would be exceeded, the search fails explicitly instead of silently truncating content and changing its score.

## Stdio Transport

Stdio is the default and is intended for an MCP client that launches the process:

```bash
mcp-chromadb-repo-search
```

Protocol messages use stdout. With `--debug`, operational diagnostics are written to stderr so they do not corrupt the MCP stream.

## Streamable HTTP Transport

Supply `--http` to expose stateless Streamable HTTP at `/mcp`:

```bash
# Listen on all interfaces at http://localhost:8080/mcp
mcp-chromadb-repo-search --http 8080

# Listen only on localhost
mcp-chromadb-repo-search --http 127.0.0.1:8080
```

In HTTP mode, newline-delimited JSON request-completion events are written to stdout. Each event includes request ID, duration, HTTP status and byte counts, and MCP tool outcome fields when applicable. Secrets and backend response bodies are not included in these logs.

### Authentication Behavior

- When `--bearer-token` is configured, that token is always sent to Chroma.
- When it is empty in Streamable HTTP mode, the incoming `Authorization` header is passed through to Chroma.
- Embedding and reranking services use only their separately configured API keys.

{: .warning }
The server does not validate an incoming HTTP authorization header or otherwise protect `/mcp`. Place an authenticating reverse proxy or equivalent access-control layer in front of remote deployments.

## Retries, Timeouts, and Errors

Connection failures, HTTP `429` responses, and HTTP `5xx` responses are retried with bounded exponential backoff. Authentication, other client errors, invalid responses, and configuration errors fail immediately. Each backend attempt uses the configured request timeout.

Tool validation and backend failures are returned as MCP tool errors. Startup configuration failures are written to stderr and exit with status `1`.
