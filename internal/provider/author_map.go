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

// authorToProvider maps OpenRouter author prefixes to native catalog provider IDs.
var authorToProvider = map[string]string{
	"openai":     "openai",
	"anthropic":  "anthropic",
	"google":     "google_ai_studio",
	"x-ai":       "xai",
	"deepseek":   "deepseek",
	"mistralai":  "mistral",
	"cohere":     "cohere",
	"perplexity": "perplexity",
	"moonshotai": "moonshot",
	"nvidia":     "nvidia",
	"ai21":       "ai21",
}

// ParseAuthor splits `x-ai/grok-4` → `x-ai`. Returns "" for un-slugged ids.
func ParseAuthor(openrouterSlug string) string {
	if openrouterSlug == "" {
		return ""
	}
	for i := 0; i < len(openrouterSlug); i++ {
		if openrouterSlug[i] == '/' {
			if i == 0 {
				return ""
			}
			return openrouterSlug[:i]
		}
	}
	return ""
}

// DeriveNativeProvider resolves the native catalog provider ID for an
// OpenRouter author prefix, or "" when there is no direct catalog-backed API.
func DeriveNativeProvider(openrouterSlug string) string {
	author := ParseAuthor(openrouterSlug)
	if author == "" {
		return ""
	}
	if p, ok := authorToProvider[author]; ok {
		return p
	}
	return ""
}

// GetAuthorProviderMap returns the full author → provider map for introspection.
func GetAuthorProviderMap() map[string]string {
	out := make(map[string]string, len(authorToProvider))
	for k, v := range authorToProvider {
		out[k] = v
	}
	return out
}
