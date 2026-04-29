package api

import (
	"encoding/base64"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// ImageUploadRequest represents the request body for image upload
type ImageUploadRequest struct {
	Data     string `json:"data"`     // base64 encoded image data
	Type     string `json:"type"`     // image type: png, jpeg, gif, webp
	Filename string `json:"filename"` // optional original filename
}

// ImageUploadResponse represents the response for image upload
type ImageUploadResponse struct {
	Path     string `json:"path"`     // e.g., /data/images/1712345678901_abc123.png
	Filename string `json:"filename"` // stored filename
}

// ImagesHandler handles image upload and serving
type ImagesHandler struct {
	DataDir string
}

// allowedImageTypes maps type strings to media types and extensions
var allowedImageTypes = map[string]struct {
	mediaType string
	ext       string
}{
	"png":  {"image/png", ".png"},
	"jpeg": {"image/jpeg", ".jpg"},
	"jpg":  {"image/jpeg", ".jpg"},
	"gif":  {"image/gif", ".gif"},
	"webp": {"image/webp", ".webp"},
}

// Upload handles image upload from base64 data
// POST /api/images/upload
func (h *ImagesHandler) Upload(c echo.Context) error {
	var req ImageUploadRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	// Validate image type
	typeInfo, ok := allowedImageTypes[strings.ToLower(req.Type)]
	if !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "unsupported image type. Allowed: png, jpeg, gif, webp"})
	}

	// Decode base64
	data := req.Data
	// Remove data URL prefix if present (e.g., "data:image/png;base64,")
	if strings.Contains(data, ",") {
		data = strings.Split(data, ",")[1]
	}

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid base64 data"})
	}

	// Check size limit (10MB)
	if len(decoded) > 10*1024*1024 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "image too large. Maximum 10MB"})
	}

	// Validate magic bytes
	detectedType := detectImageType(decoded)
	if detectedType == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "unable to detect image type from file content"})
	}
	if detectedType != typeInfo.mediaType {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": fmt.Sprintf("image type mismatch: declared %s but detected %s", typeInfo.mediaType, detectedType),
		})
	}

	// Create images directory
	imagesDir := filepath.Join(h.DataDir, "cache", "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create images directory"})
	}

	// Generate unique filename
	timestamp := time.Now().UnixNano()
	randomStr := randomString(8)
	filename := fmt.Sprintf("%d_%s%s", timestamp, randomStr, typeInfo.ext)
	fullPath := filepath.Join(imagesDir, filename)

	// Write file
	if err := os.WriteFile(fullPath, decoded, 0644); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save image"})
	}

	log.Printf("[images] Saved image %s (%d bytes)", filename, len(decoded))

	return c.JSON(http.StatusOK, ImageUploadResponse{
		Path:     "/data/cache/images/" + filename,
		Filename: filename,
	})
}

// detectImageType checks magic bytes to verify image type
func detectImageType(data []byte) string {
	if len(data) < 8 {
		return ""
	}

	// Check PNG
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 {
		return "image/png"
	}

	// Check JPEG
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}

	// Check GIF
	if len(data) >= 6 {
		header := string(data[0:6])
		if header == "GIF87a" || header == "GIF89a" {
			return "image/gif"
		}
	}

	// Check WebP
	if len(data) >= 12 {
		if string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			return "image/webp"
		}
	}

	return ""
}

// randomString generates a random alphanumeric string of given length
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
