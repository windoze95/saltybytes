package s3

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// newS3Client creates a new S3 client from the app config.
// When AWS access key and secret are provided, static credentials are used;
// otherwise the default credential chain is preserved (IAM role, instance
// profile, etc.) so ECS/EC2 task roles work without explicit keys.
func newS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
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
		return nil, fmt.Errorf("failed to load AWS config: %v", err)
	}
	return s3.NewFromConfig(awsCfg), nil
}

// UploadRecipeImageToS3 uploads a given byte array to an S3 bucket and returns the location URL.
func UploadRecipeImageToS3(ctx context.Context, cfg *config.Config, imgBytes []byte, s3Key string) (string, error) {
	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return "", err
	}

	uploader := manager.NewUploader(client)

	result, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.EnvVars.S3Bucket),
		Key:    aws.String(s3Key),
		Body:   bytes.NewReader(imgBytes),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %v", err)
	}

	return result.Location, nil
}

// DeleteRecipeImageFromS3 deletes a given image from an S3 bucket.
func DeleteRecipeImageFromS3(ctx context.Context, cfg *config.Config, s3Key string) error {
	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return err
	}

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(cfg.EnvVars.S3Bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete from S3: %v", err)
	}

	return nil
}

// GenerateS3Key generates the S3 key for a recipe image, given the recipe ID.
func GenerateS3Key(recipeID uint) string {
	return fmt.Sprintf("recipes/%d/images/recipe_image_%d.jpg", recipeID, recipeID)
}
