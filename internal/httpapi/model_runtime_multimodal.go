package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
)

const (
	modelRuntimeMaxMessages          = 128
	modelRuntimeMaxContentParts      = 256
	modelRuntimeMaxImageInputs       = 16
	modelRuntimeMaxImageInputBytes   = 20 << 20
	modelRuntimeMaxTotalImageBytes   = 20 << 20
	modelRuntimeMaxTextInputBytes    = 2 << 20
	modelRuntimeMaxExternalURLLength = 8192
)

type modelRuntimeMessage struct {
	Role         string            `json:"role"`
	TextContent  string            `json:"-"`
	ContentParts []llm.ContentPart `json:"-"`
}

type modelRuntimeInputMetrics struct {
	Items      int64
	Bytes      int64
	Characters int64
}

func (m *modelRuntimeMessage) UnmarshalJSON(data []byte) error {
	var encoded struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := decodeStrictModelRuntimeJSON(data, &encoded); err != nil {
		return err
	}
	if len(encoded.Content) == 0 {
		return fmt.Errorf("content is required")
	}
	m.Role = encoded.Role
	if len(encoded.Content) > 0 && encoded.Content[0] == '"' {
		return json.Unmarshal(encoded.Content, &m.TextContent)
	}
	if err := decodeStrictModelRuntimeJSON(encoded.Content, &m.ContentParts); err != nil {
		return fmt.Errorf("content must be a string or an array of content parts: %w", err)
	}
	return nil
}

func decodeStrictModelRuntimeJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validateModelRuntimeMessages(requestMessages []modelRuntimeMessage) ([]llm.Message, modelRuntimeInputMetrics, bool, error) {
	if len(requestMessages) == 0 || len(requestMessages) > modelRuntimeMaxMessages {
		return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages must contain between 1 and %d items", managedagents.ErrInvalid, modelRuntimeMaxMessages)
	}
	messages := make([]llm.Message, 0, len(requestMessages))
	metrics := modelRuntimeInputMetrics{}
	totalParts := 0
	imageCount := 0
	imageBytes := int64(0)
	textBytes := int64(0)
	for messageIndex, message := range requestMessages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "system", "user", "assistant":
		default:
			return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].role must be system, user, or assistant", managedagents.ErrInvalid, messageIndex)
		}
		parts := message.ContentParts
		if parts == nil {
			text := strings.TrimSpace(message.TextContent)
			if text == "" {
				return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content is required", managedagents.ErrInvalid, messageIndex)
			}
			parts = []llm.ContentPart{{Type: "text", Text: text}}
		}
		if len(parts) == 0 {
			return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content must contain at least one part", managedagents.ErrInvalid, messageIndex)
		}
		totalParts += len(parts)
		if totalParts > modelRuntimeMaxContentParts {
			return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: message content must not exceed %d total parts", managedagents.ErrInvalid, modelRuntimeMaxContentParts)
		}
		normalized := make([]llm.ContentPart, 0, len(parts))
		for partIndex, part := range parts {
			switch strings.TrimSpace(part.Type) {
			case "text":
				if part.ImageURL != nil {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content[%d] text part must not contain image_url", managedagents.ErrInvalid, messageIndex, partIndex)
				}
				text := strings.TrimSpace(part.Text)
				if text == "" {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content[%d].text is required", managedagents.ErrInvalid, messageIndex, partIndex)
				}
				textBytes += int64(len(text))
				if textBytes > modelRuntimeMaxTextInputBytes {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: text input exceeds %d bytes", managedagents.ErrInvalid, modelRuntimeMaxTextInputBytes)
				}
				metrics.Items++
				metrics.Bytes += int64(len(text))
				metrics.Characters += int64(utf8.RuneCountInString(text))
				normalized = append(normalized, llm.ContentPart{Type: "text", Text: text})
			case "image_url":
				if role != "user" {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content[%d] image_url is only allowed for user messages", managedagents.ErrInvalid, messageIndex, partIndex)
				}
				if strings.TrimSpace(part.Text) != "" || part.ImageURL == nil {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content[%d].image_url is required", managedagents.ErrInvalid, messageIndex, partIndex)
				}
				imageCount++
				if imageCount > modelRuntimeMaxImageInputs {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: image input must not exceed %d items", managedagents.ErrInvalid, modelRuntimeMaxImageInputs)
				}
				imageURL, size, err := validateModelRuntimeImageURL(*part.ImageURL)
				if err != nil {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content[%d]: %v", managedagents.ErrInvalid, messageIndex, partIndex, err)
				}
				imageBytes += size
				if imageBytes > modelRuntimeMaxTotalImageBytes {
					return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: inline image input exceeds %d total decoded bytes", managedagents.ErrInvalid, modelRuntimeMaxTotalImageBytes)
				}
				metrics.Items++
				metrics.Bytes += size
				normalized = append(normalized, llm.ContentPart{Type: "image_url", ImageURL: &imageURL})
			default:
				return nil, modelRuntimeInputMetrics{}, false, fmt.Errorf("%w: messages[%d].content[%d].type must be text or image_url", managedagents.ErrInvalid, messageIndex, partIndex)
			}
		}
		messages = append(messages, llm.Message{Role: role, Content: normalized})
	}
	return messages, metrics, imageCount > 0, nil
}

func validateModelRuntimeImageURL(input llm.ImageURL) (llm.ImageURL, int64, error) {
	value := strings.TrimSpace(input.URL)
	detail := strings.ToLower(strings.TrimSpace(input.Detail))
	switch detail {
	case "", "auto", "low", "high":
	default:
		return llm.ImageURL{}, 0, fmt.Errorf("image_url.detail must be auto, low, or high")
	}
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		comma := strings.IndexByte(value, ',')
		if comma < 0 {
			return llm.ImageURL{}, 0, fmt.Errorf("image data URL must contain a base64 payload")
		}
		header := strings.Split(value[5:comma], ";")
		if len(header) != 2 || !strings.EqualFold(header[1], "base64") || !supportedModelRuntimeImageMIME(header[0]) {
			return llm.ImageURL{}, 0, fmt.Errorf("image data URL must use base64 PNG, JPEG, WebP, or GIF content")
		}
		payload := value[comma+1:]
		if base64.StdEncoding.DecodedLen(len(payload)) > modelRuntimeMaxImageInputBytes+2 {
			return llm.ImageURL{}, 0, fmt.Errorf("image data URL exceeds %d decoded bytes", modelRuntimeMaxImageInputBytes)
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(payload)
		if err != nil {
			return llm.ImageURL{}, 0, fmt.Errorf("image data URL contains invalid base64")
		}
		if len(decoded) == 0 || len(decoded) > modelRuntimeMaxImageInputBytes {
			return llm.ImageURL{}, 0, fmt.Errorf("image data URL must contain between 1 and %d decoded bytes", modelRuntimeMaxImageInputBytes)
		}
		return llm.ImageURL{URL: value, Detail: detail}, int64(len(decoded)), nil
	}
	if len(value) > modelRuntimeMaxExternalURLLength {
		return llm.ImageURL{}, 0, fmt.Errorf("image URL exceeds %d bytes", modelRuntimeMaxExternalURLLength)
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return llm.ImageURL{}, 0, fmt.Errorf("image URL must be a base64 data:image URL or a public HTTPS URL without credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" || isPrivateModelRuntimeImageHost(host) {
		return llm.ImageURL{}, 0, fmt.Errorf("image URL must use a public host")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return llm.ImageURL{}, 0, fmt.Errorf("image URL contains an invalid port")
		}
	}
	return llm.ImageURL{URL: value, Detail: detail}, int64(len(value)), nil
}

func supportedModelRuntimeImageMIME(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func isPrivateModelRuntimeImageHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") {
		return true
	}
	address, err := netip.ParseAddr(host)
	if err == nil {
		address = address.Unmap()
		return !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast()
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, char := range host {
		if (char < '0' || char > '9') && char != '.' {
			return false
		}
	}
	return true
}
