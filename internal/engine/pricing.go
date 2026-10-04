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

// Package engine — spend calculation. Multiplies token usage by per-1M pricing
// to compute the dollar cost of a single LLM call. The pricing data comes from
// the OpenRouter catalog sync (populated in model_metadata / model_provider_pricing
// DB tables) or the in-memory OR cache.
//
// Formula: cost = prompt_tokens/1e6 * prompt_per1m + completion_tokens/1e6 * completion_per1m

package engine

import (
	"github.com/ladr/ladr/internal/provider"
)

// CalculateCallCost computes the dollar cost of an LLM call. Returns
// (cost, pricingFound). If pricing data is not available for the model
// (e.g. an unknown custom-provider model), pricingFound is false and cost
// is 0 — the caller should skip spend enforcement and fall back to the
// LLM-call-count ceiling.
//
// Lookup order:
//  1. In-memory OR catalog cache (fastest — populated by the background sync)
//  2. DB model_metadata table (comprehensive — persisted cache)
//
// Both sources price per 1M tokens. The model ID is the public ID as known
// to the catalog / OpenRouter (e.g. "openai/gpt-4o" or "gpt-oss-20b").
func CalculateCallCost(modelID string, usage *CallUsage) (cost float64, found bool) {
	if usage == nil || modelID == "" {
		return 0, false
	}

	promptPer1M, completionPer1M, ok := lookupPricing(modelID)
	if !ok {
		return 0, false
	}

	cost = float64(usage.PromptTokens)/1e6*promptPer1M +
		float64(usage.CompletionTokens)/1e6*completionPer1M
	return cost, true
}

// lookupPricing finds the per-1M prompt and completion price for a model.
// Checks the in-memory OR cache first, then returns. If the model is not in
// the cache, returns ok=false (the DB path will be wired in when the engine
// has direct DB access; for now the OR cache covers all synced models).
func lookupPricing(modelID string) (promptPer1M, completionPer1M float64, ok bool) {
	// Try the in-memory OR catalog cache.
	for _, entry := range provider.GetORCatalogCached() {
		if entry.ID == modelID {
			if entry.PromptPer1M != nil && entry.CompletionPer1M != nil {
				return *entry.PromptPer1M, *entry.CompletionPer1M, true
			}
			// Entry exists but pricing is nil (free model or missing data).
			if entry.PromptPer1M != nil && *entry.PromptPer1M == 0 &&
				entry.CompletionPer1M != nil && *entry.CompletionPer1M == 0 {
				return 0, 0, true // free model
			}
			return 0, 0, false
		}
	}

	// Also check the static catalog (4 hardcoded models).
	if cm := provider.GetCatalogModel(modelID); cm != nil && cm.Pricing != nil {
		pp, cp := 0.0, 0.0
		if cm.Pricing.PromptPer1M != nil {
			pp = *cm.Pricing.PromptPer1M
		}
		if cm.Pricing.CompletionPer1M != nil {
			cp = *cm.Pricing.CompletionPer1M
		}
		return pp, cp, true
	}

	return 0, 0, false
}
