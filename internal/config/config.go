package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	PORT                string
	CATALOG_SERVICE_URL string
	ENV                 string
}

func Load() (*Config, error) {
	if os.Getenv("ENV") != "prod" {
		_ = godotenv.Load()
	}
	cfg := &Config{
		PORT:                getEnv("PORT", "8080"),
		CATALOG_SERVICE_URL: getEnv("CATALOG_SERVICE_URL", ""),
		ENV:                 getEnv("ENV", "development"),
	}

	if cfg.CATALOG_SERVICE_URL == "" {
		return nil, fmt.Errorf("CATALOG_SERVICE_URL is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
