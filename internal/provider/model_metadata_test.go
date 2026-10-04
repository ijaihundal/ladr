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
	"testing"
)

// Ported from tests/unit/model-metadata.test.ts.
// NOTE: 10 of 20 TS test cases exercise ReconcileModelMetadata,
// BuildBuiltinModelOptions, and MatchModelToOrSlug — functions that have
// no Go equivalent (model-sync/discovery feature gap). Only the portable
// subset (DeriveNativeModelId + OR registry + ResolveAllowedTargetsForModel
// routing) is covered here.

// --- OR model registry ---

func TestIsOpenRouterModel_Registry(t *testing.T) {
	ClearOpenRouterModelIds()
	if IsOpenRouterModel("anthropic/claude-sonnet-4") {
		t.Error("expected false before set")
	}
	SetOpenRouterModelIds(map[string]struct{}{
		"anthropic/claude-sonnet-4": {},
		"openai/gpt-4o":             {},
	})
	if !IsOpenRouterModel("anthropic/claude-sonnet-4") {
		t.Error("expected true after set")
	}
	if !IsOpenRouterModel("openai/gpt-4o") {
		t.Error("expected true after set")
	}
	if IsOpenRouterModel("unknown/model") {
		t.Error("expected false for unregistered")
	}
	if IsOpenRouterModel("") {
		t.Error("expected false for empty")
	}
	ClearOpenRouterModelIds()
}

// --- DeriveNativeModelId ---

func TestDeriveNativeModelId_StripsPrefixForOpenAI(t *testing.T) {
	cases := []struct{ slug, provider, want string }{
		{"openai/gpt-4o", "openai", "gpt-4o"},
		{"openai/gpt-4o-mini", "openai", "gpt-4o-mini"},
	}
	for _, c := range cases {
		if got := DeriveNativeModelId(c.slug, c.provider); got != c.want {
			t.Errorf("DeriveNativeModelId(%q,%q) = %q, want %q", c.slug, c.provider, got, c.want)
		}
	}
}

func TestDeriveNativeModelId_FullSlugForNonOpenAI(t *testing.T) {
	cases := []struct{ slug, provider, want string }{
		{"openai/gpt-4o", "openrouter", "openai/gpt-4o"},
		{"openai/gpt-4o", "deepinfra", "openai/gpt-4o"},
		{"anthropic/claude-sonnet-4", "openrouter", "anthropic/claude-sonnet-4"},
	}
	for _, c := range cases {
		if got := DeriveNativeModelId(c.slug, c.provider); got != c.want {
			t.Errorf("DeriveNativeModelId(%q,%q) = %q, want %q", c.slug, c.provider, got, c.want)
		}
	}
}

func TestDeriveNativeModelId_NoSlash(t *testing.T) {
	cases := []struct{ slug, provider, want string }{
		{"gpt-4o", "openai", "gpt-4o"},
		{"some-model", "openrouter", "some-model"},
	}
	for _, c := range cases {
		if got := DeriveNativeModelId(c.slug, c.provider); got != c.want {
			t.Errorf("DeriveNativeModelId(%q,%q) = %q, want %q", c.slug, c.provider, got, c.want)
		}
	}
}

// --- Routing: ResolveAllowedTargetsForModel ---

func TestResolveAllowedTargets_ORModelRoutesThroughOpenRouter(t *testing.T) {
	ClearOpenRouterModelIds()
	SetOpenRouterModelIds(map[string]struct{}{"anthropic/claude-sonnet-4": {}})
	defer ClearOpenRouterModelIds()

	conns := []ProviderConnection{{Provider: "openrouter", APIKey: "sk-test", Priority: 50}}
	targets := ResolveAllowedTargetsForModel("anthropic/claude-sonnet-4",
		AllowedTargetsPolicy{Strategy: "universal"}, conns)

	var orTarget *AllowedTarget
	for i := range targets {
		if targets[i].Provider == "openrouter" {
			orTarget = &targets[i]
		}
	}
	if orTarget == nil {
		t.Fatal("no openrouter target")
	}
	if orTarget.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("openrouter model = %q", orTarget.Model)
	}
}

func TestResolveAllowedTargets_ORModelNoOpenRouterConnection(t *testing.T) {
	ClearOpenRouterModelIds()
	SetOpenRouterModelIds(map[string]struct{}{"anthropic/claude-sonnet-4": {}})
	defer ClearOpenRouterModelIds()

	conns := []ProviderConnection{{Provider: "vertex", APIKey: "test", Priority: 50}}
	targets := ResolveAllowedTargetsForModel("anthropic/claude-sonnet-4",
		AllowedTargetsPolicy{Strategy: "universal"}, conns)

	for _, tg := range targets {
		if tg.Provider == "openrouter" {
			t.Error("openrouter should not be a target without a connection")
		}
	}
}

func TestResolveAllowedTargets_CatalogModelRoutesThroughConfiguredProviders(t *testing.T) {
	conns := []ProviderConnection{{Provider: "openrouter", APIKey: "sk-test", Priority: 50}}
	targets := ResolveAllowedTargetsForModel("gpt-oss-20b",
		AllowedTargetsPolicy{Strategy: "universal"}, conns)

	found := false
	for _, tg := range targets {
		if tg.Provider == "openrouter" {
			found = true
		}
	}
	if !found {
		t.Error("catalog model should route through openrouter (configured)")
	}
}

func TestResolveAllowedTargets_UnknownNonORModelRoutesOnlyToCustom(t *testing.T) {
	ClearOpenRouterModelIds()
	conns := []ProviderConnection{
		{Provider: "openrouter", APIKey: "sk-test", Priority: 50},
		{Provider: "custom_abc", APIKey: "sk-test", Priority: 50, BaseURL: "https://example.com/v1", IsCustom: true},
	}
	targets := ResolveAllowedTargetsForModel("some-unknown-model",
		AllowedTargetsPolicy{Strategy: "universal"}, conns)

	hasCustom := false
	hasOR := false
	for _, tg := range targets {
		if tg.Provider == "custom_abc" {
			hasCustom = true
		}
		if tg.Provider == "openrouter" {
			hasOR = true
		}
	}
	if !hasCustom {
		t.Error("unknown model should route to custom connection")
	}
	if hasOR {
		t.Error("unknown non-OR model should NOT route to openrouter")
	}
}

func TestResolveAllowedTargets_ORModelOpenAIAuthorRoutesNatively(t *testing.T) {
	ClearOpenRouterModelIds()
	SetOpenRouterModelIds(map[string]struct{}{"openai/gpt-4o": {}})
	defer ClearOpenRouterModelIds()

	conns := []ProviderConnection{
		{Provider: "openai", APIKey: "sk-openai", Priority: 50},
		{Provider: "openrouter", APIKey: "sk-or", Priority: 60},
	}
	targets := ResolveAllowedTargetsForModel("openai/gpt-4o",
		AllowedTargetsPolicy{Strategy: "universal"}, conns)

	var orTarget, openaiTarget *AllowedTarget
	for i := range targets {
		switch targets[i].Provider {
		case "openrouter":
			orTarget = &targets[i]
		case "openai":
			openaiTarget = &targets[i]
		}
	}
	if orTarget == nil {
		t.Fatal("no openrouter target")
	}
	if orTarget.Model != "openai/gpt-4o" {
		t.Errorf("openrouter model = %q, want full slug", orTarget.Model)
	}
	if openaiTarget == nil {
		t.Fatal("no openai native target")
	}
	if openaiTarget.Model != "gpt-4o" {
		t.Errorf("openai model = %q, want stripped id 'gpt-4o'", openaiTarget.Model)
	}
}

func TestResolveAllowedTargets_ORModelNonOpenAIAuthorNoOpenAITarget(t *testing.T) {
	ClearOpenRouterModelIds()
	SetOpenRouterModelIds(map[string]struct{}{"anthropic/claude-sonnet-4": {}})
	defer ClearOpenRouterModelIds()

	conns := []ProviderConnection{
		{Provider: "openai", APIKey: "sk-openai", Priority: 50},
		{Provider: "openrouter", APIKey: "sk-or", Priority: 60},
	}
	targets := ResolveAllowedTargetsForModel("anthropic/claude-sonnet-4",
		AllowedTargetsPolicy{Strategy: "universal"}, conns)

	hasOR := false
	hasOpenAI := false
	for _, tg := range targets {
		switch tg.Provider {
		case "openrouter":
			hasOR = true
		case "openai":
			hasOpenAI = true
		}
	}
	if !hasOR {
		t.Error("openrouter should be a target")
	}
	if hasOpenAI {
		t.Error("openai should NOT be a target for anthropic model")
	}
}
