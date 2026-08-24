package config

import (
	"os"
	"strconv"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Port        int    `env:"PORT"`
	GRPCPort    int    `env:"GRPC_PORT"`
	ChromaHost  string `env:"CHROMA_HOST"`
	ChromaPort  int    `env:"CHROMA_PORT"`
	DefaultAPIKey string `env:"DEFAULT_API_KEY"`
	
	// Embedding config (Honcho parity: 1536d via Qwen3-Embedding-4B MRL)
	EmbedProvider   string `env:"EMBED_PROVIDER"` // "lm-studio" or "openai-compatible"
	LmStudioURL     string `env:"LM_STUDIO_URL"`
	OAICompatibleURL string `env:"OAI_COMPATIBLE_URL"`
	OAIAPIKey       string `env:"OAI_API_KEY"`
	EmbedModel      string `env:"EMBED_MODEL"` // e.g. "Qwen/Qwen3-Embedding-4B" (1536d) or "nomic-embed-text" (768d)
	EmbedDimensions int    `env:"EMBED_DIMENSIONS"` // 1536 default (MRL), Honcho VECTOR_DIMENSIONS parity
	
	// LLM Brain config (optional)
	LLMEnabled        bool   `env:"LLM_ENABLED"`
	LLMProvider       string `env:"LLM_PROVIDER"` // "lm-studio" or "openai-compatible"
	LLMLmStudioURL    string `env:"LLM_STUDIO_URL"`
	LLMOAICompatibleURL string `env:"LLM_OAI_COMPATIBLE_URL"`
	LLMOAIAPIKey      string `env:"LLM_OAI_API_KEY"`
	LLMModel          string `env:"LLM_MODEL"` // e.g. "qwen3:8b" or "gpt-4o-mini"
	
	// TTL / workspace config
	TTLHours       int    `env:"TTL_HOURS"` // 0 = disabled
	WorkspaceConfig string `env:"WORKSPACE_CONFIG"` // json overrides

	// Auth (Honcho-compatible JWT)
	AuthUseAuth   bool   `env:"AUTH_USE_AUTH"`
	AuthJWTSecret string `env:"AUTH_JWT_SECRET"`

	// ChromaDB config
	ChromaTenant     string `env:"CHROMA_TENANT"`
	ChromaDatabase   string `env:"CHROMA_DATABASE"`
}

type tomlConfig struct {
	App struct {
		Port int `toml:"port"`
	} `toml:"app"`
	DB struct {
		Host string `toml:"host"`
		Port int `toml:"port"`
	} `toml:"db"`
	Auth struct {
		UseAuth   *bool  `toml:"use_auth"`
		JWTSecret string `toml:"jwt_secret"`
		APIKey    string `toml:"api_key"`
	} `toml:"auth"`
	Embedding struct {
		Provider   string `toml:"provider"`
		Model      string `toml:"model"`
		URL        string `toml:"url"`
		Dimensions int    `toml:"dimensions"`
	} `toml:"embedding"`
	LLM struct {
		Enabled  *bool  `toml:"enabled"`
		Provider string `toml:"provider"`
		Model    string `toml:"model"`
	} `toml:"llm"`
}

func loadTOML(path string) *tomlConfig {
	if os.Getenv("VECTORIZER_CONFIG_TOML_DISABLED") != "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	var cfg tomlConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil
	}
	return &cfg
}

func Load() *Config {
	tc := loadTOML("config.toml")
	if tc == nil {
		tc = loadTOML("config/config.toml")
	}
	grpcPort := 50051
	if tc != nil {
		// allow [grpc] port in toml if present (optional)
		var raw map[string]interface{}
		if _, err := toml.DecodeFile("config.toml", &raw); err == nil {
			if g, ok := raw["grpc"].(map[string]interface{}); ok {
				if p, ok := g["port"].(int64); ok { grpcPort = int(p) }
			}
		}
	}
	return &Config{
		GRPCPort: getEnvInt("GRPC_PORT", grpcPort),
		Port:            getEnvIntWithTOML("PORT", tcAppInt(tc, func(c *tomlConfig) int { return c.App.Port }), 8091),
		ChromaHost:      getEnvStringWithTOML("CHROMA_HOST", tcStr(tc, func(c *tomlConfig) string { return c.DB.Host }), "localhost"),
		ChromaPort:      getEnvIntWithTOML("CHROMA_PORT", tcAppInt(tc, func(c *tomlConfig) int { return c.DB.Port }), 8100),
		DefaultAPIKey:   getEnvStringWithTOML("DEFAULT_API_KEY", tcStr(tc, func(c *tomlConfig) string { return c.Auth.APIKey }), ""),

		EmbedProvider:   getEnvStringWithTOML("EMBED_PROVIDER", tcStr(tc, func(c *tomlConfig) string { return c.Embedding.Provider }), "openai-compatible"),
		LmStudioURL:     getEnvStringWithTOML("LM_STUDIO_URL", tcStr(tc, func(c *tomlConfig) string { return c.Embedding.URL }), "http://localhost:1234/v1"),
		OAICompatibleURL: getEnvString("OAI_COMPATIBLE_URL", ""),
		OAIAPIKey:       os.Getenv("OAI_API_KEY"),
		EmbedModel:      getEnvStringWithTOML("EMBED_MODEL", tcStr(tc, func(c *tomlConfig) string { return c.Embedding.Model }), "Qwen/Qwen3-Embedding-4B"),
		EmbedDimensions: getEnvIntWithTOML("EMBED_DIMENSIONS", tcAppInt(tc, func(c *tomlConfig) int { return c.Embedding.Dimensions }), 1536),

		LLMEnabled:        envBoolWithTOML("LLM_ENABLED", tcBool(tc, func(c *tomlConfig) *bool { return c.LLM.Enabled }), false),
		LLMProvider:       getEnvStringWithTOML("LLM_PROVIDER", tcStr(tc, func(c *tomlConfig) string { return c.LLM.Provider }), "lm-studio"),
		LLMLmStudioURL:    getEnvString("LLM_STUDIO_URL", os.Getenv("LM_STUDIO_URL")),
		LLMOAICompatibleURL: getEnvString("LLM_OAI_COMPATIBLE_URL", ""),
		LLMOAIAPIKey:      os.Getenv("LLM_OAI_API_KEY"),
		LLMModel:          getEnvStringWithTOML("LLM_MODEL", tcStr(tc, func(c *tomlConfig) string { return c.LLM.Model }), "qwen3:8b"),

		TTLHours:       getEnvInt("TTL_HOURS", 0),

		AuthUseAuth:    envBoolWithTOML("AUTH_USE_AUTH", tcBool(tc, func(c *tomlConfig) *bool { return c.Auth.UseAuth }), false),
		AuthJWTSecret:  getEnvStringWithTOML("AUTH_JWT_SECRET", tcStr(tc, func(c *tomlConfig) string { return c.Auth.JWTSecret }), ""),

		ChromaTenant:     getEnvString("CHROMA_TENANT", "default_tenant"),
		ChromaDatabase:   getEnvString("CHROMA_DATABASE", "default_database"),
	}
}

func (c *Config) EmbedBaseURL() string {
	if c.EmbedProvider == "openai-compatible" && c.OAICompatibleURL != "" {
		return c.OAICompatibleURL
	}
	return c.LmStudioURL
}

func (c *Config) LLMBaseURL() string {
	if !c.LLMEnabled {
		return ""
	}
	if c.LLMProvider == "openai-compatible" && c.LLMOAICompatibleURL != "" {
		return c.LLMOAICompatibleURL
	}
	return c.LLMLmStudioURL
}

func (c *Config) LLMAPIKey() string {
	if c.LLMProvider == "openai-compatible" {
		return c.LLMOAIAPIKey
	}
	return os.Getenv("LLM_API_KEY") // LM Studio doesn't need a key usually
}

func getEnvString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return v
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func tcStr(tc *tomlConfig, fn func(*tomlConfig) string) string {
	if tc == nil {
		return ""
	}
	return fn(tc)
}
func tcAppInt(tc *tomlConfig, fn func(*tomlConfig) int) int {
	if tc == nil {
		return 0
	}
	return fn(tc)
}
func tcBool(tc *tomlConfig, fn func(*tomlConfig) *bool) *bool {
	if tc == nil {
		return nil
	}
	return fn(tc)
}
func getEnvStringWithTOML(envKey, tomlVal, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if tomlVal != "" {
		return tomlVal
	}
	return fallback
}
func getEnvIntWithTOML(envKey string, tomlVal, fallback int) int {
	if v := os.Getenv(envKey); v != "" {
		if iv, err := strconv.Atoi(v); err == nil {
			return iv
		}
	}
	if tomlVal != 0 {
		return tomlVal
	}
	return fallback
}
func envBoolWithTOML(envKey string, tomlVal *bool, fallback bool) bool {
	if v := os.Getenv(envKey); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	if tomlVal != nil {
		return *tomlVal
	}
	return fallback
}
