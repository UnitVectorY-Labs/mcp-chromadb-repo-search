package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authContextKey struct{}
type requestLogContextKey struct{}

// requestLogFields carries tool-specific details from the MCP handler to the
// HTTP middleware that writes the single request-completion event.
type requestLogFields struct {
	Tool                 string `json:"tool,omitempty"`
	Parameters           any    `json:"parameters,omitempty"`
	Outcome              string `json:"outcome,omitempty"`
	Error                string `json:"error,omitempty"`
	ResponseContentBytes int    `json:"response_content_bytes,omitempty"`
	DocumentsFound       *int   `json:"documents_found,omitempty"`
}

type requestLogEvent struct {
	Timestamp  time.Time         `json:"timestamp"`
	Level      string            `json:"level"`
	Event      string            `json:"event"`
	RequestID  string            `json:"request_id"`
	Transport  string            `json:"transport"`
	DurationMS int64             `json:"duration_ms"`
	HTTP       httpLogFields     `json:"http"`
	MCP        *requestLogFields `json:"mcp,omitempty"`
}

type httpLogFields struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	StatusCode    int    `json:"status_code"`
	ResponseBytes int    `json:"response_bytes"`
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	responseBytes int
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.responseBytes += n
	return n, err
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestLogFromContext(ctx context.Context) *requestLogFields {
	fields, _ := ctx.Value(requestLogContextKey{}).(*requestLogFields)
	return fields
}

type searchInput struct {
	Query  string `json:"query" jsonschema:"A focused natural-language search query. Include exact identifiers, filenames, or error text when known; semantic matching also finds related wording."`
	Source string `json:"source,omitempty" jsonschema:"Optional repository scope in owner/repository or owner/repository@branch form. Omit it to search every indexed repository."`
	Path   string `json:"path,omitempty" jsonschema:"Optional repository-relative path pattern. * and ** match across folders and ? matches one character; examples: docs/*, **/*.go, README.md."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum excerpts to return, from 1 through 20. Use the default for most questions; increase only when broader evidence is needed."`
}

func CreateMCPServer(cfg Config, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "mcp-chromadb-repo-search", Version: version}, nil)
	client := NewClient(cfg)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search",
		Title:       "Search Repository Content",
		Description: "Search indexed GitHub repository content by meaning. Use this when you need implementation details, documentation, configuration, symbols, or examples from the indexed codebases. Write one specific natural-language query with the relevant names or error text. Omit source when you do not know which repository contains the answer; set it when the user identifies a repository or branch.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(true),
			ReadOnlyHint:    true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
		query := strings.TrimSpace(input.Query)
		source := strings.TrimSpace(input.Source)
		path := strings.TrimSpace(input.Path)
		limit := input.Limit
		if limit == 0 {
			limit = defaultSearchLimit
		}
		requestLog := requestLogFromContext(ctx)
		if requestLog != nil {
			requestLog.Tool = "search"
			requestLog.Parameters = searchInput{Query: query, Source: source, Path: path, Limit: limit}
		}
		if query == "" {
			message := "query must be non-empty"
			setToolError(requestLog, message)
			return toolError(message), nil, nil
		}
		if limit < 1 || limit > maximumSearchLimit {
			message := fmt.Sprintf("limit must be between 1 and %d", maximumSearchLimit)
			setToolError(requestLog, message)
			return toolError(message), nil, nil
		}
		organization, repository, branch, err := parseSource(source)
		if err != nil {
			setToolError(requestLog, err.Error())
			return toolError(err.Error()), nil, nil
		}
		if _, err := pathDocumentWhere(path); err != nil {
			setToolError(requestLog, err.Error())
			return toolError(err.Error()), nil, nil
		}
		params := SearchParams{Query: query, Limit: limit, Organization: organization, Repository: repository, Branch: branch, Path: path}
		auth, _ := ctx.Value(authContextKey{}).(string)
		matches, err := client.Search(ctx, auth, params)
		if err != nil {
			message := fmt.Sprintf("repository content search failed: %v", err)
			// The client error can include a backend response body. Keep that out
			// of request logs while retaining the size of the tool response.
			if requestLog != nil {
				requestLog.Outcome = "error"
				requestLog.Error = "repository content search failed"
				requestLog.ResponseContentBytes = len(message)
			}
			return toolError(message), nil, nil
		}
		response := renderSearchResults(matches, source)
		if requestLog != nil {
			documentCount := len(matches)
			requestLog.Outcome = "success"
			requestLog.ResponseContentBytes = len(response)
			requestLog.DocumentsFound = &documentCount
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: response}}}, nil, nil
	})
	return srv
}

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
}

func setToolError(requestLog *requestLogFields, message string) {
	if requestLog == nil {
		return
	}
	requestLog.Outcome = "error"
	requestLog.Error = message
	requestLog.ResponseContentBytes = len(message)
}

func boolPtr(value bool) *bool {
	return &value
}

func Serve(srv *mcp.Server, cfg Config) error {
	if cfg.HTTPAddr == "" {
		if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("stdio MCP server terminated: %w", err)
		}
		return nil
	}
	addr := normalizeHTTPAddr(cfg.HTTPAddr)
	if err := http.ListenAndServe(addr, newHTTPHandler(srv)); err != nil {
		return fmt.Errorf("Streamable HTTP server error: %w", err)
	}
	return nil
}

func newHTTPHandler(srv *mcp.Server) http.Handler {
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", withAuthorizationContext(stream))
	return withRequestLogging(mux)
}

func withAuthorizationContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			r = r.WithContext(context.WithValue(r.Context(), authContextKey{}, authorization))
		}
		next.ServeHTTP(w, r)
	})
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		mcpFields := &requestLogFields{}
		r = r.WithContext(context.WithValue(r.Context(), requestLogContextKey{}, mcpFields))
		response := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		statusCode := response.statusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		var mcpLog *requestLogFields
		if mcpFields.Tool != "" {
			mcpLog = mcpFields
		}
		level := "INFO"
		if statusCode >= http.StatusBadRequest || (mcpLog != nil && mcpLog.Outcome == "error") {
			level = "ERROR"
		}
		completed := time.Now()
		event := requestLogEvent{
			Timestamp:  completed.UTC(),
			Level:      level,
			Event:      "mcp.request.completed",
			RequestID:  uuid.NewString(),
			Transport:  "streamable_http",
			DurationMS: completed.Sub(started).Milliseconds(),
			HTTP: httpLogFields{
				Method: r.Method, Path: r.URL.Path, StatusCode: statusCode, ResponseBytes: response.responseBytes,
			},
			MCP: mcpLog,
		}
		if data, err := json.Marshal(event); err == nil {
			log.Print(string(data))
		}
	})
}

func normalizeHTTPAddr(value string) string {
	if _, err := strconv.Atoi(value); err == nil {
		return ":" + value
	}
	if host, port, err := net.SplitHostPort(value); err == nil && host == "" {
		return ":" + port
	}
	return value
}
