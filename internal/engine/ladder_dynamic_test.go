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

// ladder_dynamic_test.go — Ladder 2: the dynamic-step validator.
//
// Dynamic steps (`dynamic: true`) are the "self-improving organism" primitive.
// LoadDynamicStep exists but was never wired into the execution path. These
// tests exercise the planner→executor pattern end-to-end through ExecutePipeline:
//   - Planner→executor happy path
// - Dynamic config with an if-gate (Path B — control flow in loaded config)
// - fromFallback when primary source misses
// - onInvalid when the loaded config is invalid

import (
	"encoding/json"
	"strings"
	"testing"
)

// dynamicLadder is a 2-step ladder: planner emits a JSON step config, executor
// (dynamic: true) loads and runs it.
const dynamicLadder = `{
	"name": "dynamic-demo",
	"allowedTargets": {"strategy": "universal"},
	"exit": "executor",
	"knobs": {},
	"steps": [
		{
			"id": "planner", "name": "Planner", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Emit a JSON step config."
		},
		{
			"id": "executor", "name": "Executor", "type": "normal",
			"nodes": 1,
			"dynamic": true,
			"from": {"stepId": "planner", "loopRef": "current"}
		}
	]
}`

// TestLadder_Dynamic_PlannerToExecutor proves the planner→executor pattern works:
// step 1 (planner) emits a valid JSON step config as its output, step 2 (dynamic: true)
// loads that config and executes it. The executor's output should be produced by
// the loaded config's systemPrompt, not the (empty) declared systemPrompt.
func TestLadder_Dynamic_PlannerToExecutor(t *testing.T) {
	// The planner emits a valid DynamicStepConfig JSON.
	plannerOutput := `{
		"systemPrompt": "You are the dynamically loaded executor. Say hello.",
		"fields": [{"name": "Task", "type": "text", "from": "input.task"}]
	}`

	io := newLadderIO([]string{
		plannerOutput, // step "planner" output
		"Hello from the dynamic executor!", // step "executor" (loaded config runs)
	})

	result, err, _ := runLadderWithInputs(t, dynamicLadder, io, 1, map[string]any{"input.task": "greet"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Hello from the dynamic executor!" {
		t.Errorf("result = %q, want 'Hello from the dynamic executor!'", result)
	}
	// Verify the executor's prompt contained the loaded systemPrompt, not empty.
	if len(io.prompts) < 2 {
		t.Fatalf("expected ≥2 LLM calls, got %d", len(io.prompts))
	}
	executorPrompt := io.prompts[1]
	if !strings.Contains(executorPrompt, "dynamically loaded executor") {
		t.Errorf("executor prompt should contain the loaded systemPrompt, got: %q", executorPrompt)
	}
}

// TestLadder_Dynamic_LoadedConfigWithGate proves Path B: a dynamically loaded
// config can contain control flow (if-gate). The planner emits a config with an
// if-condition; the executor loads it and the gate evaluates.
func TestLadder_Dynamic_LoadedConfigWithGate(t *testing.T) {
	// Planner emits a config with an if-gate that checks for "PASS" in the output.
	plannerOutput := `{
		"systemPrompt": "Respond with PASS or FAIL.",
		"fields": [{"name": "Task", "type": "text", "from": "input.task"}],
		"if": {"contains": "PASS", "then": "continue", "else": "abort"}
	}`

	io := newLadderIO([]string{
		plannerOutput, // step "planner"
		"PASS",        // step "executor" — output contains "PASS", gate passes → continue
	})

	result, err, _ := runLadderWithInputs(t, dynamicLadder, io, 1, map[string]any{"input.task": "check"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "PASS" {
		t.Errorf("result = %q, want 'PASS'", result)
	}
}

// dynamicLadderWithFallback has a fromFallback on the executor step.
const dynamicLadderWithFallback = `{
	"name": "dynamic-fallback",
	"allowedTargets": {"strategy": "universal"},
	"exit": "executor",
	"knobs": {},
	"steps": [
		{
			"id": "planner", "name": "Planner", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Emit a JSON step config."
		},
		{
			"id": "executor", "name": "Executor", "type": "normal",
			"nodes": 1,
			"dynamic": true,
			"from": {"stepId": "planner", "loopRef": "current"},
			"fromFallback": "input.fallbackConfig"
		}
	]
}`

// TestLadder_Dynamic_FromFallback proves that when the primary from: source
// produces no output (empty/missing), the fromFallback is consulted. Here the
// planner emits an empty string, so the executor falls back to input.fallbackConfig.
func TestLadder_Dynamic_FromFallback(t *testing.T) {
	fallbackConfig := `{"systemPrompt": "Fallback executor.", "fields": [{"name": "X", "type": "text", "from": "input.task"}]}`

	io := newLadderIO([]string{
		"",                        // planner emits empty output
		"Fallback executed!",      // executor runs with the fallback config
	})

	inputs := map[string]any{
		"input.task":           "test",
		"input.fallbackConfig": fallbackConfig,
	}

	result, err, _ := runLadderWithInputs(t, dynamicLadderWithFallback, io, 1, inputs)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Fallback executed!" {
		t.Errorf("result = %q, want 'Fallback executed!'", result)
	}
}

// dynamicLadderWithOnInvalid has an onInvalid action on the executor.
const dynamicLadderWithOnInvalid = `{
	"name": "dynamic-oninvalid",
	"allowedTargets": {"strategy": "universal"},
	"exit": "fallback",
	"knobs": {},
	"steps": [
		{
			"id": "planner", "name": "Planner", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Emit a JSON step config."
		},
		{
			"id": "executor", "name": "Executor", "type": "normal",
			"nodes": 1,
			"dynamic": true,
			"from": {"stepId": "planner", "loopRef": "current"},
			"onInvalid": {"jump": {"stepId": "fallback"}}
		},
		{
			"id": "fallback", "name": "Fallback", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Fallback step after dynamic load failure."
		}
	]
}`

// TestLadder_Dynamic_OnInvalidFiresOnBadConfig proves that when the planner emits
// invalid JSON (not a valid DynamicStepConfig), the executor's onInvalid action
// fires and jumps to the fallback step.
func TestLadder_Dynamic_OnInvalidFiresOnBadConfig(t *testing.T) {
	io := newLadderIO([]string{
		"THIS IS NOT VALID JSON",   // planner emits invalid config
		"Fallback step executed!",  // fallback step output
	})

	result, err, _ := runLadderWithInputs(t, dynamicLadderWithOnInvalid, io, 1, map[string]any{"input.task": "test"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Fallback step executed!" {
		t.Errorf("result = %q, want 'Fallback step executed!'", result)
	}
}

// TestLadder_Dynamic_OnInvalidFiresOnEmptyConfig proves that when the planner
// emits a valid JSON that's an empty/invalid DynamicStepConfig (missing
// systemPrompt), onInvalid fires.
func TestLadder_Dynamic_OnInvalidFiresOnEmptyConfig(t *testing.T) {
	io := newLadderIO([]string{
		`{"fields": []}`,           // valid JSON but invalid config (no systemPrompt, no fields)
		"Fallback step executed!",  // fallback step output
	})

	result, err, _ := runLadderWithInputs(t, dynamicLadderWithOnInvalid, io, 1, map[string]any{"input.task": "test"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Fallback step executed!" {
		t.Errorf("result = %q, want 'Fallback step executed!'", result)
	}
}

// Verify configs parse as valid JSON at init time.
func init() {
	for _, cfg := range []string{dynamicLadder, dynamicLadderWithFallback, dynamicLadderWithOnInvalid} {
		var m map[string]any
		if err := json.Unmarshal([]byte(cfg), &m); err != nil {
			panic("invalid test ladder JSON: " + err.Error())
		}
	}
}
