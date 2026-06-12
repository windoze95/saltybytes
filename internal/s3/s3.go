package s3

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
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

// UploadRecipeImageToS3 uploads a given byte array to an S3 bucket and returns
// the location URL. contentType, when non-empty, is stored as the object's
// Content-Type so browsers and CDNs serve the image correctly.
func UploadRecipeImageToS3(ctx context.Context, cfg *config.Config, imgBytes []byte, s3Key string, contentType string) (string, error) {
	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return "", err
	}

	uploader := manager.NewUploader(client)

	input := &s3.PutObjectInput{
		Bucket: aws.String(cfg.EnvVars.S3Bucket),
		Key:    aws.String(s3Key),
		Body:   bytes.NewReader(imgBytes),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	result, err := uploader.Upload(ctx, input)
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

// GenerateS3Key generates a timestamp-versioned S3 key for a generated recipe
// image. Versioning the key gives regenerated images a fresh URL so URL-keyed
// caches (Flutter cached_network_image, CDNs) pick up the new image. Generated
// images are DALL-E PNG bytes, hence the .png extension.
func GenerateS3Key(recipeID uint) string {
	return generateS3KeyAt(recipeID, time.Now().Unix())
}

// generateS3KeyAt is the deterministic core of GenerateS3Key, split out for testing.
func generateS3KeyAt(recipeID uint, unixTS int64) string {
	return fmt.Sprintf("recipes/%d/images/recipe_image_%d_%d.png", recipeID, recipeID, unixTS)
}

// GenerateUploadKey generates a collision-free S3 key for a user-uploaded
// image. The key is server-generated (never derived from the client filename)
// so uploads cannot overwrite each other or smuggle path segments. ext must
// include the leading dot (e.g. ".png").
func GenerateUploadKey(userID uint, ext string) string {
	return fmt.Sprintf("uploads/%d/images/%s%s", userID, uuid.NewString(), ext)
}

// S3KeyFromURL derives the S3 object key from an object URL previously
// returned by an upload. Returns "" when the URL is empty or cannot be parsed.
func S3KeyFromURL(imageURL string) string {
	if imageURL == "" {
		return ""
	}

	u, err := url.Parse(imageURL)
	if err != nil {
		return ""
	}

	key := strings.TrimPrefix(u.Path, "/")

	// Path-style URLs (https://s3.<region>.amazonaws.com/<bucket>/<key>)
	// include the bucket as the first path segment; strip it. Virtual-hosted
	// URLs (https://<bucket>.s3.<region>.amazonaws.com/<key>) do not.
	if isPathStyleS3Host(u.Host) {
		if i := strings.Index(key, "/"); i >= 0 {
			key = key[i+1:]
		}
	}

	if unescaped, err := url.PathUnescape(key); err == nil {
		key = unescaped
	}

	return key
}

// isPathStyleS3Host reports whether the host is a bare S3 service endpoint
// (path-style: "s3.<region>.amazonaws.com", "s3-<region>.amazonaws.com", or
// "s3.amazonaws.com"). Virtual-hosted hosts carry the bucket as a subdomain
// ("<bucket>.s3.<region>.amazonaws.com") — including buckets whose own name
// starts with "s3-" — and must not have a path segment stripped.
func isPathStyleS3Host(host string) bool {
	if host == "s3.amazonaws.com" {
		return true
	}
	rest, ok := strings.CutSuffix(host, ".amazonaws.com")
	if !ok {
		return false
	}
	var region string
	switch {
	case strings.HasPrefix(rest, "s3."):
		region = rest[len("s3."):]
	case strings.HasPrefix(rest, "s3-"):
		region = rest[len("s3-"):]
	default:
		return false
	}
	// A bucket subdomain would introduce extra dots before the s3 label.
	return region != "" && !strings.Contains(region, ".")
}

// RecipeImageKeyFromURL derives the deletable S3 key for a recipe's image
// from its stored URL, returning "" unless the key lies under the recipe's
// own "recipes/<recipeID>/" prefix. A recipe's ImageURL can be
// client-supplied (manual import) or scraped from external pages (JSON-LD),
// so a key derived from it must never be trusted to reference objects
// outside the recipe's own folder.
func RecipeImageKeyFromURL(imageURL string, recipeID uint) string {
	key := S3KeyFromURL(imageURL)
	if key == "" {
		return ""
	}
	if !strings.HasPrefix(key, fmt.Sprintf("recipes/%d/", recipeID)) {
		return ""
	}
	return key
}
