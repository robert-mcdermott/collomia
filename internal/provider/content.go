package provider

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func messageContentParts(message Message) ([]ContentPart, error) {
	parts := make([]ContentPart, 0, len(message.Parts)+1)
	if message.Content != "" {
		parts = append(parts, ContentPart{Type: ContentText, Text: message.Content})
	}
	for _, part := range message.Parts {
		switch part.Type {
		case ContentText:
			if part.Text != "" {
				parts = append(parts, part)
			}
		case ContentImage:
			if len(part.Data) == 0 {
				return nil, fmt.Errorf("image attachment %q has no resolved data", part.Name)
			}
			if !supportedProviderImageType(part.MediaType) {
				return nil, fmt.Errorf("image attachment %q has unsupported media type %q", part.Name, part.MediaType)
			}
			parts = append(parts, part)
		default:
			return nil, fmt.Errorf("unsupported message content type %q", part.Type)
		}
	}
	return parts, nil
}

func supportedProviderImageType(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func imageDataURL(part ContentPart) string {
	return "data:" + part.MediaType + ";base64," + base64.StdEncoding.EncodeToString(part.Data)
}

func imageBase64(part ContentPart) string {
	return base64.StdEncoding.EncodeToString(part.Data)
}

func bedrockImageFormat(mediaType string) (string, error) {
	switch strings.ToLower(mediaType) {
	case "image/png":
		return "png", nil
	case "image/jpeg":
		return "jpeg", nil
	case "image/gif":
		return "gif", nil
	case "image/webp":
		return "webp", nil
	default:
		return "", fmt.Errorf("Bedrock does not support attachment media type %q", mediaType)
	}
}
