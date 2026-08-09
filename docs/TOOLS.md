---
layout: default
title: MCP Tool Reference
nav_order: 4
permalink: /tools
---

# MCP Tool Reference
{: .no_toc }

## Table of Contents
{: .no_toc .text-delta }

- TOC
{:toc}

---

## `search`

Search GitHub repository content indexed by [chromadb-repo-indexer](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer) in the configured ChromaDB collection.

The tool is read-only, idempotent, and open-world because it reads externally managed services. MCP clients may qualify its name with the configured server name. For example, a client configured as `mcp-chromadb-repo-search` may expose it as `mcp_chromadb_repo_search_search`.

### Request Parameters

| Parameter | Type | Required | Default | Description |
|---|---|---:|---|---|
| `query` | string | yes | — | Focused natural-language query. Include identifiers, filenames, configuration names, or exact error text when known. Whitespace-only values are invalid. |
| `source` | string | no | all indexed repositories | Repository scope in `owner/repository` or `owner/repository@branch` form. Owner and repository are required when this parameter is present. |
| `path` | string | no | all paths | Repository-relative wildcard pattern applied before vector ranking. A leading `/` is ignored. Line breaks are invalid. |
| `limit` | integer | no | `5` | Maximum excerpts returned. Valid values are `1` through `20`; JSON `0` is treated as omitted and uses the default. |

The `source` filter is exact and case-sensitive because it matches indexed metadata. Omitting the branch searches every indexed branch for that owner and repository.

Path patterns support:

| Pattern | Meaning | Example |
|---|---|---|
| `*` | Any number of characters, including `/` | `docs/*` |
| `**/` | Zero or more directories | `**/*.go` |
| `?` | One character | `config?.yaml` |

### Request Example

```json
{
  "query": "Where is the HTTP server address loaded and validated?",
  "source": "example-org/example-repository@main",
  "path": "internal/config/*",
  "limit": 5
}
```

### MCP Response Envelope

A successful tool call returns one text content item. The logical shape is:

```json
{
  "content": [
    {
      "type": "text",
      "text": "Found 1 relevant repository excerpt, ranked by semantic relevance.\n..."
    }
  ]
}
```

The `text` value is Markdown rather than a JSON array of records. This is intentional: the result can be placed directly into a model's context while retaining human-readable attribution.

### Markdown Result Structure

Each result contains:

| Field | Presence | Description |
|---|---|---|
| Summary | always | Number of returned excerpts and a relevance-order statement |
| Result heading | always | One-based result number |
| `Repo` | always for valid indexer records | Indexed `owner/repository@branch` |
| `File` | always for valid indexer records | Repository-relative path and indexed line range when available |
| `Source` | when required metadata is present | GitHub link using the indexed commit SHA, falling back to the indexed branch |
| `Context` | when available | Indexed Markdown section and/or code symbol |
| Code fence | always | Complete stored chunk, with an indexed language identifier when available |

Example:

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

The deterministic source/type prefix stored by the indexer is removed from the visible excerpt because the same information is rendered as structured Markdown around it. Internal record IDs, hashes, schema and chunking versions, Chroma distances, and reranking scores are not returned.

### No-Match Response

No matches are a successful tool call with explanatory text:

```text
No relevant repository content was found. Try a broader or more specific query.
```

When `source` was supplied, the message also suggests omitting it to search all indexed repositories.

### Error Response

Validation and backend failures return one text content item with `isError: true`:

```json
{
  "content": [
    {
      "type": "text",
      "text": "limit must be between 1 and 20"
    }
  ],
  "isError": true
}
```

Common validation errors include:

- `query must be non-empty`
- `limit must be between 1 and 20`
- `source must use owner/repository or owner/repository@branch`
- `source branch must be non-empty after @`
- `path must not contain line breaks`

Backend errors begin with `repository content search failed:` and may identify the embedding request, collection lookup, Chroma query, or reranking request that failed.

## Result Selection Semantics

The server retrieves more candidates than it returns. Without reranking it retrieves three times the requested limit, up to 60 candidates. With reranking, the configured multiplier and maximum apply. After relevance ordering, exact duplicate excerpt content is removed and no more than two excerpts from one file are initially selected. Deferred same-file excerpts fill any remaining result slots.

As a result, the response may contain fewer items than `limit` when the collection has too few matching, non-empty, unique chunks.
