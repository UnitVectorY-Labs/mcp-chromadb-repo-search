package content

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRequestLogging(t *testing.T) {
	backend := http.NewServeMux()
	backend.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{0.1}}}})
	})
	backend.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/repository-content", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Collection{ID: "collection-id", Name: "repository-content"})
	})
	backend.HandleFunc("/api/v2/tenants/tenant/databases/database/collections/collection-id/query", func(w http.ResponseWriter, r *http.Request) {
		document := "Source: Acme/widgets@main:config.go\n\nconfiguration"
		_ = json.NewEncoder(w).Encode(queryResponse{
			IDs:       [][]string{{"record-id"}},
			Documents: [][]*string{{&document}},
			Metadatas: [][]map[string]any{{{"organization": "Acme", "repository": "widgets", "branch": "main", "path": "config.go"}}},
		})
	})
	backendServer := httptest.NewServer(backend)
	defer backendServer.Close()

	var logs bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	}()

	cfg := Config{
		HTTPAddr:        "127.0.0.1:0",
		ServerURL:       backendServer.URL,
		CollectionName:  "repository-content",
		Tenant:          "tenant",
		Database:        "database",
		RetryAttempts:   1,
		EmbeddingAPIURL: backendServer.URL,
		EmbeddingModel:  "model",
		RequestTimeout:  time.Second,
	}
	server := httptest.NewServer(newHTTPHandler(CreateMCPServer(cfg, "test")))
	defer server.Close()

	callMCP(t, server.URL, 1, map[string]any{"name": "search", "arguments": map[string]any{"query": "find configuration", "source": "Acme/widgets@main"}})
	callMCP(t, server.URL, 2, map[string]any{"name": "search", "arguments": map[string]any{"query": ""}})
	response, err := http.Get(server.URL + "/not-found")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("not-found status = %d, want 404", response.StatusCode)
	}

	lines := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte{'\n'})
	if len(lines) != 3 {
		t.Fatalf("log entries = %d, want 3: %s", len(lines), logs.String())
	}
	var success, failure, notFound requestLogEvent
	if err := json.Unmarshal(lines[0], &success); err != nil {
		t.Fatalf("decode success log: %v", err)
	}
	if err := json.Unmarshal(lines[1], &failure); err != nil {
		t.Fatalf("decode failure log: %v", err)
	}
	if err := json.Unmarshal(lines[2], &notFound); err != nil {
		t.Fatalf("decode not-found log: %v", err)
	}
	if success.Event != "mcp.request.completed" || success.Transport != "streamable_http" || success.RequestID == "" || success.Timestamp.IsZero() {
		t.Fatalf("unexpected common success fields: %+v", success)
	}
	if success.HTTP.Method != http.MethodPost || success.HTTP.Path != "/mcp" || success.HTTP.StatusCode != http.StatusOK || success.HTTP.ResponseBytes == 0 {
		t.Fatalf("unexpected HTTP success fields: %+v", success.HTTP)
	}
	if success.MCP == nil || success.MCP.Tool != "search" || success.MCP.Outcome != "success" || success.MCP.DocumentsFound == nil || *success.MCP.DocumentsFound != 1 || success.MCP.ResponseContentBytes == 0 {
		t.Fatalf("unexpected MCP success fields: %+v", success.MCP)
	}
	params, ok := success.MCP.Parameters.(map[string]any)
	if !ok || params["query"] != "find configuration" || params["source"] != "Acme/widgets@main" || params["limit"] != float64(defaultSearchLimit) {
		t.Fatalf("unexpected logged parameters: %#v", success.MCP.Parameters)
	}
	if failure.Level != "ERROR" || failure.MCP == nil || failure.MCP.Outcome != "error" || failure.MCP.Error != "query must be non-empty" || failure.MCP.DocumentsFound != nil {
		t.Fatalf("unexpected failure log: %+v", failure)
	}
	if notFound.Level != "ERROR" || notFound.MCP != nil || notFound.HTTP.StatusCode != http.StatusNotFound || notFound.HTTP.Path != "/not-found" {
		t.Fatalf("unexpected non-MCP request log: %+v", notFound)
	}
}

func callMCP(t *testing.T, serverURL string, id int, params map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}
