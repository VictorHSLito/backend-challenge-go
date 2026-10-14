package sqs

import (
	"context"
	"time"

	"github.com/victorhslito/junglegaming/internal/config"
	"go.uber.org/fx"
)

var Module = fx.Module("sqs",
	fx.Provide(newClient),
)

func newClient(cfg config.Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return NewClient(ctx, Config{
		Region:          cfg.AWSRegion,
		Endpoint:        cfg.SQSEndpoint,
		AccessKeyID:     cfg.AWSAccessKeyID,
		SecretAccessKey: cfg.AWSSecretAccessKey,
	})
}
