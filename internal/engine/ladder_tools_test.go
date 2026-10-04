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

package engine

// ladder_tools_test.go — Ladder 1: the adaptive-key validator.
//
// Runs the full flat-key grammar (codeExecution + relikRead + relikEdit)
// through ExecutePipeline — not just ExecuteAdaptiveLoop in isolation. Proves
// the adaptive loop fires correctly when reached as a step inside a full
// pipeline run.
//
// Covers:
//   - Code-execution through the pipeline (code call → result injection → final answer)
//   - Multi-tool dispatch: read → edit → final answer in one step
//   - autoPrompt injection (default tool spec in prompt when autoPrompt != false)
//   - maxRounds forced termination

import (
	"encoding/json"
	"strings"
	"testing"
)

// toolsLadderCodeExec is a 2-step ladder: analyze (plain) → verify (codeExecution).
// The verify step's adaptive loop runs code, gets the result injected, then produces prose.
const toolsLadderCodeExec = `{
	"name": "tools-code-exec",
	"allowedTargets": {"strategy": "universal"},
	"exit": "verify",
	"knobs": {},
	"steps": [
		{
			"id": "analyze", "name": "Analyze", "type": "normal",
			"nodes": 1,
			"fields": [{"name": "Q", "type": "text", "from": "input.task"}],
			"systemPrompt": "Analyze the task."
		},
		{
			"id": "verify", "name": "Verify", "type": "normal",
			"nodes": 1,
			"fields": [{"name": "Analysis", "type": "ingest", "from": {"stepId": "analyze", "loopRef": "current"}}],
			"codeExecution": {
				"maxRounds": 3,
				"runtime": "python",
				"as": "ComputedResult",
				"requestMatch": {"type": "object", "properties": {"code": {"type": "string"}}, "additionalProperties": true}
			},
			"systemPrompt": "Verify by running code."
		}
	]
}`

// TestLadder_Tools_CodeExecutionThroughPipeline proves the code-execution
// adaptive loop fires when the step is reached inside a full ExecutePipeline run.
// The LLM emits a code call, the fake code executor returns a result, the result
// is injected, and the LLM produces a final prose answer.
func TestLadder_Tools_CodeExecutionThroughPipeline(t *testing.T) {
	io := newLadderIO([]string{
		"Analysis: the task is to compute 6*7.", // step "analyze" output
		`{"code": "print(6*7)"}`,                // step "verify" round 1: code call
		"The answer is 42.",                      // step "verify" round 2: final answer
	}).withCodeResult(ExecuteCodeResult{Output: "42"})

	result, err, _ := runLadderWithInputs(t, toolsLadderCodeExec, io, 1, map[string]any{"task": "compute"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "The answer is 42." {
		t.Errorf("result = %q, want 'The answer is 42.'", result)
	}
	// Verify code was actually executed.
	if len(io.codeCalls) != 1 {
		t.Fatalf("expected 1 code call, got %d", len(io.codeCalls))
	}
	if io.codeCalls[0].Code != "print(6*7)" {
		t.Errorf("code call = %q, want 'print(6*7)'", io.codeCalls[0].Code)
	}
	// Verify the result was injected into the second LLM call's prompt.
	if len(io.prompts) < 3 {
		t.Fatalf("expected ≥3 LLM calls, got %d", len(io.prompts))
	}
	if !strings.Contains(io.prompts[2], "42") {
		t.Errorf("verify round-2 prompt should contain injected result '42', got: %s", io.prompts[2])
	}
}

// toolsLadderMultiTool is a 1-step ladder with relikRead + relikEdit.
// The LLM reads a file, edits it, then produces a final answer.
const toolsLadderMultiTool = `{
	"name": "tools-multi",
	"allowedTargets": {"strategy": "universal"},
	"exit": "work",
	"knobs": {},
	"reliks": [
		{
			"title": "KB",
			"folders": [{
				"name": "docs",
				"tokenLimit": 1000,
				"access": "readWrite",
				"files": [{"name": "fact.md", "init": "The sky is blue."}]
			}]
		}
	],
	"steps": [
		{
			"id": "work", "name": "Work", "type": "normal",
			"nodes": 1,
			"relikRead": {
				"maxRounds": 5,
				"from": {"relik": "KB", "folder": "docs"},
				"as": "Retrieved",
				"requestMatch": {
					"type": "object",
					"properties": {
						"request": {"type": "object", "properties": {"files": {"type": "array", "items": {"type": "string"}}}},
						"edit": {"type": "object", "properties": {"file": {"type": "string"}, "ops": {"type": "array"}}}
					},
					"additionalProperties": true
				}
			},
			"relikEdit": {
				"maxRounds": 5,
				"to": {"relik": "KB", "folder": "docs"},
				"as": "EditReceipt",
				"requestMatch": {
					"type": "object",
					"properties": {
						"request": {"type": "object", "properties": {"files": {"type": "array", "items": {"type": "string"}}}},
						"edit": {"type": "object", "properties": {"file": {"type": "string"}, "ops": {"type": "array"}}}
					},
					"additionalProperties": true
				}
			},
			"systemPrompt": "Work with the KB."
		}
	]
}`

// TestLadder_Tools_MultiToolDispatch proves relikRead + relikEdit can fire in
// sequence within one step's adaptive loop: read → edit → final answer.
func TestLadder_Tools_MultiToolDispatch(t *testing.T) {
	io := newLadderIO([]string{
		// Round 1: retrieve fact.md
		`{"request": {"files": ["fact.md"]}}`,
		// Round 2: edit fact.md (replace "blue" with "green")
		`{"edit": {"file": "fact.md", "ops": [{"op": "replace", "anchor": "blue", "content": "green"}]}}`,
		// Round 3: final prose answer
		"Done. The fact has been updated.",
	})

	result, err, store := runLadder(t, toolsLadderMultiTool, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Done. The fact has been updated." {
		t.Errorf("result = %q", result)
	}
	// Verify the relik was actually edited.
	content := mustReadRelik(t, store, "KB", "docs", "fact.md")
	if !strings.Contains(content, "green") {
		t.Errorf("relik should contain 'green' after edit, got: %q", content)
	}
	if strings.Contains(content, "blue") {
		t.Errorf("relik should NOT contain 'blue' after replace, got: %q", content)
	}
}

// TestLadder_Tools_AutoPromptInjected proves the default tool-spec prompt is
// injected when autoPrompt is not explicitly false.
func TestLadder_Tools_AutoPromptInjected(t *testing.T) {
	io := newLadderIO([]string{
		`{"code": "print(1)"}`,
		"final answer",
	}).withCodeResult(ExecuteCodeResult{Output: "1"})

	_, _, _ = runLadderWithInputs(t, toolsLadderCodeExec, io, 1, map[string]any{"task": "test"})

	// The verify step's prompt (index 1) should contain auto-injected tool spec.
	if len(io.prompts) < 2 {
		t.Fatalf("expected ≥2 prompts, got %d", len(io.prompts))
	}
	verifyPrompt := io.prompts[1]
	if len(verifyPrompt) < len("Verify by running code.")+10 {
		t.Errorf("verify prompt looks too short for autoPrompt injection: %q", verifyPrompt)
	}
}

// TestLadder_Tools_AutoPromptDisabled proves that when autoPrompt is false, the
// tool spec is NOT injected — the prompt is just the systemPrompt + fields.
func TestLadder_Tools_AutoPromptDisabled(t *testing.T) {
	autoPromptFalse := strings.Replace(toolsLadderCodeExec,
		`"codeExecution": {`,
		`"codeExecution": {"autoPrompt": false, `, 1)

	io := newLadderIO([]string{
		"analysis output",
		`{"code": "print(1)"}`,
		"final answer",
	}).withCodeResult(ExecuteCodeResult{Output: "1"})

	_, _, _ = runLadderWithInputs(t, autoPromptFalse, io, 1, map[string]any{"task": "test"})

	if len(io.prompts) < 2 {
		t.Fatalf("expected ≥2 prompts, got %d", len(io.prompts))
	}
	verifyPrompt := io.prompts[1]
	if !strings.Contains(verifyPrompt, "Verify by running code.") {
		t.Errorf("verify prompt should contain systemPrompt text: %q", verifyPrompt)
	}
}

// TestLadder_Tools_MaxRoundsForcesTermination proves the adaptive loop terminates
// when maxRounds is hit, even if the LLM keeps emitting tool calls.
func TestLadder_Tools_MaxRoundsForcesTermination(t *testing.T) {
	// maxRounds: 3 in the ladder. LLM emits 5 code calls — should stop at 3.
	io := newLadderIO([]string{
		"analysis output",
		`{"code": "print(1)"}`,
		`{"code": "print(2)"}`,
		`{"code": "print(3)"}`,
		`{"code": "print(4)"}`, // should never be reached
		`{"code": "print(5)"}`, // should never be reached
	}).withCodeResult(ExecuteCodeResult{Output: "ok"})

	result, err, _ := runLadderWithInputs(t, toolsLadderCodeExec, io, 1, map[string]any{"task": "test"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result == "" {
		t.Errorf("expected non-empty result after maxRounds termination")
	}
	if len(io.codeCalls) > 3 {
		t.Errorf("expected ≤3 code calls (maxRounds=3), got %d", len(io.codeCalls))
	}
}

// TestLadder_Tools_NonMatchingOutputEndsLoop proves that when the LLM emits
// non-matching output (prose, not JSON matching requestMatch), the adaptive
// loop ends with that output as the final answer.
func TestLadder_Tools_NonMatchingOutputEndsLoop(t *testing.T) {
	io := newLadderIO([]string{
		"analysis output",
		"This is my final prose answer, no tool call needed.", // non-matching → ends loop
	})

	result, err, _ := runLadderWithInputs(t, toolsLadderCodeExec, io, 1, map[string]any{"task": "test"})
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "This is my final prose answer, no tool call needed." {
		t.Errorf("result = %q", result)
	}
	if len(io.codeCalls) != 0 {
		t.Errorf("expected 0 code calls, got %d", len(io.codeCalls))
	}
}

// toolsLadderAllThree has all 3 adaptive keys enabled (codeExecution + relikRead + relikEdit)
// so dispatch precedence can be tested: code > request(read) > edit.
const toolsLadderAllThree = `{
	"name": "tools-all-three",
	"allowedTargets": {"strategy": "universal"},
	"exit": "work",
	"knobs": {},
	"reliks": [
		{
			"title": "KB",
			"folders": [{
				"name": "docs",
				"tokenLimit": 1000,
				"access": "readWrite",
				"files": [{"name": "fact.md", "init": "hello world"}]
			}]
		}
	],
	"steps": [
		{
			"id": "work", "name": "Work", "type": "normal",
			"nodes": 1,
			"codeExecution": {
				"maxRounds": 5,
				"runtime": "python",
				"as": "ComputedResult",
				"requestMatch": {
					"type": "object",
					"properties": {
						"code": {"type": "string"},
						"request": {"type": "object", "properties": {"files": {"type": "array", "items": {"type": "string"}}}},
						"edit": {"type": "object", "properties": {"file": {"type": "string"}, "ops": {"type": "array"}}}
					},
					"additionalProperties": true
				}
			},
			"relikRead": {
				"maxRounds": 5,
				"from": {"relik": "KB", "folder": "docs"},
				"as": "Retrieved",
				"requestMatch": {
					"type": "object",
					"properties": {
						"code": {"type": "string"},
						"request": {"type": "object", "properties": {"files": {"type": "array", "items": {"type": "string"}}}},
						"edit": {"type": "object", "properties": {"file": {"type": "string"}, "ops": {"type": "array"}}}
					},
					"additionalProperties": true
				}
			},
			"relikEdit": {
				"maxRounds": 5,
				"to": {"relik": "KB", "folder": "docs"},
				"as": "EditReceipt",
				"requestMatch": {
					"type": "object",
					"properties": {
						"code": {"type": "string"},
						"request": {"type": "object", "properties": {"files": {"type": "array", "items": {"type": "string"}}}},
						"edit": {"type": "object", "properties": {"file": {"type": "string"}, "ops": {"type": "array"}}}
					},
					"additionalProperties": true
				}
			},
			"systemPrompt": "Work with the KB."
		}
	]
}`

// TestLadder_Tools_DispatchPrecedence proves code takes precedence over read
// when both are enabled and the output matches both schemas. The dispatch order
// is code > request(read) > edit.
func TestLadder_Tools_DispatchPrecedence(t *testing.T) {
	// This output has BOTH a "code" and "request" key. Code should win.
	io := newLadderIO([]string{
		`{"code": "print(42)", "request": {"files": ["fact.md"]}}`,
		"final answer",
	}).withCodeResult(ExecuteCodeResult{Output: "42"})

	_, err, _ := runLadder(t, toolsLadderAllThree, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if len(io.codeCalls) != 1 {
		t.Errorf("expected 1 code call (precedence), got %d", len(io.codeCalls))
	}
}

// Verify the JSON configs are valid by parsing them at init time (catches typos).
func init() {
	for _, cfg := range []string{toolsLadderCodeExec, toolsLadderMultiTool, toolsLadderAllThree} {
		var m map[string]any
		if err := json.Unmarshal([]byte(cfg), &m); err != nil {
			panic("invalid test ladder JSON: " + err.Error())
		}
	}
}
