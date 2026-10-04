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
	"strings"
	"testing"
)

// Ported from tests/unit/custom-providers.test.ts.
// NOTE: 3 of 13 TS test cases (listAvailableModels + fetchProviderModels)
// exercise HTTP model-discovery that has no Go equivalent — those are skipped
// (feature gap, not test gap).

const customTestID = "custom_11111111-1111-4111-8111-111111111111"

func customConn() ProviderConnection {
	return ProviderConnection{
		Provider: customTestID,
		APIKey:   "sk-custom",
		Priority: 10,
		BaseURL:  "https://my-llm.example.com/v1",
		Label:    ptr("My LLM"),
		IsCustom: true,
	}
}

func TestIsCustomProviderID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{customTestID, true},
		{"custom_", true},
		{"openai", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsCustomProviderID(c.id); got != c.want {
			t.Errorf("IsCustomProviderID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestIsCustomModelID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"gpt-4o", true},
		{"anything-else", true},
		{"gpt-oss-20b", false},
		{"gpt-oss-120b", false},
	}
	for _, c := range cases {
		if got := IsCustomModelId(c.id); got != c.want {
			t.Errorf("IsCustomModelId(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestResolveProviderChain_PinsToCustomProvider(t *testing.T) {
	chain, err := ResolveProviderChain("gpt-4o", []ProviderConnection{customConn()}, ResolveChainOptions{
		ProviderHint: customTestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Attempts) != 1 {
		t.Fatalf("got %d attempts, want 1", len(chain.Attempts))
	}
	a := chain.Attempts[0]
	if a.Provider != customTestID {
		t.Errorf("provider = %q, want %q", a.Provider, customTestID)
	}
	if a.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", a.Model)
	}
	if a.BaseURL != "https://my-llm.example.com/v1" {
		t.Errorf("baseURL = %q", a.BaseURL)
	}
	if a.APIKey != "sk-custom" {
		t.Errorf("apiKey = %q", a.APIKey)
	}
}

func TestResolveProviderChain_MissingCustomConnection(t *testing.T) {
	_, err := ResolveProviderChain("gpt-4o", nil, ResolveChainOptions{ProviderHint: customTestID})
	if err == nil || !strings.Contains(err.Error(), "Custom provider connection not found") {
		t.Errorf("err = %v, want 'Custom provider connection not found'", err)
	}
}

func TestResolveProviderChain_CustomNoBaseURL(t *testing.T) {
	conn := customConn()
	conn.BaseURL = ""
	_, err := ResolveProviderChain("gpt-4o", []ProviderConnection{conn}, ResolveChainOptions{ProviderHint: customTestID})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "no base url") {
		t.Errorf("err = %v, want it to contain 'no base URL'", err)
	}
}

func TestResolveProviderChain_PinsBuiltinAndMapsModel(t *testing.T) {
	chain, err := ResolveProviderChain("gpt-oss-120b",
		[]ProviderConnection{{Provider: "deepinfra", APIKey: "sk-deep", Priority: 10}},
		ResolveChainOptions{ProviderHint: "deepinfra"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Attempts) != 1 {
		t.Fatalf("got %d attempts, want 1", len(chain.Attempts))
	}
	a := chain.Attempts[0]
	if a.Provider != "deepinfra" {
		t.Errorf("provider = %q, want deepinfra", a.Provider)
	}
	if a.Model != "openai/gpt-oss-120b" {
		t.Errorf("model = %q, want openai/gpt-oss-120b", a.Model)
	}
}

func TestResolveProviderChain_AutoExcludesBuiltinForCustomModel(t *testing.T) {
	connections := []ProviderConnection{
		{Provider: "openai", APIKey: "sk-openai", Priority: 1},
		{Provider: "anthropic", APIKey: "sk-ant", Priority: 2},
		{Provider: customTestID, APIKey: "sk-custom", Priority: 3, BaseURL: "https://my-llm.example.com/v1", IsCustom: true},
	}
	chain, err := ResolveProviderChain("gpt-4o", connections, ResolveChainOptions{
		AllowedTargets: &AllowedTargetsPolicy{Strategy: "universal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Attempts) < 1 {
		t.Fatal("no attempts")
	}
	for _, a := range chain.Attempts {
		if a.Provider != customTestID {
			t.Errorf("auto mode for custom model routed to builtin %q; want only custom", a.Provider)
		}
	}
	if chain.Attempts[0].BaseURL != "https://my-llm.example.com/v1" {
		t.Errorf("baseURL = %q", chain.Attempts[0].BaseURL)
	}
}

func TestIsModelAllowedByPolicy_CustomBypassesConstrained(t *testing.T) {
	policy := AllowedTargetsPolicy{
		Strategy:  "constrained",
		Providers: []string{"openrouter"},
		Models:    []string{"gpt-oss-20b"},
	}
	if !IsModelAllowedByPolicy("gpt-oss-20b", policy) {
		t.Error("gpt-oss-20b should be allowed")
	}
	if !IsModelAllowedByPolicy("gpt-4o", policy) {
		t.Error("custom model gpt-4o should bypass constrained allowlist")
	}
	if !IsModelAllowedByPolicy("claude-opus-4", policy) {
		t.Error("custom model claude-opus-4 should bypass constrained allowlist")
	}
}

func TestResolveProviderChain_CustomModelBypassesProviderAllowlist(t *testing.T) {
	policy := AllowedTargetsPolicy{
		Strategy:  "constrained",
		Providers: []string{"openrouter"},
		Models:    []string{"gpt-oss-20b"},
	}
	conn := customConn()
	conn.Priority = 5
	chain, err := ResolveProviderChain("claude-opus-4", []ProviderConnection{conn}, ResolveChainOptions{
		AllowedTargets: &policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Attempts) < 1 {
		t.Fatal("no attempts")
	}
	for _, a := range chain.Attempts {
		if a.Provider != customTestID {
			t.Errorf("provider = %q, want custom", a.Provider)
		}
	}
	a := chain.Attempts[0]
	if a.Model != "claude-opus-4" {
		t.Errorf("model = %q, want claude-opus-4", a.Model)
	}
	if a.BaseURL != "https://my-llm.example.com/v1" {
		t.Errorf("baseURL = %q", a.BaseURL)
	}
}

func TestGetAdapter_FallsBackToCustom(t *testing.T) {
	adapter, err := GetAdapter(customTestID)
	if err != nil {
		t.Fatalf("GetAdapter(custom): %v", err)
	}
	if adapter.Name() != "custom" {
		t.Errorf("name = %q, want 'custom'", adapter.Name())
	}
	if _, err := GetAdapter("totally_unknown_provider"); err == nil {
		t.Error("GetAdapter(unknown) should error")
	}
}

func TestResolveProviders_CarriesBaseURLForBuiltin(t *testing.T) {
	chain, err := ResolveProviders(
		"gpt-oss-20b",
		AllowedTargetsPolicy{Strategy: "constrained", Providers: []string{"openrouter"}, Models: []string{"gpt-oss-20b"}},
		[]ProviderConnection{{Provider: "openrouter", APIKey: "sk-or", Priority: 10, BaseURL: "https://openrouter.example/v1"}},
		&ProviderPolicy{FallbackEnabled: true, MaxAttempts: 3, SingleProviderBackoff: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Attempts[0].BaseURL != "https://openrouter.example/v1" {
		t.Errorf("baseURL = %q", chain.Attempts[0].BaseURL)
	}
}
