package s3

import (
	"regexp"
	"testing"
)

func TestGenerateS3Key_VersionedPNG(t *testing.T) {
	key := GenerateS3Key(42)

	pattern := regexp.MustCompile(`^recipes/42/images/recipe_image_42_\d+\.png$`)
	if !pattern.MatchString(key) {
		t.Errorf("GenerateS3Key(42) = %q, want match for %q", key, pattern)
	}
}

func TestGenerateS3KeyAt_Deterministic(t *testing.T) {
	key := generateS3KeyAt(7, 1700000000)
	want := "recipes/7/images/recipe_image_7_1700000000.png"
	if key != want {
		t.Errorf("generateS3KeyAt(7, 1700000000) = %q, want %q", key, want)
	}
}

func TestGenerateS3KeyAt_VersionChangesKey(t *testing.T) {
	first := generateS3KeyAt(1, 1700000000)
	second := generateS3KeyAt(1, 1700000001)
	if first == second {
		t.Errorf("keys for different timestamps should differ, both = %q", first)
	}
}

func TestGenerateUploadKey_UUIDFormat(t *testing.T) {
	key := GenerateUploadKey(9, ".png")

	pattern := regexp.MustCompile(`^uploads/9/images/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.png$`)
	if !pattern.MatchString(key) {
		t.Errorf("GenerateUploadKey(9, \".png\") = %q, want match for %q", key, pattern)
	}
}

func TestGenerateUploadKey_Unique(t *testing.T) {
	first := GenerateUploadKey(1, ".jpg")
	second := GenerateUploadKey(1, ".jpg")
	if first == second {
		t.Errorf("upload keys should be unique, both = %q", first)
	}
}

func TestS3KeyFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "virtual-hosted style",
			url:  "https://my-bucket.s3.us-east-2.amazonaws.com/recipes/1/images/recipe_image_1_1700000000.png",
			want: "recipes/1/images/recipe_image_1_1700000000.png",
		},
		{
			name: "path style",
			url:  "https://s3.us-east-2.amazonaws.com/my-bucket/recipes/1/images/recipe_image_1.jpg",
			want: "recipes/1/images/recipe_image_1.jpg",
		},
		{
			name: "legacy static key",
			url:  "https://my-bucket.s3.us-east-2.amazonaws.com/recipes/5/images/recipe_image_5.jpg",
			want: "recipes/5/images/recipe_image_5.jpg",
		},
		{
			name: "url-encoded path",
			url:  "https://my-bucket.s3.us-east-2.amazonaws.com/uploads/1/images/my%20image.png",
			want: "uploads/1/images/my image.png",
		},
		{
			name: "empty",
			url:  "",
			want: "",
		},
		{
			name: "no path",
			url:  "https://my-bucket.s3.us-east-2.amazonaws.com",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := S3KeyFromURL(tt.url); got != tt.want {
				t.Errorf("S3KeyFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
