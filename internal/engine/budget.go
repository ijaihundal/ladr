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

// Package engine — resource budget resolution.
// Ported from packages/engine-core/budget.ts.
package engine

import (
	"math"
	"os"
	"strconv"
)

// DefaultResourceBudget is the default set of resource caps.
var DefaultResourceBudget = ResourceBudget{
	MaxLoops:          50,
	MaxNodesPerStep:   20,
	MaxRecursionDepth: 5,
	MaxTotalLlmCalls:  500,
}

// envInt reads an env var, returning (value, ok). ok is false if the var is
// unset or not a valid finite number.
func envInt(key string) (int, bool) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int(f), true
}

// ResolveResourceBudget builds a ResourceBudget from environment variables,
// falling back to DefaultResourceBudget for any that are unset/invalid.
func ResolveResourceBudget() ResourceBudget {
	b := DefaultResourceBudget

	if v, ok := envInt("ENGINE_MAX_LOOPS"); ok {
		b.MaxLoops = v
	}
	if b.MaxLoops < 1 {
		b.MaxLoops = 1
	}

	if v, ok := envInt("ENGINE_MAX_NODES"); ok {
		b.MaxNodesPerStep = v
	}
	if b.MaxNodesPerStep < 1 {
		b.MaxNodesPerStep = 1
	}

	if v, ok := envInt("ENGINE_MAX_RECURSION"); ok {
		b.MaxRecursionDepth = v
	}
	if b.MaxRecursionDepth < 0 {
		b.MaxRecursionDepth = 0
	}

	if v, ok := envInt("ENGINE_MAX_TOTAL_LLM_CALLS"); ok {
		b.MaxTotalLlmCalls = v
	}
	if b.MaxTotalLlmCalls < 1 {
		b.MaxTotalLlmCalls = 1
	}

	return b
}
