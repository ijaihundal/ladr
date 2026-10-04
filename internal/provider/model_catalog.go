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

// Modality enumerates input/output modalities a catalog model supports.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
)

// CatalogModelPricing holds pricing info for a catalog model.
type CatalogModelPricing struct {
	PromptPer1M     *float64
	CompletionPer1M *float64
	ImagePer1k      *float64
}

// CatalogProviderModel maps a public model ID to a provider-specific model ID.
type CatalogProviderModel struct {
	ProviderModel string
	Region        *string
	Pricing       *struct {
		PromptPer1M     *float64
		CompletionPer1M *float64
	} `json:"-"`
	ContextLength *int
}

// CatalogModelEntry is the full catalog entry for one model.
type CatalogModelEntry struct {
	PublicID            string
	Label               string
	Description         string
	Author              string
	Family              string
	OpenrouterSlug      *string
	Providers           map[string]CatalogProviderModel
	ContextLength       *int
	MaxOutput           *int
	InputModalities     []Modality
	OutputModalities    []Modality
	Pricing             *CatalogModelPricing
	ReleasedAt          *string
	KnowledgeCutoff     *string
	SupportedParameters []string
	Tags                []string
}

// intPtr creates an *int from a literal.
func intPtr(v int) *int { return &v }

// strPtr creates a *string from a literal.
func strPtr(v string) *string { return &v }

// floatPtr creates a *float64 from a literal.
func floatPtr(v float64) *float64 { return &v }

// catalogModels is the static list of catalog models.
var catalogModels = []CatalogModelEntry{
	{
		PublicID:            "gpt-oss-20b",
		Label:               "GPT-OSS-20B",
		Author:              "openai",
		Family:              "gpt-oss",
		OpenrouterSlug:      strPtr("openai/gpt-oss-20b"),
		Description:         "OpenAI's open-weight Mixture-of-Experts model (20B total, ~3.6B active). Fast and efficient for general chat, reasoning, and tool use, with a 128K context window.",
		ContextLength:       intPtr(128000),
		MaxOutput:           intPtr(32768),
		InputModalities:     []Modality{ModalityText, ModalityImage},
		OutputModalities:    []Modality{ModalityText},
		SupportedParameters: []string{"tools", "temperature", "top_p", "max_tokens", "json_mode"},
		Tags:                []string{"open-weight", "tools", "vision"},
		Providers: map[string]CatalogProviderModel{
			"vertex": {
				ProviderModel: "projects/my-project/locations/global/publishers/openai/models/gpt-oss-20b-maas",
				Region:        strPtr("global"),
			},
			"openrouter": {ProviderModel: "openai/gpt-oss-20b"},
			"deepinfra":  {ProviderModel: "openai/gpt-oss-20b"},
			"novita":     {ProviderModel: "openai/gpt-oss-20b"},
			"fireworks":  {ProviderModel: "accounts/fireworks/models/gpt-oss-20b"},
			"nebius":     {ProviderModel: "nebius/openai/gpt-oss-20b"},
		},
	},
	{
		PublicID:            "qwen3-coder-480b",
		Label:               "Qwen3-Coder-480B",
		Author:              "qwen",
		Family:              "qwen3-coder",
		OpenrouterSlug:      strPtr("qwen/qwen3-coder-480b"),
		Description:         "Alibaba's Qwen3-Coder (480B total, A35B active). A coding-specialized model with strong agentic and tool-use capabilities and a 256K context window, tuned for software engineering tasks.",
		ContextLength:       intPtr(262144),
		MaxOutput:           intPtr(32768),
		InputModalities:     []Modality{ModalityText},
		OutputModalities:    []Modality{ModalityText},
		SupportedParameters: []string{"tools", "temperature", "top_p", "max_tokens", "json_mode"},
		Tags:                []string{"code", "tools", "agentic"},
		Providers: map[string]CatalogProviderModel{
			"vertex": {
				ProviderModel: "projects/my-project/locations/global/publishers/qwen/models/qwen3-coder-480b-a35b-instruct-maas",
				Region:        strPtr("global"),
			},
		},
	},
	{
		PublicID:            "gemini-3-flash-preview",
		Label:               "Gemini 3 Flash Preview",
		Author:              "google",
		Family:              "gemini-3-flash",
		OpenrouterSlug:      strPtr("google/gemini-3-flash-preview"),
		Description:         "Google's Gemini 3 Flash (preview) — a fast, lightweight multimodal model supporting text and image inputs with text outputs. Optimized for high-throughput, low-latency tasks with a 1M context window.",
		ContextLength:       intPtr(1000000),
		MaxOutput:           intPtr(65536),
		InputModalities:     []Modality{ModalityText, ModalityImage, ModalityAudio, ModalityVideo},
		OutputModalities:    []Modality{ModalityText},
		SupportedParameters: []string{"tools", "temperature", "top_p", "max_tokens", "json_mode"},
		Tags:                []string{"multimodal", "tools", "long-context", "vision"},
		Providers: map[string]CatalogProviderModel{
			"vertex":           {ProviderModel: "gemini-3-flash-preview", Region: strPtr("global")},
			"google_ai_studio": {ProviderModel: "gemini-3-flash-preview"},
			"openrouter":       {ProviderModel: "google/gemini-3-flash-preview"},
		},
	},
	{
		PublicID:            "gpt-oss-120b",
		Label:               "GPT-OSS-120B",
		Author:              "openai",
		Family:              "gpt-oss",
		OpenrouterSlug:      strPtr("openai/gpt-oss-120b"),
		Description:         "OpenAI's flagship open-weight Mixture-of-Experts model (120B total, ~5B active). Delivers frontier-class reasoning and coding quality while running efficiently, with a 128K context window.",
		ContextLength:       intPtr(128000),
		MaxOutput:           intPtr(32768),
		InputModalities:     []Modality{ModalityText, ModalityImage},
		OutputModalities:    []Modality{ModalityText},
		SupportedParameters: []string{"tools", "temperature", "top_p", "max_tokens", "json_mode"},
		Tags:                []string{"open-weight", "tools", "vision", "reasoning"},
		Providers: map[string]CatalogProviderModel{
			"vertex": {
				ProviderModel: "projects/my-project/locations/global/publishers/openai/models/gpt-oss-120b-maas",
				Region:        strPtr("global"),
			},
			"openrouter": {ProviderModel: "openai/gpt-oss-120b"},
			"deepinfra":  {ProviderModel: "openai/gpt-oss-120b"},
			"novita":     {ProviderModel: "openai/gpt-oss-120b"},
			"fireworks":  {ProviderModel: "accounts/fireworks/models/gpt-oss-120b"},
			"nebius":     {ProviderModel: "nebius/openai/gpt-oss-120b"},
		},
	},
}

// modelByPublicID is an index for fast lookup.
var modelByPublicID = func() map[string]*CatalogModelEntry {
	m := make(map[string]*CatalogModelEntry, len(catalogModels))
	for i := range catalogModels {
		entry := catalogModels[i]
		m[lowerTrim(entry.PublicID)] = &entry
	}
	return m
}()

func lowerTrim(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		// trim leading spaces
		if len(out) == 0 && c == ' ' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out = append(out, c)
	}
	// trim trailing spaces
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// ListCatalogModels returns a copy of all catalog model entries.
func ListCatalogModels() []CatalogModelEntry {
	out := make([]CatalogModelEntry, len(catalogModels))
	for i := range catalogModels {
		out[i] = catalogModels[i]
	}
	return out
}

// GetCatalogModel returns the catalog entry for the given model ID, or nil.
func GetCatalogModel(modelID string) *CatalogModelEntry {
	key := lowerTrim(modelID)
	if key == "" {
		return nil
	}
	return modelByPublicID[key]
}

// IsCatalogModel returns true if the model ID is in the catalog.
func IsCatalogModel(modelID string) bool {
	return GetCatalogModel(modelID) != nil
}

// SupportsProviderModel returns true if the provider serves the given catalog model.
// Non-catalog models always return true.
func SupportsProviderModel(providerID, modelID string) bool {
	model := GetCatalogModel(modelID)
	if model == nil {
		return true
	}
	providerKey := lowerTrim(providerID)
	if providerKey == "" {
		return false
	}
	_, ok := model.Providers[providerKey]
	return ok
}

// ResolveProviderModel returns the provider-specific model ID for a catalog model,
// or the original modelID if it is not a catalog model.
func ResolveProviderModel(providerID, modelID string) string {
	resolved := lowerTrim(modelID)
	_ = resolved // resolved is only used for emptiness check; we use the original modelID
	trimmed := trimSpace(modelID)
	if trimmed == "" {
		return ""
	}
	model := GetCatalogModel(trimmed)
	if model == nil {
		return trimmed
	}
	providerKey := lowerTrim(providerID)
	if mapping, ok := model.Providers[providerKey]; ok {
		return mapping.ProviderModel
	}
	return trimmed
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && s[start] == ' ' {
		start++
	}
	end := len(s)
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
