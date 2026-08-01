package content

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestClientSearch(t *testing.T) {
	var queryPayload queryRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.Header.Get("Authorization"); got != "Bearer chroma-secret" {
			t.Errorf("Chroma authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(Collection{ID: "collection-id", Name: "repository-content"})
	})
	mux.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/collection-id/query", func(w http.ResponseWriter, r *http.Request) {
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
