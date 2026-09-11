package handler

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"omnipulse/apps/api-gateway/internal/service"
	"omnipulse/apps/api-gateway/internal/utils"
)

type MediaHandler struct {
	mediaService *service.MediaService
}

func NewMediaHandler(mediaService *service.MediaService) *MediaHandler {
	return &MediaHandler{mediaService: mediaService}
}

// UploadImage handles POST /api/v1/media/upload
// Accepts multipart/form-data with key 'file' (max 10MB)
// Returns { "url": string, "public_id": string }
func (h *MediaHandler) UploadImage(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := r.Context().Value(TenantIDKey).(string)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "Missing tenant context")
		return
	}

	if h.mediaService == nil {
		utils.WriteError(w, http.StatusServiceUnavailable, "Cloudinary media service is not configured on this server")
		return
	}

	// 12MB max memory + body cap
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "File exceeds 10MB limit or invalid multipart payload")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "Missing 'file' field in multipart form-data")
		return
	}
	defer file.Close()

	// Validate content type
	contentType := header.Header.Get("Content-Type")
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/jpg":  true,
		"image/png":  true,
		"image/webp": true,
		"image/gif":  true,
	}

	// Check extension as secondary fallback
	filename := header.Filename
	lowerName := strings.ToLower(filename)
	isValidExt := strings.HasSuffix(lowerName, ".jpg") ||
		strings.HasSuffix(lowerName, ".jpeg") ||
		strings.HasSuffix(lowerName, ".png") ||
		strings.HasSuffix(lowerName, ".webp") ||
		strings.HasSuffix(lowerName, ".gif")

	if !validTypes[contentType] && !isValidExt {
		utils.WriteError(w, http.StatusBadRequest, "Invalid file format. Only JPEG, PNG, WebP, and GIF images are supported.")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "Failed to read uploaded file bytes")
		return
	}

	secureURL, publicID, err := h.mediaService.UploadImage(r.Context(), fileBytes, filename)
	if err != nil {
		log.Printf("[MediaHandler] Cloudinary upload failed for tenant %s: %v\n", tenantID, err)
		utils.WriteError(w, http.StatusBadGateway, fmt.Sprintf("Failed to upload image to CDN: %v", err))
		return
	}

	log.Printf("[MediaHandler] Successfully uploaded image for tenant %s -> %s\n", tenantID, secureURL)
	utils.WriteJSON(w, http.StatusOK, map[string]string{
		"url":       secureURL,
		"public_id": publicID,
	})
}
