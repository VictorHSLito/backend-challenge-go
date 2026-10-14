package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPPort    string
	DatabaseURL string

	// OIDC
	OIDCIssuerURL    string
	OIDCAudience     string
	OIDCInternalRole string
}

func LoadConfig() (Config, error) {
	_ = godotenv.Load()

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	issuer := os.Getenv("OIDC_ISSUER_URL")
	if issuer == "" {
		return Config{}, fmt.Errorf("OIDC_ISSUER_URL is required")
	}

	internalRole := os.Getenv("OIDC_INTERNAL_ROLE")
	if internalRole == "" {
		internalRole = "internal"
	}

	return Config{
		HTTPPort:         port,
		DatabaseURL:      databaseURL,
		OIDCIssuerURL:    issuer,
		OIDCAudience:     os.Getenv("OIDC_AUDIENCE"),
		OIDCInternalRole: internalRole,
	}, nil
}
