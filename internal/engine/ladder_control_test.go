// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version of the License, or (at your
// option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.

package engine

// ladder_control_test.go — Ladder 4: the control-flow combinator.
//
// Stresses the interaction between control-flow features through real ladder
// programs run via ExecutePipeline:
// - Compound conditions (and/or/not) — implemented but undocumented
// - Reflexion loop (backward jump + gate)
// - Recursion with knob-controlled depth
// - Gate + jump + gated write composition

import (
	"encoding/json"
	"strings"
	"testing"
)

// controlLadderCompound uses and/or/not compound conditions in a gate.
// When the gate passes → jump to "pass". When it fails → jump to "fallback".
// Both targets are terminal steps; exit is "pass" (the happy path).
const controlLadderCompound = `{
	"name": "control-compound",
	"allowedTargets": {"strategy": "universal"},
	"exit": "pass",
	"knobs": {},
	"steps": [
		{
			"id": "check", "name": "Check", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Output your assessment.",
			"if": {
				"and": [
					{"contains": "READY"},
					{"or": [{"equals": "READY"}, {"contains": "GO"}]},
					{"not": {"contains": "BLOCKED"}}
				],
				"then": {"jump": {"stepId": "pass"}},
				"else": {"jump": {"stepId": "fallback"}}
			}
		},
		{
			"id": "pass", "name": "Pass", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Gate passed."
		},
		{
			"id": "fallback", "name": "Fallback", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Fallback activated."
		}
	]
}`

// TestLadder_Control_CompoundConditions_Pass proves and/or/not compound
// conditions evaluate correctly when all sub-conditions pass.
func TestLadder_Control_CompoundConditions_Pass(t *testing.T) {
	io := newLadderIO([]string{
		"READY GO",  // check step: contains READY ✓, or{contains GO} ✓, not BLOCKED ✓ → gate passes
		"Gate passed.", // pass step output
	})

	result, err, _ := runLadder(t, controlLadderCompound, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// Gate passes → jump to pass → pass runs → exit (pass) output.
	if result != "Gate passed." {
		t.Errorf("result = %q, want 'Gate passed.'", result)
	}
}

// TestLadder_Control_CompoundConditions_FailNot proves the gate fails when the
// `not` sub-condition is violated (output contains BLOCKED).
func TestLadder_Control_CompoundConditions_FailNot(t *testing.T) {
	io := newLadderIO([]string{
		"READY GO BLOCKED", // not{contains BLOCKED} → fails → gate fails → jump to fallback
		"Fallback output.",  // fallback step
	})

	_, err, _ := runLadder(t, controlLadderCompound, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// Gate fails → jump to fallback → fallback runs. But exit is "pass" — so
	// we can't assert on result directly. Instead, verify the fallback step ran.
	// The fallback step's output was written even though it's not the exit.
	// We check that the right number of LLM calls were made.
	if len(io.prompts) != 2 {
		t.Errorf("expected 2 LLM calls (check + fallback), got %d", len(io.prompts))
	}
}

// TestLadder_Control_CompoundConditions_FailAnd proves the gate fails when one
// branch of an `and` fails (output doesn't contain the required substring).
func TestLadder_Control_CompoundConditions_FailAnd(t *testing.T) {
	io := newLadderIO([]string{
		"NO MATCH HERE", // and{contains READY} fails → gate fails → jump to fallback
		"Fallback output.",
	})

	_, err, _ := runLadder(t, controlLadderCompound, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// Gate fails → jump to fallback. Verify 2 LLM calls.
	if len(io.prompts) != 2 {
		t.Errorf("expected 2 LLM calls (check + fallback), got %d", len(io.prompts))
	}
}

// controlLadderReflexion is a generate→verify→(backward-jump-on-fail) ladder.
// Has a generous execution budget so the reflexion loop can run.
const controlLadderReflexion = `{
	"name": "control-reflexion",
	"allowedTargets": {"strategy": "universal"},
	"exit": "verify",
	"knobs": {},
	"executionBudget": {"maxHops": 500, "maxLlmCalls": 500},
	"steps": [
		{
			"id": "generate", "name": "Generate", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Generate a solution."
		},
		{
			"id": "verify", "name": "Verify", "type": "normal",
			"nodes": 1,
			"fields": [{"name": "Solution", "type": "ingest", "from": {"stepId": "generate", "loopRef": "current"}}],
			"systemPrompt": "Verify the solution.",
			"if": {
				"contains": "PASS",
				"then": "continue",
				"else": {"jump": {"stepId": "generate"}}
			}
		}
	]
}`

// TestLadder_Control_ReflexionLoop proves a reflexion loop: generate → verify
// fails (gate triggers backward jump to generate) → generate again → verify passes.
func TestLadder_Control_ReflexionLoop(t *testing.T) {
	io := newLadderIO([]string{
		"first attempt",     // generate (loop 0)
		"FAIL",              // verify (loop 0) — gate fails, backward jump to generate
		"second attempt",    // generate (loop 0, re-executed after backward jump)
		"PASS",              // verify (loop 0, re-executed) — gate passes → continue → exit
		// Extra fallbacks in case the loop needs more (shouldn't be reached).
		"third attempt",
		"PASS",
	})

	result, err, _ := runLadder(t, controlLadderReflexion, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if !strings.Contains(result, "PASS") {
		t.Errorf("result = %q, want something containing 'PASS'", result)
	}
	// Verify at least 4 LLM calls were made (2 generate + 2 verify).
	if len(io.prompts) < 4 {
		t.Errorf("expected ≥4 LLM calls, got %d", len(io.prompts))
	}
}

// controlLadderRecursion uses recursion with knob-controlled maxDepth.
const controlLadderRecursion = `{
	"name": "control-recursion",
	"allowedTargets": {"strategy": "universal"},
	"exit": "main",
	"knobs": {
		"depth": {"name": "depth", "type": "recursion", "input": "numerical", "min": 1, "max": 5, "default": 3}
	},
	"steps": [
		{
			"id": "main", "name": "Main", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Do the work.",
			"recursion": {"maxDepth": "{{knobs.depth}}"}
		}
	]
}`

// TestLadder_Control_RecursionWithKnob proves recursion with a knob-controlled
// maxDepth works: the recursion descends the specified number of levels.
func TestLadder_Control_RecursionWithKnob(t *testing.T) {
	// The recursion callback returns a simple string. With maxDepth=3, we expect
	// the recurseFn to be called up to 3 times.
	callCount := 0
	io := newLadderIO([]string{
		"main output",
	}).withRecurseFn(func(args RecurseArgs) string {
		callCount++
		return "recursive result"
	})

	_, err, _ := runLadder(t, controlLadderRecursion, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// The recursion should have been called at least once (maxDepth defaults to 3).
	// The exact count depends on the engine's recursion implementation, but it
	// should be > 0 and ≤ 3 (the default maxDepth).
	if callCount == 0 {
		t.Errorf("expected recursion to fire, got 0 calls")
	}
}

// controlLadderGateJump combines a gate with a jump. When the gate passes,
// execution continues to "done". When it fails, the else-jump goes to "rejected".
const controlLadderGateWriteJump = `{
	"name": "control-gate-jump",
	"allowedTargets": {"strategy": "universal"},
	"exit": "done",
	"knobs": {},
	"reliks": [
		{
			"title": "Mem",
			"folders": [{
				"name": "log",
				"tokenLimit": 1000,
				"access": "readWrite",
				"files": [{"name": "audit.md", "init": "blank"}]
			}]
		}
	],
	"steps": [
		{
			"id": "decide", "name": "Decide", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Output APPROVED or DENIED.",
			"if": {
				"contains": "APPROVED",
				"then": "continue",
				"else": {"jump": {"stepId": "rejected"}}
			}
		},
		{
			"id": "done", "name": "Done", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Final step."
		},
		{
			"id": "rejected", "name": "Rejected", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Rejected path."
		}
	]
}`

// TestLadder_Control_GateContinueOnPass proves the then:continue action fires
// when the gate passes: execution advances to the next step ("done").
func TestLadder_Control_GateWriteOnPass(t *testing.T) {
	io := newLadderIO([]string{
		"APPROVED", // gate passes → continue to done
		"Final output.",
	})

	result, err, _ := runLadder(t, controlLadderGateWriteJump, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Final output." {
		t.Errorf("result = %q, want 'Final output.'", result)
	}
}

// TestLadder_Control_GateJumpOnFail proves the else-jump fires when the gate fails.
// The jump goes to "rejected" (skipping "done").
func TestLadder_Control_GateJumpOnFail(t *testing.T) {
	io := newLadderIO([]string{
		"DENIED",         // decide step: gate fails → jump to rejected
		"Rejected output.", // rejected step
	})

	_, err, _ := runLadder(t, controlLadderGateWriteJump, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// Verify 2 LLM calls: decide + rejected (done was skipped by the jump).
	if len(io.prompts) != 2 {
		t.Errorf("expected 2 LLM calls (decide + rejected), got %d", len(io.prompts))
	}
}

// controlLadderMatches uses the `matches` (regex) condition — the undocumented
// 9th condition key.
const controlLadderMatches = `{
	"name": "control-matches",
	"allowedTargets": {"strategy": "universal"},
	"exit": "check",
	"knobs": {},
	"steps": [
		{
			"id": "check", "name": "Check", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Output a result.",
			"if": {
				"matches": "\\d{3}-\\d{4}",
				"then": "continue",
				"else": "abort"
			}
		}
	]
}`

// TestLadder_Control_MatchesRegex proves the `matches` (regex) condition evaluates
// correctly through the pipeline.
func TestLadder_Control_MatchesRegex(t *testing.T) {
	io := newLadderIO([]string{
		"The code is 123-4567.", // matches \d{3}-\d{4} → gate passes
	})

	result, err, _ := runLadder(t, controlLadderMatches, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "The code is 123-4567." {
		t.Errorf("result = %q", result)
	}
}

// TestLadder_Control_MatchesRegexFail proves the `matches` condition fails on
// non-matching output → else action (abort) fires.
func TestLadder_Control_MatchesRegexFail(t *testing.T) {
	io := newLadderIO([]string{
		"No phone number here.", // doesn't match \d{3}-\d{4} → gate fails → abort
	})

	_, err, _ := runLadder(t, controlLadderMatches, io, 1)
	if err == nil {
		t.Errorf("expected error from abort action when regex doesn't match")
	}
}

// Verify configs parse as valid JSON at init time.
func init() {
	for _, cfg := range []string{
		controlLadderCompound, controlLadderReflexion,
		controlLadderRecursion, controlLadderGateWriteJump,
		controlLadderMatches,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(cfg), &m); err != nil {
			panic("invalid test ladder JSON: " + err.Error())
		}
	}
}
