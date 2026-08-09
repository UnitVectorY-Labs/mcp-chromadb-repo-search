---
layout: default
title: mcp-chromadb-repo-search
nav_order: 1
permalink: /
---

# mcp-chromadb-repo-search

Give MCP clients a semantic search interface to the source code and documentation stored across your GitHub repositories.

**mcp-chromadb-repo-search** is a read-only Model Context Protocol server for repository content replicated into a ChromaDB vector database. It lets an AI assistant ask a focused question, narrow the search to a repository, branch, or path, and receive compact source-attributed excerpts ready to use as retrieval-augmented generation (RAG) context.

## How It Fits Together

Repository indexing and repository search are intentionally separate applications:

1. [**chromadb-repo-indexer**](https://github.com/UnitVectorY-Labs/chromadb-repo-indexer) discovers eligible repository files, divides them into source-aware chunks, creates embeddings, and synchronizes those records to ChromaDB.
2. **mcp-chromadb-repo-search** embeds an MCP client's query with an OpenAI-compatible endpoint and uses that vector to retrieve related chunks from ChromaDB.
3. When configured, an OpenAI-compatible reranking model reevaluates the retrieved candidates using the query and each candidate's repository and file context.
4. The server removes duplicate content, favors evidence from different files, and returns the requested number of Markdown excerpts with links to the indexed GitHub commit.

The embedding service used for queries must be compatible with the embeddings already stored in the ChromaDB collection. Indexing configuration and lifecycle remain the responsibility of **chromadb-repo-indexer**.

## Key Features

- **Semantic repository search** — finds relevant code and documentation even when the query and source use different wording
- **Repository-aware filters** — optionally scopes retrieval by owner, repository, branch, and repository-relative path pattern
- **Optional reranking** — improves candidate ordering with a separately configured OpenAI-compatible reranking model
- **Source-attributed results** — identifies repository, branch, file, line range, section or symbol, and a commit-pinned GitHub link when metadata is available
- **RAG-ready Markdown** — returns concise excerpts designed to be consumed directly by an MCP client or language model
- **Local and remote transports** — supports MCP over stdio and stateless Streamable HTTP for remote clients
- **Deployment flexibility** — works with configurable ChromaDB, embedding, and reranking endpoints rather than assuming a particular provider
