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

// init registers all builtin adapters: the 7 special adapters plus all
// catalog-driven OpenAI-compatible providers.
func init() {
	// Special adapters with bespoke request/auth shapes.
	RegisterAdapter(NewOpenAiCompatibleAdapter("openai", "https://api.openai.com/v1"))
	RegisterAdapter(&anthropicAdapter{})
	RegisterAdapter(&googleAiStudioAdapter{}) // vertex uses google_ai_studio adapter
	RegisterAdapter(NewOpenAiCompatibleAdapter("openrouter", "https://openrouter.ai/api/v1"))
	RegisterAdapter(&customAdapter{})

	// Catalog-driven OpenAI-compatible adapters. Each provider of
	// `protocol: "openai"` is built from the factory — adding one is a single
	// row in catalog.go, no adapter file required.
	for _, entry := range ListOpenAiCompatibleProviders() {
		if entry.BaseURL != "" {
			RegisterAdapter(NewOpenAiCompatibleAdapter(entry.ID, entry.BaseURL))
		}
	}
}
