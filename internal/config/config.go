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

	// AWS / SQS
	AWSRegion          string
	SQSEndpoint        string // empty = real AWS; set to LocalStack URL in dev
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	SQSInputQueue      string // inbound wagering transactions (FIFO)
	SQSEventsQueue     string // outbound wallet events (FIFO)
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

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	inputQueue := os.Getenv("SQS_INPUT_QUEUE")
	if inputQueue == "" {
		return Config{}, fmt.Errorf("SQS_INPUT_QUEUE is required")
	}
	eventsQueue := os.Getenv("SQS_EVENTS_QUEUE")
	if eventsQueue == "" {
		return Config{}, fmt.Errorf("SQS_EVENTS_QUEUE is required")
	}

	return Config{
		HTTPPort:         port,
		DatabaseURL:      databaseURL,
		OIDCIssuerURL:    issuer,
		OIDCAudience:     os.Getenv("OIDC_AUDIENCE"),
		OIDCInternalRole: internalRole,

		AWSRegion:          region,
		SQSEndpoint:        os.Getenv("SQS_ENDPOINT"),
		AWSAccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SQSInputQueue:      inputQueue,
		SQSEventsQueue:     eventsQueue,
	}, nil
}
