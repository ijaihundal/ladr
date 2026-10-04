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

// Ported from tests/unit/provider-resolver.test.ts.
func ptr[T any](v T) *T { return &v }

func TestResolveProviders_EnforcesAllowedTargetsAndPriority(t *testing.T) {
	chain, err := ResolveProviders(
		"gpt-oss-20b",
		AllowedTargetsPolicy{
			Strategy:  "constrained",
			Providers: []string{"openrouter", "deepinfra"},
			Models:    []string{"gpt-oss-20b"},
		},
		[]ProviderConnection{
			{Provider: "openrouter", APIKey: "sk-openrouter", Priority: 20},
			{Provider: "deepinfra", APIKey: "sk-deepinfra", Priority: 10},
		},
		&ProviderPolicy{FallbackEnabled: true, MaxAttempts: 3, SingleProviderBackoff: true},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chain.Attempts) != 2 {
		t.Fatalf("got %d attempts, want 2", len(chain.Attempts))
	}
	if chain.Attempts[0].Provider != "deepinfra" {
		t.Errorf("attempts[0].Provider = %q, want deepinfra (lower priority wins)", chain.Attempts[0].Provider)
	}
	if chain.Attempts[1].Provider != "openrouter" {
		t.Errorf("attempts[1].Provider = %q, want openrouter", chain.Attempts[1].Provider)
	}
}

func TestResolveProviders_SkipsUnsupportedProviderModelPairs(t *testing.T) {
	chain, err := ResolveProviders(
		"gpt-oss-20b",
		AllowedTargetsPolicy{
			Strategy:  "constrained",
			Providers: []string{"openai", "openrouter"},
			Models:    []string{"gpt-oss-20b"},
		},
		[]ProviderConnection{
			{Provider: "openai", APIKey: "sk-openai", Priority: 5},
			{Provider: "openrouter", APIKey: "sk-openrouter", Priority: 20},
		},
		&ProviderPolicy{FallbackEnabled: true, MaxAttempts: 3, SingleProviderBackoff: true},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// openai doesn't serve gpt-oss-20b; openrouter does and is repeated 3x (single provider backoff).
	if len(chain.Attempts) != 3 {
		t.Fatalf("got %d attempts, want 3", len(chain.Attempts))
	}
	for i, a := range chain.Attempts {
		if a.Provider != "openrouter" {
			t.Errorf("attempts[%d].Provider = %q, want openrouter", i, a.Provider)
		}
	}
}

func TestResolveProviders_RepeatsSingleProviderAttempts(t *testing.T) {
	chain, err := ResolveProviders(
		"gpt-oss-120b",
		AllowedTargetsPolicy{
			Strategy:  "constrained",
			Providers: []string{"deepinfra"},
			Models:    []string{"gpt-oss-120b"},
		},
		[]ProviderConnection{
			{Provider: "deepinfra", APIKey: "sk-deep", Priority: 10},
		},
		&ProviderPolicy{FallbackEnabled: true, MaxAttempts: 3, SingleProviderBackoff: true},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chain.Attempts) != 3 {
		t.Fatalf("got %d attempts, want 3", len(chain.Attempts))
	}
	if !chain.UseSingleProviderBackoff {
		t.Error("UseSingleProviderBackoff = false, want true")
	}
	for i, a := range chain.Attempts {
		if a.Provider != "deepinfra" {
			t.Errorf("attempts[%d].Provider = %q, want deepinfra", i, a.Provider)
		}
	}
}

func TestResolveProviders_ErrorsWhenNoBYOKConfigured(t *testing.T) {
	_, err := ResolveProviders(
		"gpt-oss-20b",
		AllowedTargetsPolicy{Strategy: "universal"},
		nil,
		&ProviderPolicy{FallbackEnabled: true, MaxAttempts: 3, SingleProviderBackoff: true},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "No API key configured") {
		t.Errorf("error = %q, want it to contain 'No API key configured'", err.Error())
	}
}

func TestResolveProviders_ErrorsWhenProviderDoesNotSupportModel(t *testing.T) {
	_, err := ResolveProviders(
		"gpt-oss-20b",
		AllowedTargetsPolicy{Strategy: "universal"},
		[]ProviderConnection{
			{Provider: "anthropic", APIKey: "sk-ant", Priority: 1},
		},
		&ProviderPolicy{FallbackEnabled: true, MaxAttempts: 3, SingleProviderBackoff: true},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not supported by any of your configured providers") {
		t.Errorf("error = %q, want it to contain 'not supported by any of your configured providers'", err.Error())
	}
}
