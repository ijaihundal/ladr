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

package engine

import (
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

func TestCeilingTracker_Hops(t *testing.T) {
	ceilings := ResourceCeilings{MaxHops: 3, MaxLlmCalls: 100, MaxRecursion: 5, MaxSpend: 10.0}
	tracker := NewCeilingTracker(ceilings)

	// First 3 hops should pass
	for i := 0; i < 3; i++ {
		if err := tracker.IncHop(); err != nil {
			t.Fatalf("hop %d should not exceed ceiling: %v", i+1, err)
		}
	}

	// 4th hop should fail
	err := tracker.IncHop()
	if err == nil {
		t.Fatal("4th hop should exceed ceiling")
	}
	if _, ok := err.(*CeilingExceededError); !ok {
		t.Fatalf("expected CeilingExceededError, got %T", err)
	}
}

func TestCeilingTracker_LlmCalls(t *testing.T) {
	ceilings := ResourceCeilings{MaxHops: 100, MaxLlmCalls: 2, MaxRecursion: 5, MaxSpend: 10.0}
	tracker := NewCeilingTracker(ceilings)

	tracker.IncLlmCall()
	tracker.IncLlmCall()
	err := tracker.IncLlmCall()
	if err == nil {
		t.Fatal("3rd LLM call should exceed ceiling")
	}
}

func TestCeilingTracker_Spend(t *testing.T) {
	ceilings := ResourceCeilings{MaxHops: 100, MaxLlmCalls: 100, MaxRecursion: 5, MaxSpend: 1.0}
	tracker := NewCeilingTracker(ceilings)

	// Spend $0.50 — should pass
	if err := tracker.AddSpend(0.50); err != nil {
		t.Fatalf("spend $0.50 should not exceed: %v", err)
	}

	// Spend another $0.60 (total $1.10) — should exceed $1.00 cap
	err := tracker.AddSpend(0.60)
	if err == nil {
		t.Fatal("total spend $1.10 should exceed $1.00 cap")
	}
}

func TestCeilingTracker_Snapshot(t *testing.T) {
	ceilings := ResourceCeilings{MaxHops: 100, MaxLlmCalls: 50, MaxRecursion: 5, MaxSpend: 10.0}
	tracker := NewCeilingTracker(ceilings)

	tracker.IncHop()
	tracker.IncHop()
	tracker.IncLlmCall()
	tracker.AddSpend(0.25)

	snap := tracker.Snapshot()
	if snap["hops"].(int64) != 2 {
		t.Errorf("expected 2 hops, got %v", snap["hops"])
	}
	if snap["llmCalls"].(int64) != 1 {
		t.Errorf("expected 1 LLM call, got %v", snap["llmCalls"])
	}
	if snap["spend"].(float64) != 0.25 {
		t.Errorf("expected $0.25 spend, got %v", snap["spend"])
	}
}

func TestResolveCeilings_Defaults(t *testing.T) {
	// Nil budget → platform defaults
	c := ResolveCeilings(nil)
	defaults := DefaultCeilings()
	if c.MaxHops != defaults.MaxHops {
		t.Errorf("expected default hops %d, got %d", defaults.MaxHops, c.MaxHops)
	}
}

func TestResolveCeilings_Override(t *testing.T) {
	hops := 500
	spend := 15.0
	budget := &ladr.ExecutionBudget{
		MaxHops:  &hops,
		MaxSpend: &spend,
	}
	c := ResolveCeilings(budget)
	if c.MaxHops != 500 {
		t.Errorf("expected 500 hops, got %d", c.MaxHops)
	}
	if c.MaxSpend != 15.0 {
		t.Errorf("expected $15 spend, got %v", c.MaxSpend)
	}
	// Non-overridden fields should use defaults
	defaults := DefaultCeilings()
	if c.MaxLlmCalls != defaults.MaxLlmCalls {
		t.Errorf("expected default LLM calls %d, got %d", defaults.MaxLlmCalls, c.MaxLlmCalls)
	}
}

func TestResolveCeilings_ClampedToPlatformMax(t *testing.T) {
	hops := 999999 // way over platform max
	budget := &ladr.ExecutionBudget{MaxHops: &hops}
	c := ResolveCeilings(budget)
	max := PlatformMaximums()
	if c.MaxHops != max.MaxHops {
		t.Errorf("expected clamped to platform max %d, got %d", max.MaxHops, c.MaxHops)
	}
}
