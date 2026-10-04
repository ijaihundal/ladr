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
	"fmt"
	"sort"
)

// DefaultPolicy is the default provider policy used when no override is given.
var DefaultPolicy = ProviderPolicy{
	FallbackEnabled:       true,
	MaxAttempts:           3,
	SingleProviderBackoff: true,
	BackoffBaseMs:         2000,
	BackoffMaxMs:          8000,
}

func normalizePolicy(policy *ProviderPolicy) ProviderPolicy {
	resolved := DefaultPolicy
	if policy == nil {
		return resolved
	}
	resolved.FallbackEnabled = policy.FallbackEnabled
	resolved.MaxAttempts = policy.MaxAttempts
	resolved.SingleProviderBackoff = policy.SingleProviderBackoff
	resolved.BackoffBaseMs = policy.BackoffBaseMs
	resolved.BackoffMaxMs = policy.BackoffMaxMs
	return resolved
}

func dedupeAttempts(attempts []ProviderConfig) []ProviderConfig {
	seen := make(map[string]struct{})
	var out []ProviderConfig
	for _, a := range attempts {
		key := fmt.Sprintf("%s:%s:%s", a.Provider, a.Model, a.BaseURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	return out
}

func singleAttemptChain(attempt ProviderConfig, policy *ProviderPolicy) ProviderChain {
	resolved := normalizePolicy(policy)
	return ProviderChain{
		Attempts:                 []ProviderConfig{attempt},
		MaxAttempts:              1,
		UseSingleProviderBackoff: resolved.SingleProviderBackoff,
		BackoffBaseMs:            resolved.BackoffBaseMs,
		BackoffMaxMs:             resolved.BackoffMaxMs,
	}
}

// ResolveProviders resolves the set of provider attempts for a model using
// the given allowed-targets policy and the user's connections.
func ResolveProviders(
	modelID string,
	allowedTargets AllowedTargetsPolicy,
	userConnections []ProviderConnection,
	policy *ProviderPolicy,
) (ProviderChain, error) {
	resolved := normalizePolicy(policy)
	matchingTargets := ResolveAllowedTargetsForModel(modelID, allowedTargets, userConnections)

	if len(matchingTargets) == 0 {
		if len(userConnections) == 0 {
			return ProviderChain{}, fmt.Errorf("No API key configured. Add your provider API key in Settings to continue.")
		}
		return ProviderChain{}, fmt.Errorf("Model %s is not supported by any of your configured providers. Try a different model or add a compatible provider in Settings.", modelID)
	}

	var attemptPool []ProviderConfig
	for _, target := range matchingTargets {
		var connection *ProviderConnection
		for i := range userConnections {
			if userConnections[i].Provider == target.Provider {
				connection = &userConnections[i]
				break
			}
		}
		if connection == nil {
			continue
		}
		apiKey := connection.APIKey
		if apiKey == "" {
			continue
		}
		attemptPool = append(attemptPool, ProviderConfig{
			Provider: target.Provider,
			Model:    target.Model,
			APIKey:   apiKey,
			BaseURL:  connection.BaseURL,
		})
	}

	prioritized := dedupeAttempts(attemptPool)
	priorityOf := func(provider string) int {
		for _, c := range userConnections {
			if c.Provider == provider {
				return c.Priority
			}
		}
		return 50
	}
	sort.SliceStable(prioritized, func(i, j int) bool {
		return priorityOf(prioritized[i].Provider) < priorityOf(prioritized[j].Provider)
	})

	if len(prioritized) == 0 {
		return ProviderChain{}, fmt.Errorf("No provider connection available for model %s", modelID)
	}

	requestedAttempts := resolved.MaxAttempts
	if requestedAttempts < 1 {
		requestedAttempts = 1
	}
	if requestedAttempts > 3 {
		requestedAttempts = 3
	}

	var attempts []ProviderConfig
	if len(prioritized) == 1 {
		for i := 0; i < requestedAttempts; i++ {
			attempts = append(attempts, prioritized[0])
		}
	} else {
		cap := len(prioritized)
		if !resolved.FallbackEnabled {
			cap = 1
		}
		take := requestedAttempts
		if take > cap {
			take = cap
		}
		attempts = prioritized[:take]
	}

	return ProviderChain{
		Attempts:                 attempts,
		MaxAttempts:              len(attempts),
		UseSingleProviderBackoff: len(prioritized) == 1 && resolved.SingleProviderBackoff,
		BackoffBaseMs:            resolved.BackoffBaseMs,
		BackoffMaxMs:             resolved.BackoffMaxMs,
	}, nil
}

func resolveCustomChain(
	modelID string,
	providerID string,
	connections []ProviderConnection,
	policy *ProviderPolicy,
) (ProviderChain, error) {
	var connection *ProviderConnection
	for i := range connections {
		if connections[i].Provider == providerID {
			connection = &connections[i]
			break
		}
	}
	if connection == nil {
		return ProviderChain{}, fmt.Errorf("Custom provider connection not found: %s", providerID)
	}
	apiKey := connection.APIKey
	if apiKey == "" {
		return ProviderChain{}, fmt.Errorf("Custom provider %s has no API key configured.", providerID)
	}
	if connection.BaseURL == "" {
		return ProviderChain{}, fmt.Errorf("Custom provider %s has no base URL configured.", providerID)
	}

	return singleAttemptChain(ProviderConfig{
		Provider: providerID,
		Model:    modelID,
		APIKey:   apiKey,
		BaseURL:  connection.BaseURL,
	}, policy), nil
}

func resolvePinnedBuiltinChain(
	modelID string,
	providerID string,
	connections []ProviderConnection,
	policy *ProviderPolicy,
) (ProviderChain, error) {
	var connection *ProviderConnection
	for i := range connections {
		if connections[i].Provider == providerID {
			connection = &connections[i]
			break
		}
	}
	if connection == nil {
		return ProviderChain{}, fmt.Errorf("Provider connection not found: %s", providerID)
	}
	apiKey := connection.APIKey
	if apiKey == "" {
		return ProviderChain{}, fmt.Errorf("Provider %s has no API key configured.", providerID)
	}

	resolvedModel := ResolveProviderModel(providerID, modelID)
	if IsOpenRouterModel(modelID) {
		resolvedModel = DeriveNativeModelId(modelID, providerID)
	}

	return singleAttemptChain(ProviderConfig{
		Provider: providerID,
		Model:    resolvedModel,
		APIKey:   apiKey,
		BaseURL:  connection.BaseURL,
	}, policy), nil
}

// ResolveChainOptions configures ResolveProviderChain.
type ResolveChainOptions struct {
	ProviderHint   string
	AllowedTargets *AllowedTargetsPolicy
	Policy         *ProviderPolicy
}

// ResolveProviderChain is the top-level entry point for provider resolution.
// It dispatches to custom chain, pinned builtin, or auto resolution.
func ResolveProviderChain(
	modelID string,
	connections []ProviderConnection,
	opts ResolveChainOptions,
) (ProviderChain, error) {
	providerHint := trimSpace(opts.ProviderHint)
	normalizedModel := trimSpace(modelID)

	if normalizedModel == "" {
		return ProviderChain{}, fmt.Errorf("A model is required.")
	}

	if providerHint != "" && providerHint != "auto" {
		if IsCustomProviderID(providerHint) {
			return resolveCustomChain(normalizedModel, providerHint, connections, opts.Policy)
		}
		return resolvePinnedBuiltinChain(normalizedModel, providerHint, connections, opts.Policy)
	}

	allowed := AllowedTargetsPolicy{Strategy: "universal"}
	if opts.AllowedTargets != nil {
		allowed = *opts.AllowedTargets
	}

	return ResolveProviders(normalizedModel, allowed, connections, opts.Policy)
}
