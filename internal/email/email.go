// Package email sends transactional email (signup verification codes)
// through Amazon SES. The Sender interface exists so services can be tested
// offline and so the app runs cleanly with email disabled.
package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// Sender delivers a single email.
type Sender interface {
	Send(ctx context.Context, to, subject, textBody, htmlBody string) error
}

// SESSender sends email through Amazon SES v2.
type SESSender struct {
	client *sesv2.Client
	from   string
}

// NewSESSender builds an SES sender using the same credential strategy as
// the S3 client: static credentials when provided, otherwise the default
// chain (ECS task role).
func NewSESSender(ctx context.Context, cfg *config.Config) (*SESSender, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.EnvVars.AWSRegion),
	}
	if cfg.EnvVars.AWSAccessKeyID != "" && cfg.EnvVars.AWSSecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.EnvVars.AWSAccessKeyID,
			cfg.EnvVars.AWSSecretAccessKey,
			"",
		)))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for SES: %v", err)
	}
	return &SESSender{
		client: sesv2.NewFromConfig(awsCfg),
		from:   cfg.EnvVars.EmailFrom,
	}, nil
}

// Send delivers one email to a single recipient.
func (s *SESSender) Send(ctx context.Context, to, subject, textBody, htmlBody string) error {
	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: aws.String(subject)},
				Body: &types.Body{
					Text: &types.Content{Data: aws.String(textBody)},
					Html: &types.Content{Data: aws.String(htmlBody)},
				},
			},
		},
	}
	if _, err := s.client.SendEmail(ctx, input); err != nil {
		return fmt.Errorf("ses send failed: %w", err)
	}
	return nil
}
