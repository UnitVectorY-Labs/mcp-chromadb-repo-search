package content

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var collectionNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,510}[A-Za-z0-9]$`)

type Config struct {
	// UserAgent identifies this application to its ChromaDB, embedding, and
	// reranking backends. It is populated by the executable, not user config.
	UserAgent                 string
	ServerURL                 string
	CollectionName            string
	BearerToken               string
	Tenant                    string
	Database                  string
	RetryAttempts             int
	EmbeddingAPIURL           string
	EmbeddingModel            string
	EmbeddingAPIKey           string
	RerankAPIURL              string
	RerankModel               string
	RerankAPIKey              string
	RerankCandidateMultiplier int
	RerankMaxCandidates       int
	RerankMaxDocumentBytes    int
	RerankMaxRequestBytes     int
	HTTPAddr                  string
	Debug                     bool
	RequestTimeout            time.Duration
}

type FlagValues struct {
	set                       *flag.FlagSet
	ServerURL                 string
	CollectionName            string
	BearerToken               string
	Tenant                    string
	Database                  string
	ConfigFile                string
	RetryAttempts             int
	EmbeddingAPIURL           string
	EmbeddingModel            string
	EmbeddingAPIKey           string
	RerankAPIURL              string
	RerankModel               string
	RerankAPIKey              string
	RerankCandidateMultiplier int
	RerankMaxCandidates       int
	RerankMaxDocumentBytes    int
	RerankMaxRequestBytes     int
	HTTPAddr                  string
	Debug                     bool
	RequestTimeout            time.Duration
	Version                   bool
}

func NewFlagSet(fs *flag.FlagSet) *FlagValues {
	v := &FlagValues{set: fs}
	fs.StringVar(&v.ServerURL, "server-url", "", "full HTTP(S) Chroma server origin")
	fs.StringVar(&v.CollectionName, "collection-name", "", "Chroma collection containing indexed repository content")
	fs.StringVar(&v.BearerToken, "bearer-token", "", "optional Chroma bearer token")
	fs.StringVar(&v.Tenant, "tenant", "", "Chroma tenant")
	fs.StringVar(&v.Database, "database", "", "Chroma database")
	fs.StringVar(&v.ConfigFile, "config", "", "path to a chromadb-repo-indexer-compatible YAML configuration")
	fs.IntVar(&v.RetryAttempts, "retry-attempts", 0, "attempts for transient Chroma and embedding failures")
	fs.StringVar(&v.EmbeddingAPIURL, "embedding-api-url", "", "OpenAI-compatible embeddings API origin")
	fs.StringVar(&v.EmbeddingModel, "embedding-model", "", "embeddings model name")
	fs.StringVar(&v.EmbeddingAPIKey, "embedding-api-key", "", "optional embeddings API key")
	fs.StringVar(&v.RerankAPIURL, "rerank-api-url", "", "OpenAI-compatible reranking API origin; enables reranking with --rerank-model")
	fs.StringVar(&v.RerankModel, "rerank-model", "", "reranking model name; enables reranking with --rerank-api-url")
	fs.StringVar(&v.RerankAPIKey, "rerank-api-key", "", "optional reranking API key")
	fs.IntVar(&v.RerankCandidateMultiplier, "rerank-candidate-multiplier", 0, "number of Chroma candidates per requested result when reranking (minimum 2)")
	fs.IntVar(&v.RerankMaxCandidates, "rerank-max-candidates", 0, "maximum candidates sent to the reranker")
	fs.IntVar(&v.RerankMaxDocumentBytes, "rerank-max-document-bytes", 0, "maximum UTF-8 bytes sent for one reranking document, including its source header")
	fs.IntVar(&v.RerankMaxRequestBytes, "rerank-max-request-bytes", 0, "maximum UTF-8 bytes in the reranking request documents")
	fs.StringVar(&v.HTTPAddr, "http", "", "run Streamable HTTP on an address or port (defaults to stdio)")
	fs.BoolVar(&v.Debug, "debug", false, "enable debug logging to stderr")
	fs.DurationVar(&v.RequestTimeout, "request-timeout", 0, "timeout for each backend request")
	fs.BoolVar(&v.Version, "version", false, "print version information")
	return v
}

func (v *FlagValues) explicitlySet(name string) bool {
	found := false
	v.set.Visit(func(f *flag.Flag) { found = found || f.Name == name })
	return found
}

type yamlConfig struct {
	Version int `yaml:"version"`
	Chroma  struct {
		ServerURL      string `yaml:"server_url"`
		CollectionName string `yaml:"collection_name"`
		Tenant         string `yaml:"tenant"`
		Database       string `yaml:"database"`
	} `yaml:"chroma"`
	Files struct {
		IncludePaths      []string `yaml:"include_paths"`
		ExcludePaths      []string `yaml:"exclude_paths"`
		IncludeExtensions []string `yaml:"include_extensions"`
		ExcludeExtensions []string `yaml:"exclude_extensions"`
	} `yaml:"files"`
	Chunking struct {
		ChunkSize    int `yaml:"chunk_size"`
		ChunkOverlap int `yaml:"chunk_overlap"`
	} `yaml:"chunking"`
	Sync struct {
		BatchSize     int `yaml:"batch_size"`
		RetryAttempts int `yaml:"retry_attempts"`
	} `yaml:"sync"`
	Embedding struct {
		APIURL string `yaml:"api_url"`
		Model  string `yaml:"model"`
		APIKey string `yaml:"api_key"`
	} `yaml:"embedding"`
}

func LoadConfig(flags *FlagValues, environ []string) (Config, error) {
	env := envMap(environ)
	cfg := Config{
		Tenant:                    "default_tenant",
		Database:                  "default_database",
		RetryAttempts:             3,
		RequestTimeout:            120 * time.Second,
		RerankCandidateMultiplier: 3,
		RerankMaxCandidates:       100,
		RerankMaxDocumentBytes:    0,
		RerankMaxRequestBytes:     0,
	}

	configPath := flags.ConfigFile
	if configPath == "" {
		configPath = env["CHROMA_REPO_SEARCH_CONFIG_FILE"]
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		var file yamlConfig
		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&file); err != nil {
			return Config{}, fmt.Errorf("parse config file: %w", err)
		}
		if file.Version != 1 {
			return Config{}, errors.New("config version must be 1")
		}
		assign(&cfg.ServerURL, file.Chroma.ServerURL)
		assign(&cfg.CollectionName, file.Chroma.CollectionName)
		assign(&cfg.Tenant, file.Chroma.Tenant)
		assign(&cfg.Database, file.Chroma.Database)
		assign(&cfg.EmbeddingAPIURL, file.Embedding.APIURL)
		assign(&cfg.EmbeddingModel, file.Embedding.Model)
		assign(&cfg.EmbeddingAPIKey, file.Embedding.APIKey)
		if file.Sync.RetryAttempts != 0 {
			cfg.RetryAttempts = file.Sync.RetryAttempts
		}
	}

	applyEnv := func(target *string, name string) {
		if value := env[name]; value != "" {
			*target = value
		}
	}
	applyEnv(&cfg.ServerURL, "CHROMA_REPO_SEARCH_SERVER_URL")
	applyEnv(&cfg.CollectionName, "CHROMA_REPO_SEARCH_COLLECTION_NAME")
	applyEnv(&cfg.BearerToken, "CHROMA_REPO_SEARCH_BEARER_TOKEN")
	applyEnv(&cfg.Tenant, "CHROMA_REPO_SEARCH_TENANT")
	applyEnv(&cfg.Database, "CHROMA_REPO_SEARCH_DATABASE")
	applyEnv(&cfg.EmbeddingAPIURL, "CHROMA_REPO_SEARCH_EMBEDDING_API_URL")
	applyEnv(&cfg.EmbeddingModel, "CHROMA_REPO_SEARCH_EMBEDDING_MODEL")
	applyEnv(&cfg.EmbeddingAPIKey, "CHROMA_REPO_SEARCH_EMBEDDING_API_KEY")
	applyEnv(&cfg.RerankAPIURL, "CHROMA_REPO_SEARCH_RERANK_API_URL")
	applyEnv(&cfg.RerankModel, "CHROMA_REPO_SEARCH_RERANK_MODEL")
	applyEnv(&cfg.RerankAPIKey, "CHROMA_REPO_SEARCH_RERANK_API_KEY")
	applyEnv(&cfg.HTTPAddr, "MCP_CHROMADB_REPO_SEARCH_HTTP")
	if value := env["CHROMA_REPO_SEARCH_RETRY_ATTEMPTS"]; value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("CHROMA_REPO_SEARCH_RETRY_ATTEMPTS must be an integer")
		}
		cfg.RetryAttempts = parsed
	}
	if value := env["CHROMA_REPO_SEARCH_RERANK_CANDIDATE_MULTIPLIER"]; value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("CHROMA_REPO_SEARCH_RERANK_CANDIDATE_MULTIPLIER must be an integer")
		}
		cfg.RerankCandidateMultiplier = parsed
	}
	for _, setting := range []struct {
		name   string
		target *int
	}{
		{"CHROMA_REPO_SEARCH_RERANK_MAX_CANDIDATES", &cfg.RerankMaxCandidates},
		{"CHROMA_REPO_SEARCH_RERANK_MAX_DOCUMENT_BYTES", &cfg.RerankMaxDocumentBytes},
		{"CHROMA_REPO_SEARCH_RERANK_MAX_REQUEST_BYTES", &cfg.RerankMaxRequestBytes},
	} {
		if value := env[setting.name]; value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return Config{}, fmt.Errorf("%s must be an integer", setting.name)
			}
			*setting.target = parsed
		}
	}
	if value := env["MCP_CHROMADB_REPO_SEARCH_DEBUG"]; value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("MCP_CHROMADB_REPO_SEARCH_DEBUG must be true or false")
		}
		cfg.Debug = parsed
	}
	if value := env["MCP_CHROMADB_REPO_SEARCH_REQUEST_TIMEOUT"]; value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("MCP_CHROMADB_REPO_SEARCH_REQUEST_TIMEOUT must be a duration")
		}
		cfg.RequestTimeout = parsed
	}

	if flags.explicitlySet("server-url") {
		cfg.ServerURL = flags.ServerURL
	}
	if flags.explicitlySet("collection-name") {
		cfg.CollectionName = flags.CollectionName
	}
	if flags.explicitlySet("bearer-token") {
		cfg.BearerToken = flags.BearerToken
	}
	if flags.explicitlySet("tenant") {
		cfg.Tenant = flags.Tenant
	}
	if flags.explicitlySet("database") {
		cfg.Database = flags.Database
	}
	if flags.explicitlySet("retry-attempts") {
		cfg.RetryAttempts = flags.RetryAttempts
	}
	if flags.explicitlySet("embedding-api-url") {
		cfg.EmbeddingAPIURL = flags.EmbeddingAPIURL
	}
	if flags.explicitlySet("embedding-model") {
		cfg.EmbeddingModel = flags.EmbeddingModel
	}
	if flags.explicitlySet("embedding-api-key") {
		cfg.EmbeddingAPIKey = flags.EmbeddingAPIKey
	}
	if flags.explicitlySet("rerank-api-url") {
		cfg.RerankAPIURL = flags.RerankAPIURL
	}
	if flags.explicitlySet("rerank-model") {
		cfg.RerankModel = flags.RerankModel
	}
	if flags.explicitlySet("rerank-api-key") {
		cfg.RerankAPIKey = flags.RerankAPIKey
	}
	if flags.explicitlySet("rerank-candidate-multiplier") {
		cfg.RerankCandidateMultiplier = flags.RerankCandidateMultiplier
	}
	if flags.explicitlySet("rerank-max-candidates") {
		cfg.RerankMaxCandidates = flags.RerankMaxCandidates
	}
	if flags.explicitlySet("rerank-max-document-bytes") {
		cfg.RerankMaxDocumentBytes = flags.RerankMaxDocumentBytes
	}
	if flags.explicitlySet("rerank-max-request-bytes") {
		cfg.RerankMaxRequestBytes = flags.RerankMaxRequestBytes
	}
	if flags.explicitlySet("http") {
		cfg.HTTPAddr = flags.HTTPAddr
	}
	if flags.explicitlySet("debug") {
		cfg.Debug = flags.Debug
	}
	if flags.explicitlySet("request-timeout") {
		cfg.RequestTimeout = flags.RequestTimeout
	}

	cfg.ServerURL = strings.TrimRight(cfg.ServerURL, "/")
	cfg.EmbeddingAPIURL = strings.TrimRight(cfg.EmbeddingAPIURL, "/")
	cfg.RerankAPIURL = strings.TrimRight(cfg.RerankAPIURL, "/")
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func assign(target *string, value string) {
	if value != "" {
		*target = value
	}
}

func envMap(environ []string) map[string]string {
	result := make(map[string]string, len(environ))
	for _, item := range environ {
		if key, value, ok := strings.Cut(item, "="); ok {
			result[key] = value
		}
	}
	return result
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return errors.New("server-url is required")
	}
	if err := validateOrigin("server-url", cfg.ServerURL); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.CollectionName) == "" {
		return errors.New("collection-name is required")
	}
	if !collectionNameRE.MatchString(cfg.CollectionName) {
		return errors.New("collection-name must be 3-512 characters and use letters, numbers, '.', '_' or '-'")
	}
	if strings.TrimSpace(cfg.Tenant) == "" {
		return errors.New("tenant must be non-empty")
	}
	if strings.TrimSpace(cfg.Database) == "" {
		return errors.New("database must be non-empty")
	}
	if cfg.RetryAttempts < 1 {
		return errors.New("retry-attempts must be positive")
	}
	if strings.TrimSpace(cfg.EmbeddingAPIURL) == "" {
		return errors.New("embedding-api-url is required")
	}
	if err := validateOrigin("embedding-api-url", cfg.EmbeddingAPIURL); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.EmbeddingModel) == "" {
		return errors.New("embedding-model is required")
	}
	if (cfg.RerankAPIURL == "") != (cfg.RerankModel == "") {
		return errors.New("rerank-api-url and rerank-model must be configured together")
	}
	if cfg.RerankAPIURL != "" {
		if err := validateOrigin("rerank-api-url", cfg.RerankAPIURL); err != nil {
			return err
		}
		if cfg.RerankCandidateMultiplier < 2 {
			return errors.New("rerank-candidate-multiplier must be at least 2 when reranking is enabled")
		}
		if cfg.RerankMaxCandidates < 2 {
			return errors.New("rerank-max-candidates must be at least 2 when reranking is enabled")
		}
		if cfg.RerankMaxDocumentBytes < 0 {
			return errors.New("rerank-max-document-bytes must be zero or positive")
		}
		if cfg.RerankMaxRequestBytes < 0 {
			return errors.New("rerank-max-request-bytes must be zero or positive")
		}
	}
	if cfg.RequestTimeout <= 0 {
		return errors.New("request-timeout must be positive")
	}
	return nil
}

func validateOrigin(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must be a full HTTP(S) origin without credentials, path, query, or fragment", name)
	}
	return nil
}
