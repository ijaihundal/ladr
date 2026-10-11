// Redeo - the programming layer for inference-time reasoning.
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

// Package engine — prompt assembly.
// Ported from packages/engine-core/prompt.ts.
package engine

import (
	"fmt"
	"strings"
)

// AssemblePrompt builds the final user prompt from resolved fields and a system
// prompt.
//
// Format:
//
//	fieldName: value
//
//	fieldName 1: value
//	fieldName 2: value
//
//	[System Instruction]
//	systemPrompt
func AssemblePrompt(resolvedFields []ResolvedField, systemPrompt string) string {
	var sections []string
	for _, field := range resolvedFields {
		switch v := field.Value.(type) {
		case string:
			sections = append(sections, fmt.Sprintf("%s: %s", field.Name, v))
		case []string:
			for i, item := range v {
				sections = append(sections, fmt.Sprintf("%s %d: %s", field.Name, i+1, item))
			}
		case []any:
			for i, item := range v {
				sections = append(sections, fmt.Sprintf("%s %d: %v", field.Name, i+1, item))
			}
		default:
			sections = append(sections, fmt.Sprintf("%s: %v", field.Name, v))
		}
	}

	userPrompt := strings.Join(sections, "\n\n")
	return fmt.Sprintf("%s\n\n[System Instruction]\n%s", userPrompt, systemPrompt)
}
