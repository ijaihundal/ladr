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

// ProviderProtocol identifies the kind of API protocol a provider uses.
type ProviderProtocol string

const (
	ProtocolOpenAI  ProviderProtocol = "openai"
	ProtocolSpecial ProviderProtocol = "special"
)

// ProviderCredentialKind identifies the kind of credential a provider needs.
type ProviderCredentialKind string

const (
	CredentialAPIKey ProviderCredentialKind = "api_key"
)

// ProviderCatalogEntry describes one provider in the catalog.
type ProviderCatalogEntry struct {
	ID               string
	Label            string
	CredentialKind   ProviderCredentialKind
	SupportsEndpoint bool
	BaseURL          string // "" when null/absent in TS
	Protocol         ProviderProtocol
	Note             string
}

// providerCatalog is the single source of truth for "which providers exist".
var providerCatalog = []ProviderCatalogEntry{
	// ── Special providers (bespoke adapters) ──────────────────────────────
	{ID: "openai", Label: "OpenAI", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.openai.com/v1", Protocol: ProtocolSpecial},
	{ID: "anthropic", Label: "Anthropic", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, Protocol: ProtocolSpecial},
	{ID: "google_ai_studio", Label: "Google AI Studio", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, Protocol: ProtocolSpecial},
	{ID: "vertex", Label: "Vertex AI", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, Protocol: ProtocolSpecial},
	{ID: "openrouter", Label: "OpenRouter", CredentialKind: CredentialAPIKey, SupportsEndpoint: true, BaseURL: "https://openrouter.ai/api/v1", Protocol: ProtocolSpecial, Note: "Aggregator — one key reaches hundreds of models."},

	// ── OpenAI-compatible providers (catalog-driven, one row each) ────────
	// Direct model-makers
	{ID: "xai", Label: "xAI", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.x.ai/v1", Protocol: ProtocolOpenAI, Note: "Grok family (native)."},
	{ID: "deepseek", Label: "DeepSeek", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.deepseek.com/v1", Protocol: ProtocolOpenAI, Note: "DeepSeek-V3 / R1 (native)."},
	{ID: "mistral", Label: "Mistral", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.mistral.ai/v1", Protocol: ProtocolOpenAI, Note: "Mistral / Codestral (native)."},
	{ID: "cohere", Label: "Cohere", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.cohere.ai/compatibility/v1", Protocol: ProtocolOpenAI, Note: "Command family via OpenAI-compat layer."},
	{ID: "perplexity", Label: "Perplexity", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.perplexity.ai", Protocol: ProtocolOpenAI, Note: "Sonar / R1 family."},
	{ID: "moonshot", Label: "Moonshot (Kimi)", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.moonshot.cn/v1", Protocol: ProtocolOpenAI},
	{ID: "nvidia", Label: "NVIDIA NIM", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://integrate.api.nvidia.com/v1", Protocol: ProtocolOpenAI},
	{ID: "ai21", Label: "AI21", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.ai21.ai/studio/v1", Protocol: ProtocolOpenAI, Note: "Jamba family."},

	// Inference-host platforms
	{ID: "groq", Label: "Groq", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.groq.com/openai/v1", Protocol: ProtocolOpenAI, Note: "Ultra-low-latency inference."},
	{ID: "together", Label: "Together AI", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.together.xyz/v1", Protocol: ProtocolOpenAI},
	{ID: "fireworks", Label: "Fireworks", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.fireworks.ai/inference/v1", Protocol: ProtocolOpenAI},
	{ID: "deepinfra", Label: "DeepInfra", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.deepinfra.com/v1/openai", Protocol: ProtocolOpenAI},
	{ID: "novita", Label: "Novita AI", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.novita.ai/v3/openai", Protocol: ProtocolOpenAI},
	{ID: "nebius", Label: "Nebius (Token Factory)", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.tokenfactory.nebius.com/v1", Protocol: ProtocolOpenAI},
	{ID: "cerebras", Label: "Cerebras", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://inference.cerebras.ai/v1", Protocol: ProtocolOpenAI, Note: "Fast inference."},
	{ID: "hyperbolic", Label: "Hyperbolic", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.hyperbolic.xyz/v1", Protocol: ProtocolOpenAI},
	{ID: "sambanova", Label: "SambaNova", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.sambanova.ai/v1", Protocol: ProtocolOpenAI},
	{ID: "siliconflow", Label: "SiliconFlow", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.siliconflow.cn/v1", Protocol: ProtocolOpenAI},
	{ID: "chutes", Label: "Chutes", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.chutes.ai/v1", Protocol: ProtocolOpenAI},
	{ID: "infermatica", Label: "Infermatica", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.infermatica.ai/v1", Protocol: ProtocolOpenAI},
	{ID: "requesty", Label: "Requesty", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.requesty.ai/v1", Protocol: ProtocolOpenAI, Note: "Routing router."},
	{ID: "unify", Label: "Unify", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.unify.ai/v1", Protocol: ProtocolOpenAI, Note: "Cross-provider router."},
	{ID: "gmicloud", Label: "GMI Cloud", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://api.gmi-serving.com/v1", Protocol: ProtocolOpenAI},
	{ID: "lepton", Label: "Lepton AI", CredentialKind: CredentialAPIKey, SupportsEndpoint: true, BaseURL: "https://api.lepton.run/v1", Protocol: ProtocolOpenAI},
	{ID: "hf", Label: "Hugging Face", CredentialKind: CredentialAPIKey, SupportsEndpoint: false, BaseURL: "https://router.huggingface.co/v1", Protocol: ProtocolOpenAI, Note: "HF Inference Router."},
}

// copyEntry returns a deep copy of the catalog entry (so callers can't mutate).
func copyEntry(e ProviderCatalogEntry) ProviderCatalogEntry {
	return e // all fields are value types, so this is a full copy
}

// ListByokProviders returns a copy of the full BYOK provider catalog.
func ListByokProviders() []ProviderCatalogEntry {
	out := make([]ProviderCatalogEntry, len(providerCatalog))
	for i, e := range providerCatalog {
		out[i] = copyEntry(e)
	}
	return out
}

// ListOpenAiCompatibleProviders returns all OpenAI-compatible catalog entries.
func ListOpenAiCompatibleProviders() []ProviderCatalogEntry {
	var out []ProviderCatalogEntry
	for _, e := range providerCatalog {
		if e.Protocol == ProtocolOpenAI {
			out = append(out, copyEntry(e))
		}
	}
	return out
}

// GetByokProvider returns the catalog entry for the given provider ID, or nil.
func GetByokProvider(providerID string) *ProviderCatalogEntry {
	if providerID == "" {
		return nil
	}
	for _, e := range providerCatalog {
		if e.ID == providerID {
			entry := copyEntry(e)
			return &entry
		}
	}
	return nil
}

// IsByokProvider returns true if the provider ID exists in the catalog.
func IsByokProvider(providerID string) bool {
	return GetByokProvider(providerID) != nil
}
