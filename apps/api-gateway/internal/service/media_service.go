package service

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type MediaService struct {
	cloudName  string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func parseCloudinaryURL(rawURL string) (cloudName, apiKey, apiSecret string, err error) {
	if rawURL == "" {
		return "", "", "", errors.New("CLOUDINARY_URL is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse CLOUDINARY_URL: %w", err)
	}
	cloudName = u.Host
	if u.User != nil {
		apiKey = u.User.Username()
		apiSecret, _ = u.User.Password()
	}
	if cloudName == "" || apiKey == "" || apiSecret == "" {
		return "", "", "", errors.New("invalid CLOUDINARY_URL: missing cloud_name, api_key, or api_secret")
	}
	return cloudName, apiKey, apiSecret, nil
}

func NewMediaService(cloudinaryURL string) (*MediaService, error) {
	cloudName, apiKey, apiSecret, err := parseCloudinaryURL(cloudinaryURL)
	if err != nil {
		return nil, err
	}

	return &MediaService{
		cloudName:  cloudName,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

type CloudinaryUploadResponse struct {
	PublicID  string `json:"public_id"`
	SecureURL string `json:"secure_url"`
	Format    string `json:"format"`
	Bytes     int    `json:"bytes"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// UploadImage uploads image bytes directly to Cloudinary CDN under folder 'omnipulse/broadcasts'
func (s *MediaService) UploadImage(ctx context.Context, fileBytes []byte, filename string) (string, string, error) {
	if len(fileBytes) == 0 {
		return "", "", errors.New("file payload is empty")
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	folder := "omnipulse/broadcasts"

	// Sign parameters: sorted alphabetically: folder=...&timestamp=...<apiSecret>
	toSign := fmt.Sprintf("folder=%s&timestamp=%s%s", folder, timestamp, s.apiSecret)
	h := sha1.New()
	h.Write([]byte(toSign))
	signature := hex.EncodeToString(h.Sum(nil))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	_ = writer.WriteField("api_key", s.apiKey)
	_ = writer.WriteField("timestamp", timestamp)
	_ = writer.WriteField("folder", folder)
	_ = writer.WriteField("signature", signature)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", "", fmt.Errorf("failed to create multipart form file: %w", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		return "", "", fmt.Errorf("failed to write file bytes: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/image/upload", s.cloudName)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, &body)
	if err != nil {
		return "", "", fmt.Errorf("failed to build upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to reach Cloudinary API: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read Cloudinary response: %w", err)
	}

	var cldResp CloudinaryUploadResponse
	if err := json.Unmarshal(respBytes, &cldResp); err != nil {
		return "", "", fmt.Errorf("failed to parse Cloudinary response: %w", err)
	}

	if cldResp.Error != nil {
		return "", "", fmt.Errorf("cloudinary upload rejected: %s", cldResp.Error.Message)
	}

	if cldResp.SecureURL == "" {
		return "", "", fmt.Errorf("cloudinary response missing secure_url (status %d)", resp.StatusCode)
	}

	return cldResp.SecureURL, cldResp.PublicID, nil
}
