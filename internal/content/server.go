package content

import (
	"context"
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
		if query == "" {
			return toolError("query must be non-empty"), nil, nil
		}
		if limit < 1 || limit > maximumSearchLimit {
			return toolError(fmt.Sprintf("limit must be between 1 and %d", maximumSearchLimit)), nil, nil
		}
		organization, repository, branch, err := parseSource(source)
		if err != nil {
			return toolError(err.Error()), nil, nil
		}
		if _, err := pathDocumentWhere(path); err != nil {
			return toolError(err.Error()), nil, nil
		}
		params := SearchParams{Query: query, Limit: limit, Organization: organization, Repository: repository, Branch: branch, Path: path}
		requestID := uuid.NewString()
		started := time.Now()
		if cfg.Debug {
			log.Printf("repository search started: request_id=%s source=%q path=%q limit=%d", requestID, source, path, limit)
		}
		auth, _ := ctx.Value(authContextKey{}).(string)
		matches, err := client.Search(ctx, auth, params)
		if err != nil {
			if cfg.Debug {
				log.Printf("repository search failed: request_id=%s duration=%s error=%v", requestID, time.Since(started), err)
			}
			return toolError(fmt.Sprintf("repository content search failed: %v", err)), nil, nil
		}
		if cfg.Debug {
			log.Printf("repository search completed: request_id=%s duration=%s results=%d", requestID, time.Since(started), len(matches))
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: renderSearchResults(matches, source)}}}, nil, nil
	})
	return srv
}

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
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
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", withAuthorizationContext(stream))
	if cfg.Debug {
		log.Printf("Streamable HTTP endpoint: http://%s/mcp", addr)
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		return fmt.Errorf("Streamable HTTP server error: %w", err)
	}
	return nil
}

func withAuthorizationContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			r = r.WithContext(context.WithValue(r.Context(), authContextKey{}, authorization))
		}
		next.ServeHTTP(w, r)
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
