[![GitHub release](https://img.shields.io/github/release/UnitVectorY-Labs/mcp-chromadb-repo-search.svg)](https://github.com/UnitVectorY-Labs/mcp-chromadb-repo-search/releases/latest) [![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/licenses/MIT) [![Active](https://img.shields.io/badge/Status-Active-green)](https://guide.unitvectorylabs.com/bestpractices/status/#active) 

# mcp-chromadb-repo-search

An MCP server for semantically searching GitHub repository content indexed in ChromaDB.

**mcp-chromadb-repo-search** turns a focused natural-language question into an embedding, queries repository chunks created by [chromadb-repo-indexer](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer), and returns concise Markdown excerpts with repository, file, line, and commit-aware source attribution. An optional reranking model can improve the ordering of retrieved candidates.

The server is read-only and supports local MCP over stdio and remote stateless MCP over Streamable HTTP.

## Documentation

- [Overview](docs/README.md) — purpose, architecture, and key features
- [Installation](docs/INSTALL.md) — prerequisites, installation methods, and MCP client setup
- [Usage](docs/USAGE.md) — flags, environment variables, YAML configuration, transports, and runtime behavior
- [MCP Tool Reference](docs/TOOLS.md) — `search` request parameters, response structure, and errors
- [Examples](docs/EXAMPLES.md) — configurations, MCP calls, filters, reranking, and direct testing

## Quick Start

Install the executable:

```bash
go install github.com/UnitVectorY-Labs/mcp-chromadb-repo-search@latest
```

Configure the required Chroma and embedding settings, then start the stdio server:

```bash
export CHROMA_REPO_SEARCH_SERVER_URL=https://chroma.example.com
export CHROMA_REPO_SEARCH_COLLECTION_NAME=repository-content
export CHROMA_REPO_SEARCH_EMBEDDING_API_URL=https://embeddings.example.com
export CHROMA_REPO_SEARCH_EMBEDDING_MODEL=your-embedding-model

mcp-chromadb-repo-search
```

The Chroma collection must first be populated by [chromadb-repo-indexer](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer), using the same embedding model expected by the collection. See [Installation](docs/INSTALL.md) for a complete setup and [Usage](docs/USAGE.md) for all configuration options.

## Development

```bash
go test ./...
go build ./...
```
