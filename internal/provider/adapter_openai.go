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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ladr/ladr/pkg/errors"
)

// toOpenAiContent converts MessageParts to the OpenAI API content format.
func toOpenAiContent(parts []MessagePart) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			out = append(out, map[string]interface{}{
				"type": "text",
				"text": part.Text,
			})
		case "image_url":
			if part.ImageURL != nil {
				out = append(out, map[string]interface{}{
					"type":      "image_url",
					"image_url": map[string]interface{}{"url": part.ImageURL.URL, "detail": part.ImageURL.Detail},
				})
			}
		case "file":
			if part.File != nil && (part.File.Data != nil || part.File.FileID != nil) {
				fileObj := map[string]interface{}{}
				if part.File.Data != nil {
					fileObj["file_data"] = *part.File.Data
				}
				if part.File.FileID != nil {
					fileObj["file_id"] = *part.File.FileID
				}
				if part.File.Filename != nil {
					fileObj["filename"] = *part.File.Filename
				}
				out = append(out, map[string]interface{}{
					"type": "file",
					"file": fileObj,
				})
			} else {
				out = append(out, map[string]interface{}{
					"type": "text",
					"text": PartToText(part),
				})
			}
		default:
			out = append(out, map[string]interface{}{
				"type": "text",
				"text": PartToText(part),
			})
		}
	}
	return out
}

// extractResponseText extracts text from an OpenAI API response content field.
func extractResponseText(content interface{}, providerName string) (string, error) {
	if content == nil {
		return "", errors.NewProviderError("Response content was filtered", errors.ProviderErrorOptions{
			Provider:   providerName,
			StatusCode: 200,
			Retryable:  false,
		})
	}
	if s, ok := content.(string); ok {
		return s, nil
	}
	arr, ok := content.([]interface{})
	if !ok {
		return "", nil
	}
	var parts []string
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "text" {
			if s, ok := m["text"].(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "\n" + p
	}
	return result, nil
}

// toOpenAiMessages converts Message slice to OpenAI API message format.
func toOpenAiMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		if len(msg.ContentParts) == 0 {
			out = append(out, map[string]interface{}{
				"role":    string(msg.Role),
				"content": msg.ContentStr,
			})
		} else {
			out = append(out, map[string]interface{}{
				"role":    string(msg.Role),
				"content": toOpenAiContent(msg.ContentParts),
			})
		}
	}
	return out
}

// openAiCompatibleAdapter is the factory-built adapter for OpenAI-compatible providers.
type openAiCompatibleAdapter struct {
	name           string
	defaultBaseURL string
}

// NewOpenAiCompatibleAdapter creates a new ProviderAdapter for an OpenAI-compatible provider.
func NewOpenAiCompatibleAdapter(name, defaultBaseURL string) ProviderAdapter {
	return &openAiCompatibleAdapter{name: name, defaultBaseURL: defaultBaseURL}
}

func (a *openAiCompatibleAdapter) Name() string { return a.name }

func (a *openAiCompatibleAdapter) Call(ctx context.Context, model string, messages []Message, config ProviderConfig) (*AdapterResult, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = a.defaultBaseURL
	}
	if baseURL == "" {
		return nil, errors.NewProviderError(fmt.Sprintf("Missing baseURL for provider adapter %s", a.name), errors.ProviderErrorOptions{
			Provider: a.name,
		})
	}
	if IsCatalogModel(model) && !SupportsProviderModel(a.name, model) {
		return nil, errors.NewProviderError(fmt.Sprintf("Model %s is not configured for provider %s", model, a.name), errors.ProviderErrorOptions{
			Provider: a.name,
		})
	}
	providerModel := ResolveProviderModel(a.name, model)

	body := map[string]interface{}{
		"model":    providerModel,
		"messages": toOpenAiMessages(messages),
	}
	if config.Temperature != nil {
		body["temperature"] = *config.Temperature
	}
	if config.MaxTokens != nil {
		body["max_tokens"] = *config.MaxTokens
	}
	bodyBytes, _ := json.Marshal(body)

	url := trimTrailingSlash(baseURL) + "/chat/completions"
	resp, err := ProviderFetch(ctx, a.name, url, "POST", map[string]string{
		"content-type":  "application/json",
		"authorization": "Bearer " + config.APIKey,
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, errors.NewProviderError(
			fmt.Sprintf("%s call failed (%d): %s", a.name, resp.StatusCode, string(respBody)),
			errors.ProviderErrorOptions{
				Provider:   a.name,
				StatusCode: resp.StatusCode,
				Retryable:  IsRetryableHttpStatus(resp.StatusCode),
			},
		)
	}

	var payload struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, errors.NewProviderError(fmt.Sprintf("Failed to parse response: %s", err.Error()), errors.ProviderErrorOptions{
			Provider: a.name,
		})
	}

	var content interface{}
	if len(payload.Choices) > 0 {
		content = payload.Choices[0].Message.Content
	}
	text, err := extractResponseText(content, a.name)
	if err != nil {
		return nil, err
	}

	var usage *TokenUsage
	if payload.Usage != nil {
		prompt := 0
		completion := 0
		if payload.Usage.PromptTokens != nil {
			prompt = *payload.Usage.PromptTokens
		}
		if payload.Usage.CompletionTokens != nil {
			completion = *payload.Usage.CompletionTokens
		}
		usage = &TokenUsage{PromptTokens: prompt, CompletionTokens: completion}
	}

	return &AdapterResult{Text: text, Usage: usage}, nil
}

func (a *openAiCompatibleAdapter) Stream(ctx context.Context, model string, messages []Message, config ProviderConfig) (<-chan StreamEvent, error) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = a.defaultBaseURL
	}
	if baseURL == "" {
		return nil, errors.NewProviderError(fmt.Sprintf("Missing baseURL for provider adapter %s", a.name), errors.ProviderErrorOptions{
			Provider: a.name,
		})
	}
	if IsCatalogModel(model) && !SupportsProviderModel(a.name, model) {
		return nil, errors.NewProviderError(fmt.Sprintf("Model %s is not configured for provider %s", model, a.name), errors.ProviderErrorOptions{
			Provider: a.name,
		})
	}
	providerModel := ResolveProviderModel(a.name, model)

	body := map[string]interface{}{
		"model":          providerModel,
		"messages":       toOpenAiMessages(messages),
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if config.Temperature != nil {
		body["temperature"] = *config.Temperature
	}
	if config.MaxTokens != nil {
		body["max_tokens"] = *config.MaxTokens
	}
	bodyBytes, _ := json.Marshal(body)

	url := trimTrailingSlash(baseURL) + "/chat/completions"
	resp, err := ProviderFetchStream(ctx, a.name, url, "POST", map[string]string{
		"content-type":  "application/json",
		"authorization": "Bearer " + config.APIKey,
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}

	return ParseOpenAiSSEStream(resp, 0), nil
}

// readBody is a helper to read the full response body.
func readBody(resp *http.Response) string {
	if resp.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// suppress unused import warning for bytes
var _ = bytes.NewReader
