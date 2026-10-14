// Package sqs is a thin wrapper around aws-sdk-go-v2's SQS client. It owns
// one concern: hide the LocalStack-vs-AWS switch and expose the three
// operations the rest of the project needs — Publish, Receive, Delete.
//
// Everything else (serialisation, retry policy, dedup) lives in the
// consumer/publisher workers so this file stays infra-only.
package sqs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Config captures everything the client needs. All fields except Region
// are optional — missing endpoint means "real AWS".
type Config struct {
	Region          string
	Endpoint        string // e.g. http://localhost:4566 for LocalStack; empty for real AWS
	AccessKeyID     string
	SecretAccessKey string
}

// Client is the wrapper exposed to the rest of the app.
type Client struct {
	raw *sqs.Client
}

// NewClient builds a configured *sqs.Client. When Endpoint is set we also
// swap in static credentials — LocalStack accepts anything, but the SDK
// refuses to run with no provider configured.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Endpoint != "" {
		accessKey := cfg.AccessKeyID
		if accessKey == "" {
			accessKey = "test"
		}
		secret := cfg.SecretAccessKey
		if secret == "" {
			secret = "test"
		}
		loadOpts = append(loadOpts,
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secret, "")),
		)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("sqs: failed to load aws config: %w", err)
	}

	sqsOpts := []func(*sqs.Options){}
	if cfg.Endpoint != "" {
		endpoint := cfg.Endpoint
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	return &Client{raw: sqs.NewFromConfig(awsCfg, sqsOpts...)}, nil
}

// ResolveQueueURL calls GetQueueUrl once at startup — we store the URL
// in the worker and use it for every subsequent call.
func (c *Client) ResolveQueueURL(ctx context.Context, name string) (string, error) {
	out, err := c.raw.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return "", fmt.Errorf("sqs: cannot resolve queue %q: %w", name, err)
	}
	return aws.ToString(out.QueueUrl), nil
}

// PublishInput is the shape the outbox publisher produces per event. For
// FIFO queues both MessageGroupId and MessageDeduplicationId are required.
type PublishInput struct {
	QueueURL               string
	Body                   string
	MessageGroupID         string // same group → strict order
	MessageDeduplicationID string // SQS-side dedup window (5 min)
}

func (c *Client) Publish(ctx context.Context, in PublishInput) error {
	input := &sqs.SendMessageInput{
		QueueUrl:    aws.String(in.QueueURL),
		MessageBody: aws.String(in.Body),
	}
	if isFIFO(in.QueueURL) {
		input.MessageGroupId = aws.String(in.MessageGroupID)
		input.MessageDeduplicationId = aws.String(in.MessageDeduplicationID)
	}
	_, err := c.raw.SendMessage(ctx, input)
	if err != nil {
		return fmt.Errorf("sqs: send to %s: %w", in.QueueURL, err)
	}
	return nil
}

// ReceivedMessage wraps the subset of fields the consumer needs. We expose
// the ReceiptHandle unmodified so the Delete call can echo it back.
type ReceivedMessage struct {
	MessageID     string
	Body          string
	ReceiptHandle string
}

func (c *Client) Receive(ctx context.Context, queueURL string, maxMessages int32, waitSeconds, visibilityTimeout int32) ([]ReceivedMessage, error) {
	out, err := c.raw.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:              aws.String(queueURL),
		MaxNumberOfMessages:   maxMessages,
		WaitTimeSeconds:       waitSeconds,
		VisibilityTimeout:     visibilityTimeout,
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		return nil, fmt.Errorf("sqs: receive from %s: %w", queueURL, err)
	}
	msgs := make([]ReceivedMessage, 0, len(out.Messages))
	for _, m := range out.Messages {
		msgs = append(msgs, ReceivedMessage{
			MessageID:     aws.ToString(m.MessageId),
			Body:          aws.ToString(m.Body),
			ReceiptHandle: aws.ToString(m.ReceiptHandle),
		})
	}
	return msgs, nil
}

func (c *Client) Delete(ctx context.Context, queueURL, receiptHandle string) error {
	_, err := c.raw.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: aws.String(receiptHandle),
	})
	if err != nil {
		return fmt.Errorf("sqs: delete on %s: %w", queueURL, err)
	}
	return nil
}

// IsNotExist tells "queue missing" apart from other failures — useful at
// startup to produce a clear error message.
func IsNotExist(err error) bool {
	if err == nil {
		return false
	}
	var qne *types.QueueDoesNotExist
	return errors.As(err, &qne)
}

func isFIFO(queueURL string) bool {
	return strings.HasSuffix(queueURL, ".fifo")
}
