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

	// Build full candidate documents (header + body)
	documents := make([]string, len(matches))
	for i, match := range matches {
		documents[i] = rerankDocument(match)
	}

	// Enforce per-document token limits (approximate) by truncating bodies if configured.
	// Token estimate: bytes / 4 (heuristic); this is conservative for typical English text.
	estimateTokens := func(s string) int { return (len(s) + 3) / 4 }
	truncatePreservingHeader := func(doc string, maxBytes int) string {
		// header is the part before the first blank line
		parts := strings.SplitN(doc, "\n\n", 2)
		header := parts[0]
		body := ""
		if len(parts) > 1 {
			body = parts[1]
		}
		allowed := maxBytes - (len(header) + 2) // account for the two newlines
		if allowed <= 0 {
			// can't include body at all
			return header + "\n\n"
		}
		if len(body) <= allowed {
			return header + "\n\n" + body
		}
		// Trim body to allowed bytes, try to cut at a newline boundary for nicer text
		trunc := body[:allowed]
		if idx := strings.LastIndex(trunc, "\n"); idx > int(0.7*float32(len(trunc))) {
			trunc = trunc[:idx]
		}
		return header + "\n\n" + trunc
	}

	// Apply per-document bytes and token truncation, and enforce configured max-document-bytes
	for idx, doc := range documents {
		if c.cfg.RerankMaxDocumentTokens > 0 {
			maxBytes := c.cfg.RerankMaxDocumentTokens * 4
			if estimateTokens(doc) > c.cfg.RerankMaxDocumentTokens {
				doc = truncatePreservingHeader(doc, maxBytes)
				documents[idx] = doc
			}
		}
		if c.cfg.RerankMaxDocumentBytes > 0 && len(doc) > c.cfg.RerankMaxDocumentBytes {
			// bytes limit enforced after token truncation
			return fmt.Errorf("reranking document %d is %d bytes, exceeding rerank-max-document-bytes of %d", idx, len(doc), c.cfg.RerankMaxDocumentBytes)
		}
	}

	// Enforce total request bytes limit as before
	requestBytes := 0
	for _, doc := range documents {
		requestBytes += len(doc)
		if c.cfg.RerankMaxRequestBytes > 0 && requestBytes > c.cfg.RerankMaxRequestBytes {
			return fmt.Errorf("reranking request is %d bytes, exceeding rerank-max-request-bytes of %d", requestBytes, c.cfg.RerankMaxRequestBytes)
		}
	}

	// If a total token cap is configured, attempt to reduce total estimated tokens by truncating largest docs.
	if c.cfg.RerankMaxRequestTokens > 0 {
		totalTokens := 0
		tokens := make([]int, len(documents))
		for i, doc := range documents {
			t := estimateTokens(doc)
			tokens[i] = t
			totalTokens += t
		}
		if totalTokens > c.cfg.RerankMaxRequestTokens {
			// Greedy truncate: reduce the largest document repeatedly until under limit.
			for loop := 0; loop < 1000 && totalTokens > c.cfg.RerankMaxRequestTokens; loop++ {
				// find largest token doc
				maxIdx := 0
				for i := 1; i < len(tokens); i++ {
					if tokens[i] > tokens[maxIdx] {
						maxIdx = i
					}
				}
				if tokens[maxIdx] <= 1 {
					break
				}
				// compute target reduction
				reduceBy := totalTokens - c.cfg.RerankMaxRequestTokens
				// don't reduce more than half of this document in one pass
				reduce := reduceBy
				maxReduce := tokens[maxIdx] / 2
				if reduce > maxReduce {
					reduce = maxReduce
				}
				if reduce <= 0 {
					reduce = 1
				}
				newTokens := tokens[maxIdx] - reduce
				// translate tokens to bytes budget
				newBytes := newTokens * 4
				documents[maxIdx] = truncatePreservingHeader(documents[maxIdx], newBytes)
				old := tokens[maxIdx]
				tokens[maxIdx] = estimateTokens(documents[maxIdx])
				totalTokens -= old - tokens[maxIdx]
			}
			if totalTokens > c.cfg.RerankMaxRequestTokens {
				// As a fallback, forcefully reduce the candidate set to fit the token budget by trimming the tail.
				trimmed := []string{}
				tTrimTokens := 0
				for i := 0; i < len(documents); i++ {
					if tTrimTokens+tokens[i] > c.cfg.RerankMaxRequestTokens {
						break
					}
					trimmed = append(trimmed, documents[i])
					tTrimTokens += tokens[i]
				}
				documents = trimmed
				// reflect reduced matches as well
				if len(documents) < len(matches) {
					matches = matches[:len(documents)]
				}
			}
		}
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
