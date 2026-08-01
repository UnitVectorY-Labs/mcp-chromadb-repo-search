
# Commands for mcp-chromadb-repo-search
default:
  @just --list
# Build mcp-chromadb-repo-search with Go
build:
  go build ./...

# Run tests for mcp-chromadb-repo-search with Go
test:
  go clean -testcache
  go test ./...