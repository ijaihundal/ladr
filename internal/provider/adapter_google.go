// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ladr/ladr/pkg/errors"
)

// joinStrings joins a slice of strings with sep, skipping empty strings.
func joinStrings(parts []string, sep string) string {
	var nonEmpty []string
	for _, s := range parts {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	if len(nonEmpty) == 1 {
		return nonEmpty[0]
	}
	result := nonEmpty[0]
	for _, s := range nonEmpty[1:] {
		result += sep + s
	}
	return result
}

// googleToParts converts a Message to Google AI Studio "parts" format.
func googleToParts(message Message) []map[string]interface{} {
	if len(message.ContentParts) == 0 {
		return []map[string]interface{}{
			{"text": message.ContentStr},
		}
	}

	out := make([]map[string]interface{}, 0, len(message.ContentParts))
	for _, part := range message.ContentParts {
		switch part.Type {
		case "text":
			out = append(out, map[string]interface{}{"text": part.Text})
		case "image_url":
			if part.ImageURL != nil {
				parsed := ParseDataUrl(part.ImageURL.URL)
				if parsed != nil {
					out = append(out, map[string]interface{}{
						"inlineData": map[string]interface{}{
							"mimeType": parsed.MimeType,
							"data":     parsed.Data,
						},
					})
				} else {
					out = append(out, map[string]interface{}{
						"fileData": map[string]interface{}{
							"mimeType": "image/jpeg",
							"fileUri":  part.ImageURL.URL,
						},
					})
				}
			}
		case "file":
			if part.File != nil {
				if part.File.Data != nil && *part.File.Data != "" {
					mt := "application/octet-stream"
					if part.File.MimeType != nil && *part.File.MimeType != "" {
						mt = *part.File.MimeType
					}
					out = append(out, map[string]interface{}{
						"inlineData": map[string]interface{}{
							"mimeType": mt,
							"data":     *part.File.Data,
						},
					})
				} else if part.File.URL != nil && *part.File.URL != "" {
					mt := "application/octet-stream"
					if part.File.MimeType != nil && *part.File.MimeType != "" {
						mt = *part.File.MimeType
					}
					out = append(out, map[string]interface{}{
						"fileData": map[string]interface{}{
							"mimeType": mt,
							"fileUri":  *part.File.URL,
						},
					})
				}
			}
		default:
			out = append(out, map[string]interface{}{"text": PartToText(part)})
		}
	}
	return out
}

// googleAiStudioAdapter implements ProviderAdapter for the Google AI Studio API.
type googleAiStudioAdapter struct{}

func (a *googleAiStudioAdapter) Name() string { return "google_ai_studio" }

func (a *googleAiStudioAdapter) Call(ctx context.Context, model string, messages []Message, config ProviderConfig) (*AdapterResult, error) {
	endpointBase := config.BaseURL
	if endpointBase == "" {
		endpointBase = "https://generativelanguage.googleapis.com/v1beta"
	}

	// Extract system instruction
	var systemParts []string
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			for _, p := range googleToParts(msg) {
				if t, ok := p["text"].(string); ok && t != "" {
					systemParts = append(systemParts, t)
				}
			}
		}
	}
	systemText := joinStrings(systemParts, "\n\n")

	// Build contents
	var contents []map[string]interface{}
	for _, msg := range messages {
		if msg.Role == RoleUser || msg.Role == RoleAssistant {
			role := "user"
			if msg.Role == RoleAssistant {
				role = "model"
			}
			contents = append(contents, map[string]interface{}{
				"role":  role,
				"parts": googleToParts(msg),
			})
		}
	}

	payload := map[string]interface{}{
		"generationConfig": map[string]interface{}{},
	}
	if len(contents) > 0 {
		payload["contents"] = contents
	} else {
		payload["contents"] = []map[string]interface{}{
			{"role": "user", "parts": []map[string]interface{}{{"text": ""}}},
		}
	}
	if systemText != "" {
		payload["systemInstruction"] = map[string]interface{}{
			"parts": []map[string]interface{}{{"text": systemText}},
		}
	}
	genConfig := payload["generationConfig"].(map[string]interface{})
	if config.Temperature != nil {
		genConfig["temperature"] = *config.Temperature
	}
	if config.MaxTokens != nil {
		genConfig["maxOutputTokens"] = *config.MaxTokens
	}

	bodyBytes, _ := json.Marshal(payload)
	url := trimTrailingSlash(endpointBase) + "/models/" + model + ":generateContent"

	status, respBody, err := ProviderFetchBytes(ctx, "google_ai_studio", url, "POST", map[string]string{
		"content-type":   "application/json",
		"x-goog-api-key": config.APIKey,
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}

	if status < 200 || status >= 300 {
		return nil, errors.NewProviderError(
			fmt.Sprintf("google_ai_studio call failed (%d): %s", status, string(respBody)),
			errors.ProviderErrorOptions{
				Provider:   "google_ai_studio",
				StatusCode: status,
				Retryable:  IsRetryableHttpStatus(status),
			},
		)
	}

	var result struct {
		Candidates []struct {
			Content *struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     *int `json:"promptTokenCount"`
			CandidatesTokenCount *int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, errors.NewProviderError(fmt.Sprintf("Failed to parse response: %s", err.Error()), errors.ProviderErrorOptions{
			Provider: "google_ai_studio",
		})
	}

	var textParts []string
	if len(result.Candidates) > 0 && result.Candidates[0].Content != nil {
		for _, p := range result.Candidates[0].Content.Parts {
			if p.Text != "" {
				textParts = append(textParts, p.Text)
			}
		}
	}
	text := joinStrings(textParts, "\n")

	var usage *TokenUsage
	if result.UsageMetadata != nil {
		prompt := 0
		completion := 0
		if result.UsageMetadata.PromptTokenCount != nil {
			prompt = *result.UsageMetadata.PromptTokenCount
		}
		if result.UsageMetadata.CandidatesTokenCount != nil {
			completion = *result.UsageMetadata.CandidatesTokenCount
		}
		usage = &TokenUsage{PromptTokens: prompt, CompletionTokens: completion}
	}

	return &AdapterResult{Text: text, Usage: usage}, nil
}

func (a *googleAiStudioAdapter) Stream(ctx context.Context, model string, messages []Message, config ProviderConfig) (<-chan StreamEvent, error) {
	return nil, errors.NewProviderError("google_ai_studio streaming not supported", errors.ProviderErrorOptions{
		Provider: "google_ai_studio",
	})
}
