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

// authorNativeProvider maps OR author prefixes to native provider IDs.
// Used for routing custom (non-catalog) OpenRouter models.
var authorNativeProvider = map[string]string{
	"openai": "openai",
}

func normalizeUserProviders(connections []ProviderConnection) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, c := range connections {
		p := trimSpace(c.Provider)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// IsModelAllowedByPolicy returns true if the model is permitted under the
// given AllowedTargetsPolicy.
func IsModelAllowedByPolicy(modelID string, policy AllowedTargetsPolicy) bool {
	if policy.Strategy == "universal" {
		return true
	}
	for _, m := range policy.Models {
		if m == "*" || m == modelID {
			return true
		}
	}
	// Custom (non-catalog) models are the user's own — always allowed past
	// ladder allowlists.
	if !IsCatalogModel(modelID) {
		return true
	}
	return false
}

// ResolveAllowedTargetsForModel returns the list of provider+model targets
// that are available and permitted for the given model under the policy.
func ResolveAllowedTargetsForModel(
	modelID string,
	policy AllowedTargetsPolicy,
	userConnections []ProviderConnection,
) []AllowedTarget {
	if modelID == "" {
		return nil
	}
	userProviders := normalizeUserProviders(userConnections)
	if len(userProviders) == 0 {
		return nil
	}
	if !IsModelAllowedByPolicy(modelID, policy) {
		return nil
	}

	isCatalog := IsCatalogModel(modelID)

	// Custom (non-catalog) models bypass the provider allowlist and route
	// only to the user's custom provider connections.  If the model is a
	// known OR model and the user has an openrouter connection, route
	// through that as well.
	if !isCatalog {
		var targets []AllowedTarget
		for _, p := range userProviders {
			if IsCustomProviderID(p) {
				targets = append(targets, AllowedTarget{Provider: p, Model: modelID})
			}
		}
		if IsOpenRouterModel(modelID) {
			if containsString(userProviders, "openrouter") {
				targets = append(targets, AllowedTarget{Provider: "openrouter", Model: modelID})
			}
			slashIdx := indexByte(modelID, '/')
			if slashIdx != -1 {
				author := modelID[:slashIdx]
				if nativeProvider, ok := authorNativeProvider[author]; ok && containsString(userProviders, nativeProvider) {
					targets = append(targets, AllowedTarget{
						Provider: nativeProvider,
						Model:    DeriveNativeModelId(modelID, nativeProvider),
					})
				}
			}
		}
		return targets
	}

	// Catalog model — filter by policy providers and catalog support.
	var providers []string
	if policy.Strategy == "universal" {
		providers = append(providers, userProviders...)
	} else if containsString(policy.Providers, "*") {
		providers = append(providers, userProviders...)
	} else {
		for _, p := range userProviders {
			if containsString(policy.Providers, p) {
				providers = append(providers, p)
			}
		}
	}

	var targets []AllowedTarget
	for _, p := range providers {
		if SupportsProviderModel(p, modelID) {
			targets = append(targets, AllowedTarget{Provider: p, Model: modelID})
		}
	}
	return targets
}

func containsString(slice []string, v string) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
