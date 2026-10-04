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

// TestExecutePipeline_IntraLadderPassField_Override verifies that a within-ladder
// jump with passFields wires the resolved caller value into the target step's field.
func TestExecutePipeline_IntraLadderPassField_Override(t *testing.T) {
	// Ladder:
	//   route  → (always) → jump to finalize with passField Question = route's output
	//   finalize → reads Question (text field) and echoes it
	routeStep := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "route",
				Name: "Route",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Output a question.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondContains, Contains: "?"},
					Then: ladr.Action{
						Kind: ladr.ActionJump,
						Jump: &ladr.JumpTarget{
							StepID: "finalize",
							PassFields: []ladr.PassField{
								{To: "Question", From: ladr.FromSpec{Kind: ladr.FromKeywordOutput}},
							},
						},
					},
					Else: ladr.AbortAction(),
				},
			},
		},
	}
	finalizeStep := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "finalize",
				Name: "Finalize",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Echo back the Question exactly.",
				Fields: &ladr.FieldsConfig{
					Fields: []ladr.Field{
						{
							Kind: ladr.FieldText,
							Text: ladr.TextField{
								Name: "Question",
								Type: "text",
								From: "input.default", // would be empty without passField override
							},
						},
					},
				},
			},
		},
	}
	config := &ladr.PipelineConfig{
		Name:           "test",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Exit:           "finalize",
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{routeStep, finalizeStep},
	}

	callCount := 0
	var finalizePrompt string
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			callCount++
			switch args.StepID {
			case "route":
				return "what is the answer?"
			case "finalize":
				finalizePrompt = args.UserPrompt
				return args.UserPrompt // echoes the prompt (which contains the resolved field)
			}
			return "unknown"
		},
	}

	output, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   config,
		Context:  ExecutionRuntimeContext{InstanceID: "t1", Inputs: map[string]any{}, Knobs: map[string]float64{}},
		IO:       io,
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount < 2 {
		t.Errorf("expected both steps to run (callCount ≥ 2), got %d", callCount)
	}
	if !containsStr(finalizePrompt, "what is the answer?") {
		t.Errorf("passField override not applied: finalize prompt does not contain the override value.\n prompt: %q", finalizePrompt)
	}
	if !containsStr(output, "what is the answer?") {
		t.Errorf("expected output to contain override value, got %q", output)
	}
}

// TestExecutePipeline_CrossLadderHandoff_WithPassFields verifies that passFields
// on a cross-ladder jump are resolved in caller context and shipped to the child.
func TestExecutePipeline_CrossLadderHandoff_WithPassFields(t *testing.T) {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "router",
				Name: "Router",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Output GO.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondEquals, Equals: "GO"},
					Then: ladr.Action{
						Kind: ladr.ActionJump,
						Jump: &ladr.JumpTarget{
							LadderID: "@alice/helper",
							PassFields: []ladr.PassField{
								{To: "Topic", From: ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "math"}},
								{To: "Difficulty", From: ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "hard"}},
							},
						},
					},
					Else: ladr.AbortAction(),
				},
			},
		},
	}
	config := &ladr.PipelineConfig{
		Name:           "Router",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Exit:           "router",
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{step},
	}

	var capturedOverrides map[string]string
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string { return "GO" },
		handoffFn: func(args HandoffArgs) (HandoffResult, error) {
			capturedOverrides = args.PassFieldOverrides
			return HandoffResult{ExitOutput: "child answer"}, nil
		},
	}

	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   config,
		Context:  ExecutionRuntimeContext{InstanceID: "t1", Inputs: map[string]any{}, Knobs: map[string]float64{}},
		IO:       io,
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOverrides == nil {
		t.Fatal("expected PassFieldOverrides to be shipped to handoff")
	}
	if capturedOverrides["Topic"] != "math" {
		t.Errorf("expected Topic=math, got %q", capturedOverrides["Topic"])
	}
	if capturedOverrides["Difficulty"] != "hard" {
		t.Errorf("expected Difficulty=hard, got %q", capturedOverrides["Difficulty"])
	}
}

// TestValidateHandoffPassFieldTargets covers the runtime validator.
func TestValidateHandoffPassFieldTargets(t *testing.T) {
	stepWithFields := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "step1",
				Name: "Step1",
				Type: ladr.StepTypeNormal,
				Fields: &ladr.FieldsConfig{
					Fields: []ladr.Field{
						{Kind: ladr.FieldText, Text: ladr.TextField{Name: "Question", Type: "text", From: "input.q"}},
						{Kind: ladr.FieldText, Text: ladr.TextField{Name: "Context", Type: "text", From: "input.c"}},
					},
				},
			},
		},
	}
	config := &ladr.PipelineConfig{
		Name:  "test",
		Exit:  "step1",
		Steps: []ladr.Step{stepWithFields},
	}

	// All overrides match declared fields → no error.
	err := validateHandoffPassFieldTargets(config, "", map[string]string{
		"Question": "q1",
		"Context":  "c1",
	})
	if err != nil {
		t.Errorf("expected no error for matching overrides, got %v", err)
	}

	// Override key not on declared field list → error.
	err = validateHandoffPassFieldTargets(config, "", map[string]string{
		"Nonexistent": "x",
	})
	if err == nil {
		t.Error("expected error for non-matching override key, got nil")
	}

	// Target step ID specified and matches → validate against that step.
	err = validateHandoffPassFieldTargets(config, "step1", map[string]string{
		"Question": "q1",
	})
	if err != nil {
		t.Errorf("expected no error for matching override on named target step, got %v", err)
	}

	// Target step ID specified but doesn't exist → error.
	err = validateHandoffPassFieldTargets(config, "nope", map[string]string{
		"Question": "q1",
	})
	if err == nil {
		t.Error("expected error for non-existent target step, got nil")
	}
}

// TestValidateConfig_IntraLadderPassField_StaticCheck verifies the static
// validator catches passField.to that doesn't match a declared field on the
// intra-ladder target step.
func TestValidateConfig_IntraLadderPassField_StaticCheck(t *testing.T) {
	// Build a config with a jump whose passField.to doesn't match any field on
	// the target step.
	target := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "target",
				Name: "Target",
				Type: ladr.StepTypeNormal,
				Fields: &ladr.FieldsConfig{
					Fields: []ladr.Field{
						{Kind: ladr.FieldText, Text: ladr.TextField{Name: "Real", Type: "text", From: "input.x"}},
					},
				},
			},
		},
	}
	caller := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:           "caller",
				Name:         "Caller",
				Type:         ladr.StepTypeNormal,
				SystemPrompt: "x",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondEquals, Equals: "go"},
					Then: ladr.Action{
						Kind: ladr.ActionJump,
						Jump: &ladr.JumpTarget{
							StepID: "target",
							PassFields: []ladr.PassField{
								{To: "Fake", From: ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "x"}},
							},
						},
					},
					Else: ladr.AbortAction(),
				},
			},
		},
	}
	config := &ladr.PipelineConfig{
		Name:           "test",
		Exit:           "target",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{caller, target},
	}

	result := ladr.ValidateConfig(config)
	if result.Valid {
		t.Errorf("expected validation to fail due to passField.to mismatch, but it passed")
	}
	foundMismatch := false
	for _, msg := range result.Errors {
		if containsStr(msg, "passFields") && containsStr(msg, "Fake") {
			foundMismatch = true
		}
	}
	if !foundMismatch {
		t.Errorf("expected passField mismatch error, got: %v", result.Errors)
	}
}
