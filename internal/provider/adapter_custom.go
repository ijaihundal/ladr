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
	"strings"

	"github.com/ladr/ladr/pkg/errors"
)

// CustomProviderPrefix is the prefix for user-defined custom provider IDs.
const CustomProviderPrefix = "custom_"

// IsCustomProviderID returns true if the provider ID starts with "custom_".
func IsCustomProviderID(providerID string) bool {
	return strings.HasPrefix(providerID, CustomProviderPrefix)
}

// ResolveCustomBaseURL returns the trimmed base URL or an error if missing.
func ResolveCustomBaseURL(config ProviderConfig) (string, error) {
	baseURL := trimSpace(config.BaseURL)
	baseURL = strings.TrimSuffix(baseURL, "/")
	if baseURL == "" {
		return "", errors.NewProviderError("Custom provider is missing a base URL", errors.ProviderErrorOptions{
			Provider:  "custom",
			Retryable: false,
		})
	}
	return baseURL, nil
}

// customAdapter implements ProviderAdapter for user-defined OpenAI-compatible providers.
type customAdapter struct{}

func (a *customAdapter) Name() string { return "custom" }

func (a *customAdapter) Call(ctx context.Context, model string, messages []Message, config ProviderConfig) (*AdapterResult, error) {
	baseURL, err := ResolveCustomBaseURL(config)
	if err != nil {
		return nil, err
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": toOpenAiMessages(messages),
	}
	if config.Temperature != nil {
		body["temperature"] = *config.Temperature
	}
	if config.MaxTokens != nil {
		body["max_tokens"] = *config.MaxTokens
	}
	bodyBytes, _ := json.Marshal(body)

	url := baseURL + "/chat/completions"
	status, respBody, err := ProviderFetchBytes(ctx, "custom", url, "POST", map[string]string{
		"content-type":  "application/json",
		"authorization": "Bearer " + config.APIKey,
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}

	if status < 200 || status >= 300 {
		return nil, errors.NewProviderError(
			fmt.Sprintf("Custom provider call failed (%d): %s", status, string(respBody)),
			errors.ProviderErrorOptions{
				Provider:   "custom",
				StatusCode: status,
				Retryable:  IsRetryableHttpStatus(status),
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
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return nil, errors.NewProviderError(fmt.Sprintf("Failed to parse response: %s", err.Error()), errors.ProviderErrorOptions{
			Provider: "custom",
		})
	}

	var content interface{}
	if len(payload.Choices) > 0 {
		content = payload.Choices[0].Message.Content
	}
	text, err := extractResponseText(content, "custom")
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

func (a *customAdapter) Stream(ctx context.Context, model string, messages []Message, config ProviderConfig) (<-chan StreamEvent, error) {
	baseURL, err := ResolveCustomBaseURL(config)
	if err != nil {
		return nil, err
	}

	body := map[string]interface{}{
		"model":          model,
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

	url := baseURL + "/chat/completions"
	resp, err := ProviderFetchStream(ctx, "custom", url, "POST", map[string]string{
		"content-type":  "application/json",
		"authorization": "Bearer " + config.APIKey,
	}, bodyBytes, config.TimeoutMs)
	if err != nil {
		return nil, err
	}

	return ParseOpenAiSSEStream(resp, 0), nil
}
