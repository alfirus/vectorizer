package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port        int    `env:"PORT"`
	ChromaHost  string `env:"CHROMA_HOST"`
	ChromaPort  int    `env:"CHROMA_PORT"`
	DefaultAPIKey string `env:"DEFAULT_API_KEY"`
	
	// Embedding config
	EmbedProvider   string `env:"EMBED_PROVIDER"` // "lm-studio" or "openai-compatible"
	LmStudioURL     string `env:"LM_STUDIO_URL"`
	OAICompatibleURL string `env:"OAI_COMPATIBLE_URL"`
	OAIAPIKey       string `env:"OAI_API_KEY"`
	EmbedModel      string `env:"EMBED_MODEL"` // e.g. "nomic-embed-text" or "text-embedding-3-small"
	
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

func Load() *Config {
	return &Config{
		Port:            getEnvInt("PORT", 8091),
		ChromaHost:      getEnvString("CHROMA_HOST", "localhost"),
		ChromaPort:      getEnvInt("CHROMA_PORT", 8100),
		DefaultAPIKey:   os.Getenv("DEFAULT_API_KEY"),
		
		EmbedProvider:   getEnvString("EMBED_PROVIDER", "lm-studio"),
		LmStudioURL:     getEnvString("LM_STUDIO_URL", "http://localhost:1234/v1"),
		OAICompatibleURL: getEnvString("OAI_COMPATIBLE_URL", ""),
		OAIAPIKey:       os.Getenv("OAI_API_KEY"),
		EmbedModel:      getEnvString("EMBED_MODEL", "nomic-embed-text"),
		
		LLMEnabled:        envBool("LLM_ENABLED", false),
		LLMProvider:       getEnvString("LLM_PROVIDER", "lm-studio"),
		LLMLmStudioURL:    getEnvString("LLM_STUDIO_URL", os.Getenv("LM_STUDIO_URL")),
		LLMOAICompatibleURL: getEnvString("LLM_OAI_COMPATIBLE_URL", ""),
		LLMOAIAPIKey:      os.Getenv("LLM_OAI_API_KEY"),
		LLMModel:          getEnvString("LLM_MODEL", "qwen3:8b"),
		
		TTLHours:       getEnvInt("TTL_HOURS", 0),

		AuthUseAuth:    envBool("AUTH_USE_AUTH", false),
		AuthJWTSecret:  os.Getenv("AUTH_JWT_SECRET"),

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
