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

// TestExecutePipeline_MidLadderEntry verifies that when a child instance is
// spawned with input.handoffTargetStep set, the pipeline skips ahead to that
// step on loop 0 and does not execute earlier steps.
func TestExecutePipeline_MidLadderEntry(t *testing.T) {
	mkStep := func(id string) ladr.Step {
		return ladr.Step{
			Kind: ladr.StepTypeNormal,
			Normal: &ladr.NormalStep{
				BaseStep: ladr.BaseStep{
					ID:           id,
					Name:         id,
					Type:         ladr.StepTypeNormal,
					SystemPrompt: "echo",
				},
			},
		}
	}
	config := &ladr.PipelineConfig{
		Name:           "Target",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Exit:           "step3",
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{mkStep("step1"), mkStep("step2"), mkStep("step3")},
	}

	executed := map[string]bool{}
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			executed[args.StepID] = true
			return args.StepID
		},
	}

	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config: config,
		Context: ExecutionRuntimeContext{
			InstanceID: "child-1",
			Inputs: map[string]any{
				"input.context":           "via handoff",
				"input.handoffTargetStep": "step3",
			},
			Knobs: map[string]float64{},
		},
		IO:       io,
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed["step1"] {
		t.Error("step1 should NOT have executed (mid-ladder entry skips it)")
	}
	if executed["step2"] {
		t.Error("step2 should NOT have executed (mid-ladder entry skips it)")
	}
	if !executed["step3"] {
		t.Error("step3 SHOULD have executed (it is the mid-ladder entry target)")
	}
}

// TestExecutePipeline_MidLadderEntry_MissingStep verifies that a handoff
// target step that doesn't exist in the child config falls back to running
// from step 0 (defensive — no crash, clear behavior).
func TestExecutePipeline_MidLadderEntry_MissingStep(t *testing.T) {
	mkStep := func(id string) ladr.Step {
		return ladr.Step{
			Kind: ladr.StepTypeNormal,
			Normal: &ladr.NormalStep{
				BaseStep: ladr.BaseStep{ID: id, Name: id, Type: ladr.StepTypeNormal, SystemPrompt: "x"},
			},
		}
	}
	config := &ladr.PipelineConfig{
		Name:           "Target",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Exit:           "step1",
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{mkStep("step1")},
	}

	executed := map[string]bool{}
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			executed[args.StepID] = true
			return args.StepID
		},
	}

	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config: config,
		Context: ExecutionRuntimeContext{
			InstanceID: "child-1",
			Inputs: map[string]any{
				"input.context":           "via handoff",
				"input.handoffTargetStep": "nonexistent",
			},
			Knobs: map[string]float64{},
		},
		IO:       io,
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls back to running step1 (no mid-ladder skip when target not found).
	if !executed["step1"] {
		t.Error("step1 should have executed as fallback when target step not found")
	}
}

// TestExecutePipeline_MidLadderEntry_OnlyFirstLoop verifies that mid-ladder
// entry applies ONLY to loop 0; if maxLoops > 1, subsequent loops run from step 0.
func TestExecutePipeline_MidLadderEntry_OnlyFirstLoop(t *testing.T) {
	mkStep := func(id string) ladr.Step {
		return ladr.Step{
			Kind: ladr.StepTypeNormal,
			Normal: &ladr.NormalStep{
				BaseStep: ladr.BaseStep{ID: id, Name: id, Type: ladr.StepTypeNormal, SystemPrompt: "x"},
			},
		}
	}
	config := &ladr.PipelineConfig{
		Name:           "Target",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Exit:           "step2",
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{mkStep("step1"), mkStep("step2")},
	}

	executionOrder := []string{}
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			executionOrder = append(executionOrder, args.StepID)
			return args.StepID
		},
	}

	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config: config,
		Context: ExecutionRuntimeContext{
			InstanceID: "child-1",
			Inputs: map[string]any{
				"input.context":           "via handoff",
				"input.handoffTargetStep": "step2",
			},
			Knobs: map[string]float64{},
		},
		IO:       io,
		MaxLoops: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Loop 0: only step2 (mid-ladder entry). Loop 1: step1 + step2 (normal).
	expected := []string{"step2", "step1", "step2"}
	if len(executionOrder) != len(expected) {
		t.Fatalf("execution order length mismatch: got %v, want %v", executionOrder, expected)
	}
	for i, s := range expected {
		if executionOrder[i] != s {
			t.Errorf("position %d: got %q, want %q (full: %v)", i, executionOrder[i], s, executionOrder)
		}
	}
}
