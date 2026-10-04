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

import "strings"

// orModelIDs holds the set of known OpenRouter model IDs.
var orModelIDs = map[string]struct{}{}

// SetOpenRouterModelIds replaces the set of known OpenRouter model IDs.
func SetOpenRouterModelIds(ids map[string]struct{}) {
	orModelIDs = make(map[string]struct{}, len(ids))
	for k := range ids {
		orModelIDs[k] = struct{}{}
	}
}

// IsOpenRouterModel returns true if the model ID is in the OR registry.
func IsOpenRouterModel(modelID string) bool {
	if modelID == "" {
		return false
	}
	_, ok := orModelIDs[modelID]
	return ok
}

// ClearOpenRouterModelIds empties the OR model registry.
func ClearOpenRouterModelIds() {
	orModelIDs = make(map[string]struct{})
}

// GetOpenRouterModelCount returns the number of OR models registered.
func GetOpenRouterModelCount() int {
	return len(orModelIDs)
}

// --- custom model helpers (from custom-models.ts) ---

// IsCustomModelId returns true if the model ID is not a catalog model
// and does not contain a colon.
func IsCustomModelId(modelID string) bool {
	if modelID == "" {
		return false
	}
	if strings.Contains(modelID, ":") {
		return false
	}
	return !IsCatalogModel(modelID)
}

// DeriveNativeModelId converts an OpenRouter slug to a native model ID.
// For openai/author models on the openai provider, the author prefix is stripped.
func DeriveNativeModelId(orSlug, providerID string) string {
	slashIdx := strings.Index(orSlug, "/")
	if slashIdx == -1 {
		return orSlug
	}
	author := orSlug[:slashIdx]
	if providerID == "openai" && author == "openai" {
		return orSlug[slashIdx+1:]
	}
	return orSlug
}
