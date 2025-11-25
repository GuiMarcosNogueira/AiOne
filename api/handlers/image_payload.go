package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/midia/aione/internal/providers/dto"
)

const maxImageUploadBytes = 25 << 20

type imageEditMultipartPayload struct {
	Request         dto.ImageEditReq
	ProviderKey     string
	SessionID       string
	SessionTitle    string
	SessionMetadata map[string]any
	ExpiresAt       *time.Time
}

func isMultipartRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	return strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data")
}

func parseImageEditMultipart(r *http.Request) (imageEditMultipartPayload, error) {
	var payload imageEditMultipartPayload
	defer r.Body.Close()

	if err := r.ParseMultipartForm(maxImageUploadBytes); err != nil {
		return payload, fmt.Errorf("parse multipart form: %w", err)
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	form := r.MultipartForm
	if form == nil {
		return payload, fmt.Errorf("multipart payload missing form data")
	}

	payload.Request.Provider = formValue(form, "provider")
	payload.Request.Prompt = formValue(form, "prompt")
	payload.Request.Size = formValue(form, "size")
	payload.Request.ImageURL = formValue(form, "image_url")
	payload.Request.ImageBase64 = formValue(form, "image_base64")
	payload.Request.MaskURL = formValue(form, "mask_url")
	payload.Request.MaskBase64 = formValue(form, "mask_base64")

	if mediaRaw := formValue(form, "media"); mediaRaw != "" {
		if err := json.Unmarshal([]byte(mediaRaw), &payload.Request.Media); err != nil {
			return payload, fmt.Errorf("parse media payload: %w", err)
		}
	}

	payload.ProviderKey = formValue(form, "provider_key")
	payload.SessionID = formValue(form, "session_id")
	payload.SessionTitle = formValue(form, "session_title")

	if metadataRaw := formValue(form, "session_metadata"); metadataRaw != "" {
		if err := json.Unmarshal([]byte(metadataRaw), &payload.SessionMetadata); err != nil {
			return payload, fmt.Errorf("parse session_metadata: %w", err)
		}
	}

	if expiresRaw := formValue(form, "expires_at"); expiresRaw != "" {
		ts, err := time.Parse(time.RFC3339, expiresRaw)
		if err != nil {
			return payload, fmt.Errorf("invalid expires_at value: %w", err)
		}
		payload.ExpiresAt = &ts
	}

	if payload.Request.ImageBase64 == "" {
		dataURL, err := readFileDataURL(form, "image_file", "image")
		if err != nil {
			return payload, err
		}
		payload.Request.ImageBase64 = dataURL
	}
	if payload.Request.MaskBase64 == "" {
		dataURL, err := readFileDataURL(form, "mask_file", "mask")
		if err != nil {
			return payload, err
		}
		payload.Request.MaskBase64 = dataURL
	}

	return payload, nil
}

func formValue(form *multipart.Form, key string) string {
	if form == nil || form.Value == nil {
		return ""
	}
	values := form.Value[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func readFileDataURL(form *multipart.Form, keys ...string) (string, error) {
	if form == nil || form.File == nil {
		return "", nil
	}
	for _, key := range keys {
		files := form.File[key]
		if len(files) == 0 {
			continue
		}
		dataURL, err := fileToDataURL(files[0])
		if err != nil {
			return "", fmt.Errorf("read %s upload: %w", key, err)
		}
		return dataURL, nil
	}
	return "", nil
}

func fileToDataURL(fh *multipart.FileHeader) (string, error) {
	if fh == nil {
		return "", nil
	}
	file, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open upload %s: %w", fh.Filename, err)
	}
	defer file.Close()

	limited := io.LimitReader(file, maxImageUploadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read upload %s: %w", fh.Filename, err)
	}
	if len(data) == 0 {
		return "", nil
	}
	if len(data) > maxImageUploadBytes {
		return "", fmt.Errorf("upload %s exceeds %d bytes", fh.Filename, maxImageUploadBytes)
	}

	mimeType := fh.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	if mimeType == "" {
		return encoded, nil
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
}
