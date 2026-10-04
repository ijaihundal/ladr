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

// Package engine — gate matrix: fills the 4 missing condition-key gaps in the
// gate evaluator (matches/and/or/not). The other 8 keys are covered in
// gate_test.go; these exercise the regex evaluator and the recursive
// composition operators (and/or/not) at the execution level.

package engine

import (
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// =============================================================================
// matches — regex search (partial match, RE2)
// =============================================================================

func TestEvaluateGate_Matches_Regex(t *testing.T) {
	// NOTE: EvaluateGate trims the output before testing, so leading/trailing
	// whitespace is stripped before the regex runs.
	cond := ladr.Condition{Kind: ladr.CondMatches, Matches: `^error \d{3}:`}

	tests := []struct {
		output string
		pass   bool
	}{
		{"error 404: not found", true},   // exact prefix match
		{"error 500: internal", true},    // different 3-digit code
		{"  error 404: x", true},         // leading space trimmed, then matches
		{"warning: error 404:", false},   // non-prefix even after trim
		{"error 42: too short", false},   // code is not 3 digits
		{"", false},                      // empty
	}
	for _, tt := range tests {
		r := EvaluateGate(tt.output, cond)
		if r.Passed != tt.pass {
			t.Errorf("matches(%q) = %v, want %v (reason: %s)", tt.output, r.Passed, tt.pass, r.Reason)
		}
	}
}

func TestEvaluateGate_Matches_PartialSearch(t *testing.T) {
	// An unanchored pattern matches anywhere in the output (search, not full-match).
	cond := ladr.Condition{Kind: ladr.CondMatches, Matches: `\d{3}-\d{4}`}
	if !EvaluateGate("call me at 555-1234 today", cond).Passed {
		t.Error("unanchored regex should match as a search")
	}
	if EvaluateGate("no phone here", cond).Passed {
		t.Error("regex should not match when the pattern is absent")
	}
}

// =============================================================================
// and — all sub-conditions must pass
// =============================================================================

func TestEvaluateGate_And_AllTrue(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondAnd, And: []ladr.Condition{
		{Kind: ladr.CondContains, Contains: "READY"},
		{Kind: ladr.CondContains, Contains: "OK"},
	}}
	r := EvaluateGate("READY OK", cond)
	if !r.Passed {
		t.Errorf("and: all-true should pass, reason: %s", r.Reason)
	}
}

func TestEvaluateGate_And_OneFalse(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondAnd, And: []ladr.Condition{
		{Kind: ladr.CondEquals, Equals: "READY"},
		{Kind: ladr.CondContains, Contains: "MISSING"},
	}}
	r := EvaluateGate("READY", cond)
	if r.Passed {
		t.Error("and: one-false should fail")
	}
}

func TestEvaluateGate_And_EmptyArray(t *testing.T) {
	// An empty and-array is a validation error (caught at config time), but the
	// runtime evaluator treats it as vacuously true. Pin this behavior.
	cond := ladr.Condition{Kind: ladr.CondAnd, And: []ladr.Condition{}}
	r := EvaluateGate("anything", cond)
	if !r.Passed {
		t.Errorf("and: empty array should be vacuously true at runtime, reason: %s", r.Reason)
	}
}

// =============================================================================
// or — any sub-condition passing is enough
// =============================================================================

func TestEvaluateGate_Or_AnyTrue(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondOr, Or: []ladr.Condition{
		{Kind: ladr.CondEquals, Equals: "YES"},
		{Kind: ladr.CondEquals, Equals: "NO"},
	}}
	tests := []struct {
		output string
		pass   bool
	}{
		{"YES", true},
		{"NO", true},
		{"MAYBE", false},
	}
	for _, tt := range tests {
		r := EvaluateGate(tt.output, cond)
		if r.Passed != tt.pass {
			t.Errorf("or(%q) = %v, want %v (reason: %s)", tt.output, r.Passed, tt.pass, r.Reason)
		}
	}
}

func TestEvaluateGate_Or_AllFalse(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondOr, Or: []ladr.Condition{
		{Kind: ladr.CondEquals, Equals: "YES"},
		{Kind: ladr.CondEquals, Equals: "NO"},
	}}
	r := EvaluateGate("MAYBE", cond)
	if r.Passed {
		t.Error("or: all-false should fail")
	}
}

// =============================================================================
// not — inverts a single sub-condition
// =============================================================================

func TestEvaluateGate_Not_Inverts(t *testing.T) {
	sub := ladr.Condition{Kind: ladr.CondEquals, Equals: "SKIP"}
	cond := ladr.Condition{Kind: ladr.CondNot, Not: &sub}

	// When the sub passes, not fails.
	r := EvaluateGate("SKIP", cond)
	if r.Passed {
		t.Error("not: should fail when sub-condition passes")
	}
	// When the sub fails, not passes.
	r = EvaluateGate("RUN", cond)
	if !r.Passed {
		t.Error("not: should pass when sub-condition fails")
	}
}

func TestEvaluateGate_Not_NilSub(t *testing.T) {
	// A nil sub-condition is a validation error, but the runtime evaluator
	// handles it gracefully (returns false). Pin this behavior.
	cond := ladr.Condition{Kind: ladr.CondNot, Not: nil}
	r := EvaluateGate("anything", cond)
	if r.Passed {
		t.Error("not: nil sub-condition should not pass")
	}
}

// =============================================================================
// Recursive composition — deeply nested and/or/not
// =============================================================================

func TestEvaluateGate_DeepNesting(t *testing.T) {
	// and(or(not(equals "BLOCKED")), contains " GO ") — the spaced contains avoids
	// accidental substring matches on "GO" inside other words like "FORCEGO".
	inner := ladr.Condition{Kind: ladr.CondEquals, Equals: "BLOCKED"}
	notCond := ladr.Condition{Kind: ladr.CondNot, Not: &inner}
	orCond := ladr.Condition{Kind: ladr.CondOr, Or: []ladr.Condition{notCond, {Kind: ladr.CondContains, Contains: "FORCE"}}}
	cond := ladr.Condition{Kind: ladr.CondAnd, And: []ladr.Condition{orCond, {Kind: ladr.CondContains, Contains: " GO "}}}

	tests := []struct {
		output string
		pass   bool
	}{
		{"READY GO NOW", true},      // not(BLOCKED)=true → or=true; contains " GO " → and=true
		{"BLOCKED FORCE STOP", false}, // or(FORCE)=true but no " GO " substring → and=false
		{"BLOCKED STOP NOW", false}, // or=false (not-false, no FORCE) → and=false
		{"READY STOP", false},       // or=true but no " GO " → and=false
	}
	for _, tt := range tests {
		r := EvaluateGate(tt.output, cond)
		if r.Passed != tt.pass {
			t.Errorf("deepNest(%q) = %v, want %v (reason: %s)", tt.output, r.Passed, tt.pass, r.Reason)
		}
	}
}
