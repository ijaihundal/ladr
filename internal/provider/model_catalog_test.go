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

import "testing"

// Ported from tests/unit/provider-model-catalog.test.ts.
func TestResolveProviderModel_CatalogMappings(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     string
	}{
		{"fireworks", "gpt-oss-120b", "accounts/fireworks/models/gpt-oss-120b"},
		{"nebius", "gpt-oss-20b", "nebius/openai/gpt-oss-20b"},
		{"openrouter", "gemini-3-flash-preview", "google/gemini-3-flash-preview"},
	}
	for _, c := range cases {
		t.Run(c.provider+"/"+c.model, func(t *testing.T) {
			if got := ResolveProviderModel(c.provider, c.model); got != c.want {
				t.Fatalf("ResolveProviderModel(%q,%q) = %q, want %q", c.provider, c.model, got, c.want)
			}
		})
	}
}

func TestIsCatalogModel_AndSupportsProviderModel(t *testing.T) {
	if !IsCatalogModel("gpt-oss-20b") {
		t.Error("IsCatalogModel(gpt-oss-20b) = false, want true")
	}
	if !SupportsProviderModel("openrouter", "gpt-oss-20b") {
		t.Error("SupportsProviderModel(openrouter, gpt-oss-20b) = false, want true")
	}
	if SupportsProviderModel("openai", "gpt-oss-20b") {
		t.Error("SupportsProviderModel(openai, gpt-oss-20b) = true, want false")
	}
}

func TestResolveProviderModel_UnknownModelsPassthrough(t *testing.T) {
	if IsCatalogModel("gpt-4o") {
		t.Error("IsCatalogModel(gpt-4o) = true, want false")
	}
	if !SupportsProviderModel("openai", "gpt-4o") {
		t.Error("SupportsProviderModel(openai, gpt-4o) = false, want true (non-catalog passes through)")
	}
	if got := ResolveProviderModel("openai", "gpt-4o"); got != "gpt-4o" {
		t.Fatalf("ResolveProviderModel(openai, gpt-4o) = %q, want gpt-4o", got)
	}
}
