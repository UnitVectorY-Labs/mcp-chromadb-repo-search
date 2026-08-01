package content

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`version: 1
chroma:
  server_url: https://config.example.com
  collection_name: config-collection
  tenant: config-tenant
  database: config-database
sync:
  retry_attempts: 4
embedding:
  api_url: https://config-embed.example.com
  model: config-model
`), 0o600); err != nil {
		t.Fatal(err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := NewFlagSet(fs)
	if err := fs.Parse([]string{"--config", path, "--collection-name", "flag-collection", "--tenant", "flag-tenant"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(flags, []string{
		"CHROMA_REPO_SEARCH_SERVER_URL=https://env.example.com",
		"CHROMA_REPO_SEARCH_COLLECTION_NAME=env-collection",
		"CHROMA_REPO_SEARCH_DATABASE=env-database",
		"MCP_CHROMADB_REPO_SEARCH_HTTP=127.0.0.1:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "https://env.example.com" || cfg.CollectionName != "flag-collection" || cfg.Tenant != "flag-tenant" || cfg.Database != "env-database" {
		t.Fatalf("unexpected precedence result: %+v", cfg)
	}
	if cfg.EmbeddingModel != "config-model" || cfg.HTTPAddr != "127.0.0.1:9000" || cfg.RetryAttempts != 4 {
		t.Fatalf("unexpected companion settings: %+v", cfg)
	}
}

func TestLoadConfigEnablesRerankingFromEnvironment(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := NewFlagSet(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(flags, []string{
		"CHROMA_REPO_SEARCH_SERVER_URL=https://chroma.example.com",
		"CHROMA_REPO_SEARCH_COLLECTION_NAME=repository-content",
		"CHROMA_REPO_SEARCH_EMBEDDING_API_URL=https://embeddings.example.com",
		"CHROMA_REPO_SEARCH_EMBEDDING_MODEL=embedding-model",
		"CHROMA_REPO_SEARCH_RERANK_API_URL=https://rerank.example.com/",
		"CHROMA_REPO_SEARCH_RERANK_MODEL=qwen3-reranker-4b-q6k",
		"CHROMA_REPO_SEARCH_RERANK_CANDIDATE_MULTIPLIER=4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RerankAPIURL != "https://rerank.example.com" || cfg.RerankModel != "qwen3-reranker-4b-q6k" || cfg.RerankCandidateMultiplier != 4 || cfg.RerankMaxCandidates != 100 || cfg.RerankMaxDocumentBytes != 0 || cfg.RerankMaxRequestBytes != 0 {
		t.Fatalf("unexpected reranking config: %+v", cfg)
	}
}

func TestLoadConfigRejectsIncompleteRerankingConfiguration(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := NewFlagSet(fs)
	if err := fs.Parse([]string{"--server-url", "https://chroma.example.com", "--collection-name", "repository-content", "--embedding-api-url", "https://embeddings.example.com", "--embedding-model", "embedding-model", "--rerank-model", "reranker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(flags, nil); err == nil || err.Error() != "rerank-api-url and rerank-model must be configured together" {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadConfigRequiresEmbeddingSettings(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing both", []string{"--server-url", "https://chroma.example.com", "--collection-name", "repository-content"}, "embedding-api-url is required"},
		{"missing model", []string{"--server-url", "https://chroma.example.com", "--collection-name", "repository-content", "--embedding-api-url", "https://embeddings.example.com"}, "embedding-model is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			flags := NewFlagSet(fs)
			if err := fs.Parse(test.args); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(flags, nil); err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidOrigin(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := NewFlagSet(fs)
	if err := fs.Parse([]string{"--server-url", "https://chroma.example.com/api", "--collection-name", "repository-content", "--embedding-api-url", "https://embeddings.example.com", "--embedding-model", "example-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(flags, nil); err == nil {
		t.Fatal("expected invalid server URL to fail")
	}
}
