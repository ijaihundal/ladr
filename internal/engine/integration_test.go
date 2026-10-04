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
	"context"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// TestExecuteSingleStep_IfGateJump tests that ExecuteSingleStep correctly
// evaluates the If block and sets PendingJump when the else action is a jump.
func TestExecuteSingleStep_IfGateJump(t *testing.T) {
	// Create a step with an If block that jumps on failure.
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "verify",
				Name: "Verify",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Score 1-5.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondIntegerRange, IntegerRange: [2]int{4, 5}},
					Then:      ladr.ContinueAction(),
					Else: ladr.Action{
						Kind: ladr.ActionJump,
						Jump: &ladr.JumpTarget{StepID: "refine"},
					},
				},
			},
		},
	}

	// Mock LLM returns a low score (2) — should trigger the else (jump to refine).
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			return "2" // below the [4,5] range
		},
	}

	runtime := &ExecutionRuntimeContext{
		InstanceID:    "test-instance",
		LoopIndex:     0,
		CurrentStepID: "verify",
		Knobs:         map[string]float64{},
		Inputs:        map[string]any{},
	}

	var llmCallCount int64
	stepsMap := map[string]ladr.Step{"verify": step}

	_, err := ExecuteSingleStep(
		context.Background(),
		step,
		runtime,
		io,
		stepsMap,
		ProviderConfig{},
		nil,
		DefaultResourceBudget,
		&llmCallCount,
	)

	// The step should succeed (not error) — the gate handled the failure via jump.
	if err != nil {
		t.Logf("ExecuteSingleStep returned error (may be expected for abort): %v", err)
	}

	// PendingJump should be set to "refine".
	if runtime.PendingJump != "refine" {
		t.Errorf("expected PendingJump='refine', got %q", runtime.PendingJump)
	}
}

// TestExecuteSingleStep_IfGatePass tests that a passing gate doesn't set PendingJump.
func TestExecuteSingleStep_IfGatePass(t *testing.T) {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "verify",
				Name: "Verify",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Score 1-5.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondIntegerRange, IntegerRange: [2]int{4, 5}},
					Then:      ladr.ContinueAction(),
					Else: ladr.Action{
						Kind: ladr.ActionJump,
						Jump: &ladr.JumpTarget{StepID: "refine"},
					},
				},
			},
		},
	}

	// Mock LLM returns a high score (5) — should pass the gate.
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			return "5" // in the [4,5] range
		},
	}

	runtime := &ExecutionRuntimeContext{
		InstanceID:    "test-instance",
		LoopIndex:     0,
		CurrentStepID: "verify",
		Knobs:         map[string]float64{},
		Inputs:        map[string]any{},
	}

	var llmCallCount int64
	stepsMap := map[string]ladr.Step{"verify": step}

	output, err := ExecuteSingleStep(
		context.Background(),
		step,
		runtime,
		io,
		stepsMap,
		ProviderConfig{},
		nil,
		DefaultResourceBudget,
		&llmCallCount,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PendingJump should NOT be set.
	if runtime.PendingJump != "" {
		t.Errorf("expected no PendingJump, got %q", runtime.PendingJump)
	}

	// Output should be "5".
	if output != "5" {
		t.Errorf("expected output '5', got %q", output)
	}
}

// TestExecuteSingleStep_IfGateAbort tests that an abort action causes step failure.
func TestExecuteSingleStep_IfGateAbort(t *testing.T) {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "gate",
				Name: "Gate",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Output READY or SKIP.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondEquals, Equals: "READY"},
					Then:      ladr.ContinueAction(),
					Else:      ladr.AbortAction(),
				},
			},
		},
	}

	// Mock LLM returns "SKIP" — fails the equals gate, else is abort.
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			return "SKIP"
		},
	}

	runtime := &ExecutionRuntimeContext{
		InstanceID:    "test-instance",
		LoopIndex:     0,
		CurrentStepID: "gate",
		Knobs:         map[string]float64{},
		Inputs:        map[string]any{},
	}

	var llmCallCount int64
	stepsMap := map[string]ladr.Step{"gate": step}

	_, err := ExecuteSingleStep(
		context.Background(),
		step,
		runtime,
		io,
		stepsMap,
		ProviderConfig{},
		nil,
		DefaultResourceBudget,
		&llmCallCount,
	)

	if err == nil {
		t.Error("expected error for abort action")
	}
}

// TestExecuteSingleStep_MultiNodeGatePruning tests per-node gate evaluation.
func TestExecuteSingleStep_MultiNodeGatePruning(t *testing.T) {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "fanout",
				Name: "Fanout",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  3,
				},
				SystemPrompt: "Output a score 1-5.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondIntegerRange, IntegerRange: [2]int{3, 5}},
					Then:      ladr.ContinueAction(),
					Else:      ladr.AbortAction(),
				},
			},
		},
	}

	// Mock LLM returns different scores per node.
	// Node 1: score 5 (pass), Node 2: score 2 (fail), Node 3: score 4 (pass).
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			if args.NodeNumber == nil {
				return "4"
			}
			switch *args.NodeNumber {
			case 1:
				return "5"
			case 2:
				return "2"
			default:
				return "4"
			}
		},
	}

	runtime := &ExecutionRuntimeContext{
		InstanceID:    "test-instance",
		LoopIndex:     0,
		CurrentStepID: "fanout",
		Knobs:         map[string]float64{},
		Inputs:        map[string]any{},
	}

	var llmCallCount int64
	stepsMap := map[string]ladr.Step{"fanout": step}

	output, err := ExecuteSingleStep(
		context.Background(),
		step,
		runtime,
		io,
		stepsMap,
		ProviderConfig{},
		nil,
		DefaultResourceBudget,
		&llmCallCount,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 2 survivors (nodes 1 and 3).
	// PendingJump should not be set (survivors exist, then=continue).
	if runtime.PendingJump != "" {
		t.Errorf("expected no PendingJump (survivors exist), got %q", runtime.PendingJump)
	}

	// Output should contain both surviving outputs (order may vary due to
	// parallel execution).
	if !containsStr(output, "5") || !containsStr(output, "4") {
		t.Errorf("expected output to contain both '5' and '4', got %q", output)
	}
	if containsStr(output, "2") {
		t.Errorf("pruned node 2 (score 2) should not appear in output, got %q", output)
	}
}
