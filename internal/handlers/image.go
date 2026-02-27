package handlers

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/s3"
	"github.com/windoze95/saltybytes-api/internal/util"
	"go.uber.org/zap"
)

// ImageHandler handles image upload requests.
type ImageHandler struct {
	Cfg *config.Config
}

// NewImageHandler creates a new ImageHandler.
func NewImageHandler(cfg *config.Config) *ImageHandler {
	return &ImageHandler{Cfg: cfg}
}

// allowedImageTypes is the set of accepted image file extensions.
var allowedImageTypes = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
}

// UploadImage handles POST /v1/images/upload
func (h *ImageHandler) UploadImage(c *gin.Context) {
	user, err := util.GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	file, header, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Image file is required"})
		return
	}
	defer file.Close()

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedImageTypes[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported image type. Allowed: jpg, png, webp"})
		return
	}

	// Validate file size (max 10MB)
	const maxSize = 10 << 20
	if header.Size > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Image exceeds maximum size of 10MB"})
		return
	}

	// Read file bytes
	imgBytes, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read image"})
		return
	}

	// Upload to S3
	s3Key := fmt.Sprintf("uploads/%d/images/%s", user.ID, header.Filename)
	imageURL, err := s3.UploadRecipeImageToS3(c.Request.Context(), h.Cfg, imgBytes, s3Key)
	if err != nil {
		logger.Get().Error("failed to upload image to S3", zap.Uint("user_id", user.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload image"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"image_url": imageURL})
}
