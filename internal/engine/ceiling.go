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

// Package engine — resource ceiling tracker for Path B (Turing-complete grammar).
//
// Tracks resource consumption during execution and enforces hard limits.
// The spend cap is the primary safety net for untrusted published ladders.
package engine

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ladr/ladr/internal/ladr"
)

// ResourceCeilings holds the hard limits for a single execution.
// Values are resolved from the ladder's executionBudget + platform defaults.
type ResourceCeilings struct {
	MaxSpend     float64 // dollars
	MaxHops      int     // loops + jumps + recursion combined
	MaxLlmCalls  int     // total LLM API calls
	MaxRecursion int     // max recursion depth
}

// DefaultCeilings returns the platform default resource ceilings.
func DefaultCeilings() ResourceCeilings {
	return ResourceCeilings{
		MaxSpend:     5.0, // $5 per execution
		MaxHops:      200,
		MaxLlmCalls:  500,
		MaxRecursion: 5,
	}
}

// PlatformMaximums returns the absolute maximums a ladder author can request.
func PlatformMaximums() ResourceCeilings {
	return ResourceCeilings{
		MaxSpend:     50.0,
		MaxHops:      10000,
		MaxLlmCalls:  5000,
		MaxRecursion: 20,
	}
}

// ResolveCeilings merges a ladder's declared budget with platform defaults.
// The ladder can lower any ceiling (always safe) or raise it up to the platform max.
func ResolveCeilings(declared *ladr.ExecutionBudget) ResourceCeilings {
	c := DefaultCeilings()
	if declared == nil {
		return c
	}
	max := PlatformMaximums()
	if declared.MaxSpend != nil {
		c.MaxSpend = minf(*declared.MaxSpend, max.MaxSpend)
	}
	if declared.MaxHops != nil {
		c.MaxHops = mini(*declared.MaxHops, max.MaxHops)
	}
	if declared.MaxLlmCalls != nil {
		c.MaxLlmCalls = mini(*declared.MaxLlmCalls, max.MaxLlmCalls)
	}
	if declared.MaxRecursion != nil {
		c.MaxRecursion = mini(*declared.MaxRecursion, max.MaxRecursion)
	}
	return c
}

// CeilingTracker tracks live resource consumption during execution.
// Thread-safe — multiple nodes within a step run concurrently.
type CeilingTracker struct {
	mu        sync.Mutex
	ceilings  ResourceCeilings
	hops      atomic.Int64
	llmCalls  atomic.Int64
	recursion atomic.Int64

	// Spend tracking — needs mutex because we need to check+increment atomically.
	spendMu      sync.Mutex
	currentSpend float64
}

// NewCeilingTracker creates a tracker with the given ceilings.
func NewCeilingTracker(ceilings ResourceCeilings) *CeilingTracker {
	return &CeilingTracker{ceilings: ceilings}
}

// CeilingExceededError indicates a resource ceiling was hit.
type CeilingExceededError struct {
	Ceiling    string
	Used       float64
	Limit      float64
	BestOutput string // best-so-far output for graceful exit
}

func (e *CeilingExceededError) Error() string {
	return fmt.Sprintf("resource ceiling exceeded: %s (used %v, limit %v)", e.Ceiling, e.Used, e.Limit)
}

// IncHop increments the hop counter. Returns error if ceiling exceeded.
func (t *CeilingTracker) IncHop() error {
	n := t.hops.Add(1)
	if int(n) > t.ceilings.MaxHops {
		return &CeilingExceededError{Ceiling: "hops", Used: float64(n), Limit: float64(t.ceilings.MaxHops)}
	}
	return nil
}

// IncLlmCall increments the LLM call counter. Returns error if ceiling exceeded.
func (t *CeilingTracker) IncLlmCall() error {
	n := t.llmCalls.Add(1)
	if int(n) > t.ceilings.MaxLlmCalls {
		return &CeilingExceededError{Ceiling: "llmCalls", Used: float64(n), Limit: float64(t.ceilings.MaxLlmCalls)}
	}
	return nil
}

// IncRecursion increments the recursion depth counter. Returns error if exceeded.
func (t *CeilingTracker) IncRecursion() error {
	n := t.recursion.Add(1)
	if int(n) > t.ceilings.MaxRecursion {
		return &CeilingExceededError{Ceiling: "recursion", Used: float64(n), Limit: float64(t.ceilings.MaxRecursion)}
	}
	return nil
}

// DecRecursion decrements the recursion depth counter (when returning from recursion).
func (t *CeilingTracker) DecRecursion() {
	t.recursion.Add(-1)
}

// AddSpend adds to the dollar spend counter. Returns error if ceiling exceeded.
func (t *CeilingTracker) AddSpend(amount float64) error {
	t.spendMu.Lock()
	defer t.spendMu.Unlock()
	t.currentSpend += amount
	if t.currentSpend > t.ceilings.MaxSpend {
		return &CeilingExceededError{Ceiling: "spend", Used: t.currentSpend, Limit: t.ceilings.MaxSpend}
	}
	return nil
}

// CurrentSpend returns the current dollar spend.
func (t *CeilingTracker) CurrentSpend() float64 {
	t.spendMu.Lock()
	defer t.spendMu.Unlock()
	return t.currentSpend
}

// Snapshot returns the current resource usage as a map (for events/logging).
func (t *CeilingTracker) Snapshot() map[string]any {
	return map[string]any{
		"hops":           t.hops.Load(),
		"llmCalls":       t.llmCalls.Load(),
		"recursionDepth": t.recursion.Load(),
		"spend":          t.CurrentSpend(),
		"ceilings": map[string]any{
			"maxHops":      t.ceilings.MaxHops,
			"maxLlmCalls":  t.ceilings.MaxLlmCalls,
			"maxRecursion": t.ceilings.MaxRecursion,
			"maxSpend":     t.ceilings.MaxSpend,
		},
	}
}

// --- helpers ---

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
