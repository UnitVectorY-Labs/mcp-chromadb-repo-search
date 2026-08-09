---
layout: default
title: Installation
nav_order: 2
permalink: /install
---

# Installation
{: .no_toc }

## Table of Contents
{: .no_toc .text-delta }

- TOC
{:toc}

## Prerequisites

- **A populated ChromaDB collection:** use [chromadb-repo-indexer](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer) to index repository content before searching it
- **A compatible query embedding service:** an OpenAI-compatible `/v1/embeddings` endpoint and the model used for the indexed collection
- **ChromaDB connectivity:** the server URL, collection name, tenant, database, and authentication required by the deployment
- **Go:** required only when installing with `go install` or building from source
- **Optional reranking service:** an OpenAI-compatible `/v1/rerank` endpoint if reranking will be enabled

{: .important }
The query embedding model must produce vectors compatible with those stored by **chromadb-repo-indexer**. In practice, configure the same model and embedding endpoint behavior for indexing and searching.

## Installation Methods

### Download a Binary

Download a pre-built binary from the [GitHub Releases](https://github.com/UnitVectorY-Labs/mcp-chromadb-repo-search/releases) page and add it to your `PATH`.

[![GitHub release](https://img.shields.io/github/release/UnitVectorY-Labs/mcp-chromadb-repo-search.svg)](https://github.com/UnitVectorY-Labs/mcp-chromadb-repo-search/releases/latest)

### Install Using Go

```bash
go install github.com/UnitVectorY-Labs/mcp-chromadb-repo-search@latest
```

Ensure the Go binary directory, normally `$(go env GOPATH)/bin`, is on your `PATH`.

### Build from Source

```bash
git clone https://github.com/UnitVectorY-Labs/mcp-chromadb-repo-search.git
cd mcp-chromadb-repo-search
go build -o mcp-chromadb-repo-search .
```

## Prepare Repository Data

This application searches existing records; it does not clone or index repositories. Configure and run [chromadb-repo-indexer](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer) for every repository and branch that should be searchable.

The indexer stores source-aware chunks with metadata such as the repository owner, repository, branch, path, line range, section or symbol, and indexed commit SHA. This MCP server relies on that schema for filtering and source attribution. Refer to the indexer's own documentation for file discovery, chunking, embedding, and synchronization configuration.

## Configure an MCP Client

The default transport is stdio. An MCP client can start the executable and provide its configuration as environment variables:

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
        "CHROMA_REPO_SEARCH_EMBEDDING_API_KEY": "your-embedding-key"
      }
    }
  }
}
```

Only the Chroma server URL, collection name, embedding API URL, and embedding model are always required. Tokens and API keys are optional when their corresponding services do not require authentication.

See [Usage](USAGE.md) for every setting, reranking, YAML configuration, and Streamable HTTP deployment.

## Verify the Installation

```bash
mcp-chromadb-repo-search --version
```

To validate MCP startup without querying a backend, use the `tools/list` example in [Direct MCP Testing](EXAMPLES.md#direct-mcp-testing).
