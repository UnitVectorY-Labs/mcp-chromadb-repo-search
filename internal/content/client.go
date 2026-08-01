package content

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	cfg  Config
	http *http.Client
}

type Collection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

type queryRequest struct {
	QueryEmbeddings [][]float32 `json:"query_embeddings"`
	NResults        int         `json:"n_results"`
	Where           any         `json:"where,omitempty"`
	WhereDocument   any         `json:"where_document,omitempty"`
	Include         []string    `json:"include"`
}

type queryResponse struct {
	IDs       [][]string         `json:"ids"`
	Documents [][]*string        `json:"documents"`
	Metadatas [][]map[string]any `json:"metadatas"`
	Distances [][]*float64       `json:"distances"`
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
}

func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.RequestTimeout}}
}

func (c *Client) Search(ctx context.Context, authHeader string, params SearchParams) ([]SearchMatch, error) {
	embedding, err := c.embed(ctx, params.Query)
	if err != nil {
		return nil, err
	}
	collection, err := c.collection(ctx, authHeader)
	if err != nil {
		return nil, err
	}

	candidateMultiplier := 3
	if c.rerankingEnabled() {
		candidateMultiplier = c.cfg.RerankCandidateMultiplier
	}
	candidateLimit := params.Limit * candidateMultiplier
	if !c.rerankingEnabled() && candidateLimit > 60 {
		candidateLimit = 60
	}
	if c.rerankingEnabled() && candidateLimit > c.cfg.RerankMaxCandidates {
		candidateLimit = c.cfg.RerankMaxCandidates
	}
	whereDocument, err := pathDocumentWhere(params.Path)
	if err != nil {
		return nil, err
	}
	payload := queryRequest{QueryEmbeddings: [][]float32{embedding}, NResults: candidateLimit, Where: metadataWhere(params), WhereDocument: whereDocument, Include: []string{"documents", "metadatas", "distances"}}
	endpoint := fmt.Sprintf("%s/api/v2/tenants/%s/databases/%s/collections/%s/query", c.cfg.ServerURL, url.PathEscape(c.cfg.Tenant), url.PathEscape(c.cfg.Database), url.PathEscape(collection.ID))
	var response queryResponse
	if err := c.doJSON(ctx, "Chroma query", endpoint, payload, c.chromaAuthorization(authHeader), &response); err != nil {
		return nil, err
	}

	matches := make([]SearchMatch, 0, candidateLimit)
	if len(response.IDs) == 0 {
		return matches, nil
	}
	for index := range response.IDs[0] {
		match := SearchMatch{}
		if len(response.Documents) > 0 && index < len(response.Documents[0]) && response.Documents[0][index] != nil {
			match.Document = *response.Documents[0][index]
		}
		if len(response.Metadatas) > 0 && index < len(response.Metadatas[0]) {
			match.Metadata = response.Metadatas[0][index]
		}
		if len(response.Distances) > 0 && index < len(response.Distances[0]) {
			match.Distance = response.Distances[0][index]
		}
		matches = append(matches, match)
	}
	if c.rerankingEnabled() {
		if err := c.rerank(ctx, params.Query, matches); err != nil {
			return nil, err
		}
	}
	return selectMatches(matches, params.Limit), nil
}

func (c *Client) rerankingEnabled() bool {
	return c.cfg.RerankAPIURL != "" && c.cfg.RerankModel != ""
}

func (c *Client) rerank(ctx context.Context, query string, matches []SearchMatch) error {
	if len(matches) < 2 {
		return nil
	}
	documents := make([]string, len(matches))
	requestBytes := 0
	for index, match := range matches {
		document := rerankDocument(match)
		if c.cfg.RerankMaxDocumentBytes > 0 && len(document) > c.cfg.RerankMaxDocumentBytes {
			return fmt.Errorf("reranking document %d is %d bytes, exceeding rerank-max-document-bytes of %d", index, len(document), c.cfg.RerankMaxDocumentBytes)
		}
		requestBytes += len(document)
		if c.cfg.RerankMaxRequestBytes > 0 && requestBytes > c.cfg.RerankMaxRequestBytes {
			return fmt.Errorf("reranking request is %d bytes, exceeding rerank-max-request-bytes of %d", requestBytes, c.cfg.RerankMaxRequestBytes)
		}
		documents[index] = document
	}
	payload := rerankRequest{Model: c.cfg.RerankModel, Query: query, Documents: documents}
	var response rerankResponse
	if err := c.doJSON(ctx, "reranking request", c.cfg.RerankAPIURL+"/v1/rerank", payload, bearer(c.cfg.RerankAPIKey), &response); err != nil {
		return err
	}
	if len(response.Results) != len(matches) {
		return fmt.Errorf("reranking request returned %d results for %d documents", len(response.Results), len(matches))
	}
	seen := make([]bool, len(matches))
	for _, result := range response.Results {
		if result.Index < 0 || result.Index >= len(matches) || seen[result.Index] {
			return fmt.Errorf("reranking request returned invalid result indices")
		}
		seen[result.Index] = true
		matches[result.Index].rerankScore = result.RelevanceScore
	}
	for _, found := range seen {
		if !found {
			return fmt.Errorf("reranking request did not score every document")
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].rerankScore > matches[j].rerankScore })
	return nil
}

func rerankDocument(match SearchMatch) string {
	return rerankDocumentHeader(match) + "\n\n" + cleanDocument(match.Document)
}

func rerankDocumentHeader(match SearchMatch) string {
	metadata := match.Metadata
	repository := metadataString(metadata, "organization") + "/" + metadataString(metadata, "repository") + "@" + metadataString(metadata, "branch")
	file := metadataString(metadata, "path")
	if startLine := metadataInt(metadata, "start_line"); startLine > 0 {
		file += "#L" + strconv.Itoa(startLine)
		if endLine := metadataInt(metadata, "end_line"); endLine > startLine {
			file += "-L" + strconv.Itoa(endLine)
		}
	}
	return "Repository: " + repository + "\nFile: " + file
}

func (c *Client) embed(ctx context.Context, query string) ([]float32, error) {
	payload := map[string]any{"model": c.cfg.EmbeddingModel, "input": []string{"query: " + query}}
	var response embeddingResponse
	if err := c.doJSON(ctx, "embedding request", c.cfg.EmbeddingAPIURL+"/v1/embeddings", payload, bearer(c.cfg.EmbeddingAPIKey), &response); err != nil {
		return nil, err
	}
	if len(response.Data) != 1 || len(response.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding request returned no embedding")
	}
	return response.Data[0].Embedding, nil
}

func (c *Client) collection(ctx context.Context, authHeader string) (Collection, error) {
	endpoint := fmt.Sprintf("%s/api/v2/tenants/%s/databases/%s/collections/%s", c.cfg.ServerURL, url.PathEscape(c.cfg.Tenant), url.PathEscape(c.cfg.Database), url.PathEscape(c.cfg.CollectionName))
	var collection Collection
	if err := c.doJSON(ctx, "Chroma collection lookup", endpoint, nil, c.chromaAuthorization(authHeader), &collection); err != nil {
		return Collection{}, err
	}
	if collection.ID == "" {
		return Collection{}, fmt.Errorf("Chroma collection lookup returned no collection ID")
	}
	return collection, nil
}

func (c *Client) chromaAuthorization(incoming string) string {
	if c.cfg.BearerToken != "" {
		return bearer(c.cfg.BearerToken)
	}
	return incoming
}

func bearer(token string) string {
	if token == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return token
	}
	return "Bearer " + token
}

func (c *Client) doJSON(ctx context.Context, operation, endpoint string, payload any, authorization string, target any) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("%s: encode request: %w", operation, err)
		}
	}
	for attempt := 1; attempt <= c.cfg.RetryAttempts; attempt++ {
		var reader io.Reader
		method := http.MethodGet
		if payload != nil {
			reader = bytes.NewReader(body)
			method = http.MethodPost
		}
		req, requestErr := http.NewRequestWithContext(ctx, method, endpoint, reader)
		if requestErr != nil {
			return fmt.Errorf("%s: create request: %w", operation, requestErr)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if c.cfg.Debug {
			log.Printf("%s: %s %s (attempt %d)", operation, method, endpoint, attempt)
		}
		resp, requestErr := c.http.Do(req)
		if requestErr != nil {
			if attempt < c.cfg.RetryAttempts && ctx.Err() == nil {
				time.Sleep(retryDelay(attempt))
				continue
			}
			return fmt.Errorf("%s failed: %w", operation, requestErr)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("%s: read response: %w", operation, readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := c.redact(strings.TrimSpace(string(responseBody)))
			if len(message) > 1000 {
				message = message[:1000]
			}
			if attempt < c.cfg.RetryAttempts && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) {
				time.Sleep(retryDelay(attempt))
				continue
			}
			return fmt.Errorf("%s failed with HTTP %d: %s", operation, resp.StatusCode, message)
		}
		if err := json.Unmarshal(responseBody, target); err != nil {
			return fmt.Errorf("%s: decode response: %w", operation, err)
		}
		return nil
	}
	return fmt.Errorf("%s failed", operation)
}

func retryDelay(attempt int) time.Duration {
	delay := 500 * time.Millisecond
	for step := 1; step < attempt && delay < 8*time.Second; step++ {
		delay *= 2
	}
	if delay > 8*time.Second {
		return 8 * time.Second
	}
	return delay
}

func (c *Client) redact(message string) string {
	for _, secret := range []string{c.cfg.BearerToken, c.cfg.EmbeddingAPIKey} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "<redacted>")
		}
	}
	return message
}
