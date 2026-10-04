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

// Ported from tests/unit/author-map.test.ts.
func TestParseAuthor(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"x-ai/grok-4", "x-ai"},
		{"openai/gpt-4o", "openai"},
		{"deepseek/deepseek-chat", "deepseek"},
		{"noid", ""},
		{"/leading-slash", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			if got := ParseAuthor(c.slug); got != c.want {
				t.Fatalf("ParseAuthor(%q) = %q, want %q", c.slug, got, c.want)
			}
		})
	}
}

// The regression this guards: Grok used to show only OpenRouter because the old
// inline map only knew openai/anthropic/google. x-ai MUST resolve to xai.
func TestDeriveNativeProvider_KnownAuthors(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"x-ai/grok-4", "xai"},
		{"deepseek/deepseek-chat", "deepseek"},
		{"mistralai/mistral-large-2411", "mistral"},
		{"cohere/command-r-plus-08-2024", "cohere"},
		{"perplexity/sonar-pro", "perplexity"},
		{"moonshotai/kimi-k2", "moonshot"},
		{"nvidia/llama-3.1-nemotron-70b-instruct", "nvidia"},
		{"ai21/jamba-1.6-large", "ai21"},
		{"openai/gpt-4o", "openai"},
		{"anthropic/claude-sonnet-4", "anthropic"},
		{"google/gemini-2.0-flash-001", "google_ai_studio"},
	}
	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			if got := DeriveNativeProvider(c.slug); got != c.want {
				t.Fatalf("DeriveNativeProvider(%q) = %q, want %q", c.slug, got, c.want)
			}
		})
	}
}

// Meta / Qwen / Microsoft make models but expose no direct BYOK catalog API
// here — those models are reached via hosts/aggregators the user connects.
func TestDeriveNativeProvider_UnmappedAuthors(t *testing.T) {
	cases := []string{
		"meta-llama/llama-3.3-70b-instruct",
		"qwen/qwen-2.5-72b-instruct",
		"microsoft/phi-4",
		"unknown-author/some-model",
		"",
	}
	for _, slug := range cases {
		t.Run(slug, func(t *testing.T) {
			if got := DeriveNativeProvider(slug); got != "" {
				t.Fatalf("DeriveNativeProvider(%q) = %q, want empty", slug, got)
			}
		})
	}
}

func TestGetAuthorProviderMap(t *testing.T) {
	m := GetAuthorProviderMap()
	if m == nil {
		t.Fatal("map is nil")
	}
	if m["x-ai"] != "xai" {
		t.Errorf("map[x-ai] = %q, want xai", m["x-ai"])
	}
	if m["deepseek"] != "deepseek" {
		t.Errorf("map[deepseek] = %q, want deepseek", m["deepseek"])
	}
}
