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
	"io"
	"strings"

	"github.com/ladr/ladr/pkg/errors"
)

// toAnthropicContent converts a Message to Anthropic API content format.
func toAnthropicContent(message Message) []map[string]interface{} {
	if len(message.ContentParts) == 0 {
		return []map[string]interface{}{
			{"type": "text", "text": message.ContentStr},
		}
	}

	out := make([]map[string]interface{}, 0, len(message.ContentParts))
	for _, part := range message.ContentParts {
		switch part.Type {
		case "text":
			out = append(out, map[string]interface{}{"type": "text", "text": part.Text})
		case "image_url":
			if part.ImageURL != nil {
				parsed := ParseDataUrl(part.ImageURL.URL)
				if parsed != nil {
					out = append(out, map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": parsed.MimeType,
							"data":       parsed.Data,
						},
					})
				} else {
					out = append(out, map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type": "url",
							"url":  part.ImageURL.URL,
						},
					})
				}
			}
		default:
			out = append(out, map[string]interface{}{"type": "text", "text": PartToText(part)})
		}
	}
	return out
}

// anthropicAdapter implements ProviderAdapter for the Anthropic Messages API.
type anthropicAdapter struct{}

func (a *anthropicAdapter) Name() string { return "anthropic" }

func (a *anthropicAdapter) buildAnthropicPayload(model string, messages []Message, config ProviderConfig, stream bool) map[string]interface{} {
	// Extract system messages
	var systemParts []string
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if len(msg.ContentParts) == 0 {
				systemParts = append(systemParts, msg.ContentStr)
			}
		}
	}

	// Build conversation messages (user + assistant; system handled separately)
	var apiMessages []map[string]interface{}
	for _, msg := range messages {
		if msg.Role == RoleUser || msg.Role == RoleAssistant {
			role := "user"
			if msg.Role == RoleAssistant {
				role = "assistant"
			}
			apiMessages = append(apiMessages, map[string]interface{}{
				"role":    role,
				"content": toAnthropicContent(msg),
			})
		}
	}

	maxTokens := 4096
	if config.MaxTokens != nil {
		maxTokens = *config.MaxTokens
	}

	payload := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   apiMessages,
	}
	if len(systemParts) > 0 {
		payload["system"] = strings.Join(systemParts, "\n\n")
	}
	if config.Temperature != nil {
		payload["temperature"] = *config.Temperature
	}
	if stream {
		payload["stream"] = true
	}
	if len(apiMessages) == 0 {
		payload["messages"] = []map[string]interface{}{
			{"role": "user", "content": []map[string]interface{}{{"type": "text", "text": ""}}},
		}
	}
	return payload
}

func (a *anthropicAdapter) Call(ctx context.Context, model string, messages []Message, config ProviderConfig) (*AdapterResult, error) {
	payload := a.buildAnthropicPayload(model, messages, config, false)
	bodyBytes, _ := json.Marshal(payload)

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	url := trimTrailingSlash(baseURL) + "/v1/messages"

	resp, err := ProviderFetch(ctx, "anthropic", url, "POST", map[string]string{
		"content-type":      "application/json",
		"x-api-key":         config.APIKey,
		"anthropic-version": "2023-06-01",
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, errors.NewProviderError(
			fmt.Sprintf("anthropic call failed (%d): %s", resp.StatusCode, string(respBody)),
			errors.ProviderErrorOptions{
				Provider:   "anthropic",
				StatusCode: resp.StatusCode,
				Retryable:  IsRetryableHttpStatus(resp.StatusCode),
			},
		)
	}

	var json_payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *struct {
			InputTokens  *int `json:"input_tokens"`
			OutputTokens *int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&json_payload); err != nil {
		return nil, errors.NewProviderError(fmt.Sprintf("Failed to parse response: %s", err.Error()), errors.ProviderErrorOptions{
			Provider: "anthropic",
		})
	}

	text := ""
	for _, item := range json_payload.Content {
		if item.Type == "text" && item.Text != "" {
			if text != "" {
				text += item.Text
			} else {
				text = item.Text
			}
		}
	}

	var usage *TokenUsage
	if json_payload.Usage != nil {
		prompt := 0
		completion := 0
		if json_payload.Usage.InputTokens != nil {
			prompt = *json_payload.Usage.InputTokens
		}
		if json_payload.Usage.OutputTokens != nil {
			completion = *json_payload.Usage.OutputTokens
		}
		usage = &TokenUsage{PromptTokens: prompt, CompletionTokens: completion}
	}

	return &AdapterResult{Text: text, Usage: usage}, nil
}

func (a *anthropicAdapter) Stream(ctx context.Context, model string, messages []Message, config ProviderConfig) (<-chan StreamEvent, error) {
	payload := a.buildAnthropicPayload(model, messages, config, true)
	bodyBytes, _ := json.Marshal(payload)

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	url := trimTrailingSlash(baseURL) + "/v1/messages"

	resp, err := ProviderFetchStream(ctx, "anthropic", url, "POST", map[string]string{
		"content-type":      "application/json",
		"x-api-key":         config.APIKey,
		"anthropic-version": "2023-06-01",
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		reader := newLineReader(resp.Body)
		var usage *TokenUsage

		for {
			line, err := reader.ReadLine()
			if len(line) > 0 {
				trimmed := strings.TrimSpace(string(line))
				if strings.HasPrefix(trimmed, "data: ") {
					jsonStr := trimmed[6:]
					var event map[string]interface{}
					if json.Unmarshal([]byte(jsonStr), &event) == nil {
						eventType, _ := event["type"].(string)
						switch eventType {
						case "content_block_delta":
							if delta, ok := event["delta"].(map[string]interface{}); ok {
								if dt, _ := delta["type"].(string); dt == "text_delta" {
									if text, ok := delta["text"].(string); ok && text != "" {
										ch <- NewTextDelta(text)
									}
								}
							}
						case "message_delta":
							if u, ok := event["usage"].(map[string]interface{}); ok {
								prompt := 0
								completion := 0
								if usage != nil {
									prompt = usage.PromptTokens
									completion = usage.CompletionTokens
								}
								if v, ok := u["input_tokens"].(float64); ok {
									prompt += int(v)
								}
								if v, ok := u["output_tokens"].(float64); ok {
									completion += int(v)
								}
								usage = &TokenUsage{PromptTokens: prompt, CompletionTokens: completion}
							}
						case "message_start":
							if msg, ok := event["message"].(map[string]interface{}); ok {
								if msgUsage, ok := msg["usage"].(map[string]interface{}); ok {
									prompt := 0
									if v, ok := msgUsage["input_tokens"].(float64); ok {
										prompt = int(v)
									}
									usage = &TokenUsage{PromptTokens: prompt, CompletionTokens: 0}
								}
							}
						}
					}
				}
			}
			if err != nil {
				break
			}
		}

		if usage != nil {
			ch <- NewUsageEvent(*usage)
		}
		ch <- NewDone()
	}()

	return ch, nil
}
