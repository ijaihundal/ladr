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

func TestPerTokenToPer1M(t *testing.T) {
	tests := []struct {
		input   string
		want    float64
		wantNil bool
	}{
		{"0.0000025", 2.5, false},     // GPT-4o input: $2.50/1M
		{"0.00001", 10.0, false},      // GPT-4o output: $10/1M
		{"0.000005", 5.0, false},      // Claude Opus input: $5/1M
		{"0", 0, false},               // Free model
		{"", 0, true},                 // Missing → nil
		{"not-a-number", 0, true},     // Unparseable → nil
	}
	for _, tt := range tests {
		got := perTokenToPer1M(tt.input)
		if tt.wantNil {
			if got != nil {
				t.Errorf("perTokenToPer1M(%q) = %v, want nil", tt.input, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("perTokenToPer1M(%q) = nil, want %v", tt.input, tt.want)
			continue
		}
		if abs(*got-tt.want) > 1e-9 {
			t.Errorf("perTokenToPer1M(%q) = %v, want %v", tt.input, *got, tt.want)
		}
	}
}

func TestPerTokenToPer1K(t *testing.T) {
	got := perTokenToPer1K("0.00004")
	if got == nil || abs(*got-0.04) > 1e-9 {
		t.Errorf("perTokenToPer1K(0.00004) = %v, want 0.04", got)
	}
	if perTokenToPer1K("") != nil {
		t.Error("empty string should return nil")
	}
}

func TestNormalizeOREntry(t *testing.T) {
	raw := ORModelEntry{
		ID:            "openai/gpt-4o",
		Name:          "OpenAI: GPT-4o",
		Description:   "multimodal flagship",
		ContextLength: 128000,
		Architecture: struct {
			InputModalities  []string `json:"input_modalities"`
			OutputModalities []string `json:"output_modalities"`
		}{
			InputModalities:  []string{"text", "image"},
			OutputModalities: []string{"text"},
		},
		Pricing: struct {
			Prompt         string `json:"prompt"`
			Completion     string `json:"completion"`
			InputCacheRead string `json:"input_cache_read"`
			Image          string `json:"image"`
			WebSearch      string `json:"web_search"`
		}{
			Prompt:         "0.0000025",    // $2.50/1M
			Completion:     "0.00001",      // $10/1M
			InputCacheRead: "0.00000125",   // $1.25/1M
		},
	}
	raw.TopProvider.MaxCompletionTokens = intPtr(16384)

	meta := normalizeOREntry(raw)

	if meta.ID != "openai/gpt-4o" {
		t.Errorf("ID = %q", meta.ID)
	}
	if meta.Label != "OpenAI: GPT-4o" {
		t.Errorf("Label = %q", meta.Label)
	}
	if meta.ContextLength != 128000 {
		t.Errorf("ContextLength = %d, want 128000", meta.ContextLength)
	}
	if meta.PromptPer1M == nil || abs(*meta.PromptPer1M-2.5) > 1e-9 {
		t.Errorf("PromptPer1M = %v, want 2.5", meta.PromptPer1M)
	}
	if meta.CompletionPer1M == nil || abs(*meta.CompletionPer1M-10.0) > 1e-9 {
		t.Errorf("CompletionPer1M = %v, want 10.0", meta.CompletionPer1M)
	}
	if meta.CacheReadPer1M == nil || abs(*meta.CacheReadPer1M-1.25) > 1e-9 {
		t.Errorf("CacheReadPer1M = %v, want 1.25", meta.CacheReadPer1M)
	}
	if meta.MaxOutput == nil || *meta.MaxOutput != 16384 {
		t.Errorf("MaxOutput = %v, want 16384", meta.MaxOutput)
	}
	if len(meta.InputModalities) != 2 {
		t.Errorf("InputModalities len = %d, want 2", len(meta.InputModalities))
	}
}

func TestNormalizeOREntry_FreeModel(t *testing.T) {
	raw := ORModelEntry{
		ID:            "meta-llama/llama-3.1-8b-instruct:free",
		Name:          "Llama 3.1 8B (free)",
		ContextLength: 131072,
		Pricing: struct {
			Prompt         string `json:"prompt"`
			Completion     string `json:"completion"`
			InputCacheRead string `json:"input_cache_read"`
			Image          string `json:"image"`
			WebSearch      string `json:"web_search"`
		}{
			Prompt:     "0",
			Completion: "0",
		},
	}

	meta := normalizeOREntry(raw)
	if meta.PromptPer1M == nil || *meta.PromptPer1M != 0 {
		t.Errorf("free model PromptPer1M = %v, want 0", meta.PromptPer1M)
	}
	if meta.CompletionPer1M == nil || *meta.CompletionPer1M != 0 {
		t.Errorf("free model CompletionPer1M = %v, want 0", meta.CompletionPer1M)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
