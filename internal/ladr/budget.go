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

// Package ladr — execution budget (Path B configurable resource ceilings).
package ladr

// ExecutionBudget allows a ladder author to configure resource ceilings
// per-ladder. All fields are optional pointers — nil means "use platform default."
// Authors can lower any ceiling (always safe) or raise it up to the platform max.
//
// YAML:
//
//	executionBudget:
//	  maxSpend: 10.0      # dollars — the primary safety net
//	  maxHops: 500
//	  maxLlmCalls: 1000
//	  maxRecursion: 10
type ExecutionBudget struct {
	MaxSpend     *float64 `json:"maxSpend,omitempty"`     // dollar spend cap
	MaxHops      *int     `json:"maxHops,omitempty"`      // loops + jumps + recursion combined
	MaxLlmCalls  *int     `json:"maxLlmCalls,omitempty"`  // total LLM API calls
	MaxRecursion *int     `json:"maxRecursion,omitempty"` // max recursion depth
}

// IsEmpty returns true if no budget field is set.
func (e ExecutionBudget) IsEmpty() bool {
	return e.MaxSpend == nil && e.MaxHops == nil &&
		e.MaxLlmCalls == nil && e.MaxRecursion == nil
}
