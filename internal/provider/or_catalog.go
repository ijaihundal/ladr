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

// Package provider — OpenRouter live catalog fetcher.
//
// Fetches model metadata + pricing from OpenRouter's public /api/v1/models
// endpoint (no API key required). OpenRouter aggregates 400+ models across all
// major providers (OpenAI, Anthropic, Google, DeepSeek, open-weight models),
// making it the single best machine-readable source for cross-provider pricing.
//
// Pricing comes from OpenRouter as per-token decimal strings (e.g.
// "0.0000025" = $2.50/1M). We convert to per-1M on intake to match our DB
// schema (numeric(14,6) per 1M tokens) and the frontend's expected field
// names (pricingPromptPer1m, pricingCompletionPer1m).

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ORModelsURL is the public OpenRouter models endpoint (no auth required).
const ORModelsURL = "https://openrouter.ai/api/v1/models"

// ORCatalogTTL is how long the in-memory cache is valid before a refresh.
const ORCatalogTTL = 5 * time.Minute

// ORModelEntry is a single model's metadata from OpenRouter's /models API.
type ORModelEntry struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	ContextLength int    `json:"context_length"`
	Architecture struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	Pricing struct {
		Prompt         string `json:"prompt"`
		Completion     string `json:"completion"`
		InputCacheRead string `json:"input_cache_read"`
		Image          string `json:"image"`
		WebSearch      string `json:"web_search"`
	} `json:"pricing"`
	TopProvider struct {
		ContextLength        int  `json:"context_length"`
		MaxCompletionTokens  *int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	SupportedParameters []string `json:"supported_parameters"`
}

// orCatalogResponse is the top-level shape of OpenRouter's /models response.
type orCatalogResponse struct {
	Data []ORModelEntry `json:"data"`
}

// ORModelMetadata is the normalized metadata for one model after conversion.
// Prices are per-1M tokens (USD). Nil means the field was absent in the OR
// response (e.g. free models, or models without image pricing).
type ORModelMetadata struct {
	ID                string
	Label             string
	Description       string
	ContextLength     int
	MaxOutput         *int
	InputModalities   []string
	OutputModalities  []string
	PromptPer1M       *float64
	CompletionPer1M   *float64
	ImagePer1k        *float64
	CacheReadPer1M    *float64
	SupportedParameters []string
}

// --- in-memory cache ---

var (
	orCacheMu       sync.RWMutex
	orCache         []ORModelMetadata
	orCacheTime     time.Time
	orCacheInFlight sync.Mutex
)

// FetchORCatalog fetches the full OpenRouter model catalog (cached for
// ORCatalogTTL). Returns a slice of normalized metadata entries. If the cache
// is fresh, no network call is made. The OpenRouter /models endpoint is public
// — no API key is required. An optional apiKey (for the connected OpenRouter
// connection) raises rate limits but is not necessary for correctness.
func FetchORCatalog(ctx context.Context, apiKey string) ([]ORModelMetadata, error) {
	orCacheMu.RLock()
	if time.Since(orCacheTime) < ORCatalogTTL && len(orCache) > 0 {
		result := orCache
		orCacheMu.RUnlock()
		return result, nil
	}
	orCacheMu.RUnlock()

	// In-flight dedup: only one goroutine fetches at a time.
	orCacheInFlight.Lock()
	defer orCacheInFlight.Unlock()

	// Double-check after acquiring the lock (another goroutine may have refreshed).
	orCacheMu.RLock()
	if time.Since(orCacheTime) < ORCatalogTTL && len(orCache) > 0 {
		result := orCache
		orCacheMu.RUnlock()
		return result, nil
	}
	orCacheMu.RUnlock()

	entries, err := fetchORCatalogUncached(ctx, apiKey)
	if err != nil {
		// If we have stale cache data, serve it rather than failing.
		orCacheMu.RLock()
		if len(orCache) > 0 {
			result := orCache
			orCacheMu.RUnlock()
			return result, nil
		}
		orCacheMu.RUnlock()
		return nil, err
	}

	orCacheMu.Lock()
	orCache = entries
	orCacheTime = time.Now()
	orCacheMu.Unlock()

	return entries, nil
}

// fetchORCatalogUncached calls OpenRouter's /models endpoint and parses the
// response into normalized metadata.
func fetchORCatalogUncached(ctx context.Context, apiKey string) ([]ORModelMetadata, error) {
	headers := map[string]string{
		"Accept": "application/json",
	}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}

	resp, err := ProviderFetch(ctx, "openrouter", ORModelsURL, "GET", headers, nil, 30000)
	if err != nil {
		return nil, fmt.Errorf("openrouter catalog fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openrouter catalog fetch failed (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024)) // 50MB max
	if err != nil {
		return nil, fmt.Errorf("openrouter catalog read: %w", err)
	}

	var payload orCatalogResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("openrouter catalog parse: %w", err)
	}

	entries := make([]ORModelMetadata, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		entries = append(entries, normalizeOREntry(m))
	}
	return entries, nil
}

// normalizeOREntry converts a raw OR API entry to our normalized metadata,
// translating per-token prices to per-1M.
func normalizeOREntry(m ORModelEntry) ORModelMetadata {
	meta := ORModelMetadata{
		ID:                  m.ID,
		Label:               m.Name,
		Description:         m.Description,
		ContextLength:       m.ContextLength,
		MaxOutput:           m.TopProvider.MaxCompletionTokens,
		InputModalities:     m.Architecture.InputModalities,
		OutputModalities:    m.Architecture.OutputModalities,
		SupportedParameters: m.SupportedParameters,
	}

	// If top_provider has a different context length, prefer it (more specific).
	if m.TopProvider.ContextLength > 0 {
		meta.ContextLength = m.TopProvider.ContextLength
	}

	// Convert per-token to per-1M. OpenRouter returns decimal strings.
	meta.PromptPer1M = perTokenToPer1M(m.Pricing.Prompt)
	meta.CompletionPer1M = perTokenToPer1M(m.Pricing.Completion)
	meta.ImagePer1k = perTokenToPer1K(m.Pricing.Image)
	meta.CacheReadPer1M = perTokenToPer1M(m.Pricing.InputCacheRead)

	return meta
}

// perTokenToPer1M converts a per-token decimal string (e.g. "0.0000025")
// to a per-1M-token float64 (e.g. 2.5). Returns nil for empty/unparseable input.
// "0" is a valid value (free model) and returns &0.0.
func perTokenToPer1M(perToken string) *float64 {
	perToken = strings.TrimSpace(perToken)
	if perToken == "" {
		return nil
	}
	v, err := strconv.ParseFloat(perToken, 64)
	if err != nil {
		return nil
	}
	per1m := v * 1_000_000
	return &per1m
}

// perTokenToPer1K converts a per-token decimal string to per-1K (for image pricing).
func perTokenToPer1K(perToken string) *float64 {
	perToken = strings.TrimSpace(perToken)
	if perToken == "" {
		return nil
	}
	v, err := strconv.ParseFloat(perToken, 64)
	if err != nil {
		return nil
	}
	per1k := v * 1_000
	return &per1k
}

// GetORCatalogCached returns the cached catalog without triggering a fetch.
// Returns nil if the cache is empty or stale — use FetchORCatalog to populate.
func GetORCatalogCached() []ORModelMetadata {
	orCacheMu.RLock()
	defer orCacheMu.RUnlock()
	if time.Since(orCacheTime) >= ORCatalogTTL {
		return nil
	}
	return orCache
}

// IsORCatalogFresh reports whether the in-memory cache is populated and fresh.
func IsORCatalogFresh() bool {
	orCacheMu.RLock()
	defer orCacheMu.RUnlock()
	return len(orCache) > 0 && time.Since(orCacheTime) < ORCatalogTTL
}
