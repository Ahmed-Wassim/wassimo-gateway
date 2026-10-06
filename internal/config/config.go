package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	PORT                 string
	CATALOG_SERVICE_URL  string
	IDENTITY_SERVICE_URL string
	CART_SERVICE_URL     string
	ENV                  string
	JWT_PUBLIC_KEY       string
	JWT_ISSUER           string
	JWT_AUDIENCE         string
	JWT_KID              string
}

func Load() (*Config, error) {
	if os.Getenv("ENV") != "prod" {
		_ = godotenv.Load()
	}
	cfg := &Config{
		PORT:                 getEnv("PORT", "8080"),
		CATALOG_SERVICE_URL:  getEnv("CATALOG_SERVICE_URL", ""),
		IDENTITY_SERVICE_URL: getEnv("IDENTITY_SERVICE_URL", ""),
		CART_SERVICE_URL:     getEnv("CART_SERVICE_URL", ""),
		ENV:                  getEnv("ENV", "development"),
		JWT_PUBLIC_KEY:       getEnv("JWT_PUBLIC_KEY", ""),
		JWT_ISSUER:           getEnv("JWT_ISSUER", ""),
		JWT_AUDIENCE:         getEnv("JWT_AUDIENCE", ""),
		JWT_KID:              getEnv("JWT_KID", ""),
	}

	if cfg.CATALOG_SERVICE_URL == "" {
		return nil, fmt.Errorf("CATALOG_SERVICE_URL is required")
	}
	if cfg.IDENTITY_SERVICE_URL == "" {
		return nil, fmt.Errorf("IDENTITY_SERVICE_URL is required")
	}
	if cfg.CART_SERVICE_URL == "" {
		return nil, fmt.Errorf("CART_SERVICE_URL is required")
	}
	if cfg.JWT_PUBLIC_KEY == "" {
		return nil, fmt.Errorf("JWT_PUBLIC_KEY is required")
	}
	if cfg.JWT_ISSUER == "" {
		return nil, fmt.Errorf("JWT_ISSUER is required")
	}
	if cfg.JWT_AUDIENCE == "" {
		return nil, fmt.Errorf("JWT_AUDIENCE is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
