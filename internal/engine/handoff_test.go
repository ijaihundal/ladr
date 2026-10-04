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
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// TestExecutePipeline_CrossLadderHandoff verifies the end-to-end tail-call
// handoff: a step fires a cross-ladder jump, the pipeline captures PendingHandoff,
// calls io.Handoff, and returns the child's exit output as the user-visible result.
func TestExecutePipeline_CrossLadderHandoff(t *testing.T) {
	// Ladder with one step whose else action is a cross-ladder jump.
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
						Jump: &ladr.JumpTarget{LadderID: "@alice/helper"},
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

	handoffCalled := false
	var capturedTarget string
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			return "GO" // matches the then condition
		},
		handoffFn: func(args HandoffArgs) (HandoffResult, error) {
			handoffCalled = true
			capturedTarget = args.Target.LadderID
			return HandoffResult{
				ChildInstanceID: "child-1",
				ExitOutput:      "answer from child ladder",
			}, nil
		},
	}

	runtimeCtx := ExecutionRuntimeContext{
		InstanceID: "test-instance",
		Inputs:     map[string]any{"input.context": "test"},
		Knobs:      map[string]float64{},
	}

	output, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   config,
		Context:  runtimeCtx,
		IO:       io,
		MaxLoops: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handoffCalled {
		t.Fatal("expected io.Handoff to be called")
	}
	if capturedTarget != "@alice/helper" {
		t.Errorf("expected target @alice/helper, got %q", capturedTarget)
	}
	if output != "answer from child ladder" {
		t.Errorf("expected child exit output, got %q", output)
	}
}

// TestExecutePipeline_CrossLadderHandoff_FiringOutputBecomesInputContext verifies
// that the firing step's combined output is passed as FiringOutput (which becomes
// the child's input.context per master spec §5).
func TestExecutePipeline_CrossLadderHandoff_FiringOutputBecomesInputContext(t *testing.T) {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:   "prep",
				Name: "Prep",
				Type: ladr.StepTypeNormal,
				Nodes: &ladr.Nodes{
					Kind: ladr.NodesNumber,
					Num:  1,
				},
				SystemPrompt: "Output structured query.",
				If: &ladr.If{
					Condition: ladr.Condition{Kind: ladr.CondContains, Contains: "QUERY:"},
					Then: ladr.Action{
						Kind: ladr.ActionJump,
						Jump: &ladr.JumpTarget{LadderID: "@alice/researcher"},
					},
					Else: ladr.AbortAction(),
				},
			},
		},
	}
	config := &ladr.PipelineConfig{
		Name:           "Prep",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Exit:           "prep",
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{step},
	}

	var capturedFiring string
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string {
			return "QUERY: find me coffee"
		},
		handoffFn: func(args HandoffArgs) (HandoffResult, error) {
			capturedFiring = args.FiringOutput
			return HandoffResult{ExitOutput: "ok"}, nil
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
	if !strings.Contains(capturedFiring, "QUERY: find me coffee") {
		t.Errorf("expected FiringOutput to contain the step's output, got %q", capturedFiring)
	}
}

// TestExecutePipeline_CrossLadderHandoff_HandoffErrorPropagates verifies that
// an error from io.Handoff propagates as a pipeline error.
func TestExecutePipeline_CrossLadderHandoff_HandoffErrorPropagates(t *testing.T) {
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
						Jump: &ladr.JumpTarget{LadderID: "@alice/missing"},
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

	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string { return "GO" },
		handoffFn: func(args HandoffArgs) (HandoffResult, error) {
			return HandoffResult{}, errSentinel_handoff
		},
	}

	_, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config:   config,
		Context:  ExecutionRuntimeContext{InstanceID: "t1", Inputs: map[string]any{}, Knobs: map[string]float64{}},
		IO:       io,
		MaxLoops: 1,
	})
	if err == nil {
		t.Fatal("expected error from handoff failure, got nil")
	}
}

// errSentinel_handoff is a sentinel error used by the handoff-error test.
var errSentinel_handoff = &simpleError{msg: "handoff resolver not wired"}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
