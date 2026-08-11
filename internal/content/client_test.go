package content

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClientSearch(t *testing.T) {
	const userAgent = "mcp-chromadb-repo-search/1.2.3"
	var queryPayload queryRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("embedding User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer embed-secret" {
			t.Errorf("embedding authorization = %q", got)
		}
		var payload struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Model != "model" || !reflect.DeepEqual(payload.Input, []string{"query: find configuration"}) {
			t.Errorf("unexpected embedding payload: %+v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{0.1, 0.2}}}})
	})
	mux.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/repository-content", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("Chroma collection User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer chroma-secret" {
			t.Errorf("Chroma authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(Collection{ID: "collection-id", Name: "repository-content"})
	})
	mux.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/collection-id/query", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("Chroma query User-Agent = %q, want %q", got, userAgent)
		}
		_ = json.NewDecoder(r.Body).Decode(&queryPayload)
		distance := 0.25
		document := "Source: Acme/widgets@main:config.go\n\nconfiguration"
		_ = json.NewEncoder(w).Encode(queryResponse{
			IDs: [][]string{{"record-id"}}, Documents: [][]*string{{&document}},
			Metadatas: [][]map[string]any{{{"organization": "Acme", "repository": "widgets"}}},
			Distances: [][]*float64{{&distance}},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(Config{
		UserAgent: userAgent,
		ServerURL: server.URL, CollectionName: "repository-content", BearerToken: "chroma-secret",
		Tenant: "tenant", Database: "database", RetryAttempts: 1, EmbeddingAPIURL: server.URL,
		EmbeddingModel: "model", EmbeddingAPIKey: "embed-secret", RequestTimeout: time.Second,
	})
	result, err := client.Search(context.Background(), "Bearer ignored", SearchParams{Query: "find configuration", Limit: 5, Organization: "Acme", Repository: "widgets", Branch: "main", Path: "internal/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Distance == nil || *result[0].Distance != 0.25 {
		t.Fatalf("unexpected result: %+v", result)
	}
	expectedWhere := map[string]any{"$and": []any{
		map[string]any{"organization": map[string]any{"$eq": "Acme"}},
		map[string]any{"repository": map[string]any{"$eq": "widgets"}},
		map[string]any{"branch": map[string]any{"$eq": "main"}},
	}}
	if !reflect.DeepEqual(queryPayload.Where, expectedWhere) {
		t.Fatalf("where = %#v", queryPayload.Where)
	}
	if queryPayload.NResults != 15 {
		t.Fatalf("n_results = %d, want 15 candidates", queryPayload.NResults)
	}
	wantDocumentWhere := map[string]any{"$regex": `^Source: [^\n]+:internal/.*\.go(?:\n|$)`}
	if !reflect.DeepEqual(queryPayload.WhereDocument, wantDocumentWhere) {
		t.Fatalf("where_document = %#v, want %#v", queryPayload.WhereDocument, wantDocumentWhere)
	}
	if !reflect.DeepEqual(queryPayload.Include, []string{"documents", "metadatas", "distances"}) {
		t.Fatalf("include = %#v", queryPayload.Include)
	}
}

func TestClientSearchReranksCandidatesBeforeLimiting(t *testing.T) {
	const userAgent = "mcp-chromadb-repo-search/1.2.3"
	var queryPayload queryRequest
	var rerankPayload rerankRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{0.1}}}})
	})
	mux.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/repository-content", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Collection{ID: "collection-id"})
	})
	mux.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/collection-id/query", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&queryPayload)
		documents := make([]*string, 6)
		metadata := make([]map[string]any, 6)
		for index := range documents {
			document := "Source: Acme/widgets@main:file" + string(rune('a'+index)) + ".go\n\ncontent " + string(rune('a'+index))
			documents[index] = &document
			metadata[index] = map[string]any{"organization": "Acme", "repository": "widgets", "branch": "main", "path": "file" + string(rune('a'+index)) + ".go"}
		}
		_ = json.NewEncoder(w).Encode(queryResponse{IDs: [][]string{{"0", "1", "2", "3", "4", "5"}}, Documents: [][]*string{documents}, Metadatas: [][]map[string]any{metadata}})
	})
	mux.HandleFunc("/v1/rerank", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("reranking User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer rerank-secret" {
			t.Errorf("reranking authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&rerankPayload)
		_ = json.NewEncoder(w).Encode(rerankResponse{Results: []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		}{
			{Index: 4, RelevanceScore: 0.99}, {Index: 1, RelevanceScore: 0.80}, {Index: 0, RelevanceScore: 0.5},
			{Index: 2, RelevanceScore: 0.4}, {Index: 3, RelevanceScore: 0.3}, {Index: 5, RelevanceScore: 0.2},
		}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(Config{
		UserAgent: userAgent,
		ServerURL: server.URL, CollectionName: "repository-content", Tenant: "tenant", Database: "database", RetryAttempts: 1,
		EmbeddingAPIURL: server.URL, EmbeddingModel: "embedding-model", RequestTimeout: time.Second,
		RerankAPIURL: server.URL, RerankModel: "reranker", RerankAPIKey: "rerank-secret", RerankCandidateMultiplier: 3,
		RerankMaxCandidates: 100, RerankMaxDocumentBytes: 12000, RerankMaxRequestBytes: 240000,
	})
	result, err := client.Search(context.Background(), "", SearchParams{Query: "gogitup flags", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if queryPayload.NResults != 6 {
		t.Fatalf("n_results = %d, want 6 reranking candidates", queryPayload.NResults)
	}
	if rerankPayload.Model != "reranker" || rerankPayload.Query != "gogitup flags" {
		t.Fatalf("unexpected reranking payload: %+v", rerankPayload)
	}
	if !reflect.DeepEqual(rerankPayload.Documents, []string{
		"Repository: Acme/widgets@main\nFile: filea.go\n\ncontent a",
		"Repository: Acme/widgets@main\nFile: fileb.go\n\ncontent b",
		"Repository: Acme/widgets@main\nFile: filec.go\n\ncontent c",
		"Repository: Acme/widgets@main\nFile: filed.go\n\ncontent d",
		"Repository: Acme/widgets@main\nFile: filee.go\n\ncontent e",
		"Repository: Acme/widgets@main\nFile: filef.go\n\ncontent f",
	}) {
		t.Fatalf("reranking documents = %#v", rerankPayload.Documents)
	}
	if len(result) != 2 || !strings.Contains(result[0].Document, "content e") || !strings.Contains(result[1].Document, "content b") {
		t.Fatalf("reranked results = %+v", result)
	}
}

func TestBearer(t *testing.T) {
	for input, expected := range map[string]string{"": "", "token": "Bearer token", "Bearer token": "Bearer token", "bearer token": "bearer token"} {
		if got := bearer(input); got != expected {
			t.Errorf("bearer(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestChromaAuthorizationPassThrough(t *testing.T) {
	client := NewClient(Config{})
	if got := client.chromaAuthorization("Bearer incoming"); got != "Bearer incoming" {
		t.Fatalf("authorization = %q", got)
	}
	client.cfg.BearerToken = "configured"
	if got := client.chromaAuthorization("Bearer incoming"); got != "Bearer configured" {
		t.Fatalf("configured authorization = %q", got)
	}
}

func TestRerankDocumentIncludesRepositoryAndFileLocation(t *testing.T) {
	document := rerankDocument(SearchMatch{
		Document: "Source: Acme/widgets@main:docs/usage.md\n\ncontent",
		Metadata: map[string]any{
			"organization": "Acme", "repository": "widgets", "branch": "main", "path": "docs/usage.md",
			"start_line": 30, "end_line": 59,
		},
	})
	if document != "Repository: Acme/widgets@main\nFile: docs/usage.md#L30-L59\n\ncontent" {
		t.Fatalf("reranking document = %q", document)
	}
}

func TestRerankRejectsConfiguredLimitWithoutTruncating(t *testing.T) {
	client := NewClient(Config{RerankAPIURL: "https://rerank.example.com", RerankModel: "reranker", RerankMaxDocumentBytes: 20})
	matches := []SearchMatch{
		{Document: "one", Metadata: map[string]any{"organization": "Acme", "repository": "widgets", "branch": "main", "path": "first.go"}},
		{Document: "two", Metadata: map[string]any{"organization": "Acme", "repository": "widgets", "branch": "main", "path": "second.go"}},
	}
	err := client.rerank(context.Background(), "query", matches)
	if err == nil || !strings.Contains(err.Error(), "rerank-max-document-bytes") {
		t.Fatalf("error = %v", err)
	}
}
