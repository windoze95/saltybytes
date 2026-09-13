// Package s3 stores recipe images in an S3-compatible object store: AWS S3
// by default, or any store reachable through S3_ENDPOINT (Cloudflare R2,
// DigitalOcean Spaces, MinIO). See config.EnvVars for the S3_* settings.
package s3

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/windoze95/saltybytes-api/internal/config"
)

// The client is built once per process: config is process-wide, and rebuilding
// it per call re-resolves credentials (an HTTP round-trip on ECS task roles).
// Only a successful build is cached, so a transient failure is retried.
var (
	clientMu sync.Mutex
	client   *s3.Client
)

func getClient(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if client != nil {
		return client, nil
	}
	c, err := newS3Client(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client = c
	return c, nil
}

// newS3Client creates an S3 client from the app config. Static credentials
// are used when a key pair is configured (S3_* first, then AWS_*); otherwise
// the default credential chain is preserved (IAM role, instance profile,
// etc.) so ECS/EC2 task roles work without explicit keys. With S3_ENDPOINT
// set, requests go to that endpoint in path style and the SDK's default
// CRC checksum trailers are disabled — R2 and other compatible stores reject
// them.
func newS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.S3Region()),
	}

	if keyID, secret := cfg.S3Credentials(); keyID != "" && secret != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			keyID,
			secret,
			"",
		)))
	}

	endpoint := strings.TrimSpace(cfg.EnvVars.S3Endpoint)
	if endpoint != "" {
		opts = append(opts,
			awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
			awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
		)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %v", err)
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	}), nil
}

// UploadRecipeImageToS3 uploads a given byte array to the image bucket and
// returns the object's public URL. contentType, when non-empty, is stored as
// the object's Content-Type so browsers and CDNs serve the image correctly.
// With S3_PUBLIC_URL set the URL is <S3_PUBLIC_URL>/<key>; otherwise it is
// the SDK-reported location (AWS virtual-hosted style).
func UploadRecipeImageToS3(ctx context.Context, cfg *config.Config, imgBytes []byte, s3Key string, contentType string) (string, error) {
	client, err := getClient(ctx, cfg)
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

	if base := cfg.S3PublicBaseURL(); base != "" {
		return PublicURL(base, s3Key), nil
	}
	return result.Location, nil
}

// PublicURL joins a public base URL and an object key, escaping each key
// segment so S3KeyFromURL round-trips it.
func PublicURL(base, key string) string {
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.TrimRight(base, "/") + "/" + strings.Join(segments, "/")
}

// DeleteRecipeImageFromS3 deletes a given image from the image bucket.
func DeleteRecipeImageFromS3(ctx context.Context, cfg *config.Config, s3Key string) error {
	client, err := getClient(ctx, cfg)
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

// S3KeyFromURL derives the object key from an image URL previously returned
// by an upload. URLs under S3_PUBLIC_URL map directly; AWS S3 URLs (the
// pre-S3_PUBLIC_URL shape still present in older rows) are parsed by host
// style. Returns "" when the URL is empty or cannot be parsed.
func S3KeyFromURL(cfg *config.Config, imageURL string) string {
	if imageURL == "" {
		return ""
	}

	if base := cfg.S3PublicBaseURL(); base != "" {
		if rest, ok := strings.CutPrefix(imageURL, base+"/"); ok {
			return unescapeKey(rest)
		}
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

	return unescapeKey(key)
}

func unescapeKey(key string) string {
	if i := strings.IndexAny(key, "?#"); i >= 0 {
		key = key[:i]
	}
	if unescaped, err := url.PathUnescape(key); err == nil {
		return unescaped
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

// RecipeImageKeyFromURL derives the deletable object key for a recipe's image
// from its stored URL, returning "" unless the key lies under the recipe's
// own "recipes/<recipeID>/" prefix. A recipe's ImageURL can be
// client-supplied (manual import) or scraped from external pages (JSON-LD),
// so a key derived from it must never be trusted to reference objects
// outside the recipe's own folder.
func RecipeImageKeyFromURL(cfg *config.Config, imageURL string, recipeID uint) string {
	key := S3KeyFromURL(cfg, imageURL)
	if key == "" {
		return ""
	}
	if !strings.HasPrefix(key, fmt.Sprintf("recipes/%d/", recipeID)) {
		return ""
	}
	return key
}
