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
	"github.com/ladr/ladr/internal/relik"
)

// newTestRelikStore builds an in-memory relik store. Defined here for the
// gated-write fallback tests in this file.
func newTestRelikStore() *relik.MemoryStore { return relik.NewMemoryStore() }

// TestResolutionPriority_FullChain verifies the locked-in priority order
// (passField override > from: > fallback:) with a single ladder that exercises
// all three tiers across three different fields:
//   - FieldA: passField override present → wins
//   - FieldB: passField absent, from: has a value → from: wins
//   - FieldC: passField absent, from: misses (empty input key), fallback: literal → fallback wins
func TestResolutionPriority_FullChain(t *testing.T) {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID:           "merge",
				Name:         "Merge",
				Type:         ladr.StepTypeNormal,
				Nodes:        &ladr.Nodes{Kind: ladr.NodesNumber, Num: 1},
				SystemPrompt: "Output the three values.",
				Fields: &ladr.FieldsConfig{
					Fields: []ladr.Field{
						{
							Kind: ladr.FieldText,
							Text: ladr.TextField{
								Name:     "FieldA",
								Type:     "text",
								From:     "input.fieldA_default",
								Fallback: &ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "fieldA_fallback"},
							},
						},
						{
							Kind: ladr.FieldText,
							Text: ladr.TextField{
								Name:     "FieldB",
								Type:     "text",
								From:     "input.fieldB_value",
								Fallback: &ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "fieldB_fallback"},
							},
						},
						{
							Kind: ladr.FieldText,
							Text: ladr.TextField{
								Name:     "FieldC",
								Type:     "text",
								From:     "input.fieldC_missing", // intentionally unset
								Fallback: &ladr.FromSpec{Kind: ladr.FromLiteral, Literal: "fieldC_fallback"},
							},
						},
					},
				},
			},
		},
	}
	io := &mockEngineIO{
		llmFn: func(args ExecuteLlmParams) string { return "ok" },
	}
	runtime := &ExecutionRuntimeContext{
		InstanceID:    "t1",
		LoopIndex:     0,
		CurrentStepID: "merge",
		// FieldA_default intentionally unset (would trigger fallback) — but passField overrides it.
		Inputs: map[string]any{
			"input.fieldB_value": "fieldB_from_input",
		},
		Knobs: map[string]float64{},
		// passField override ONLY for FieldA. FieldB and FieldC go through normal resolution.
		PassFieldOverrides: map[string]string{
			"FieldA": "fieldA_passfield",
		},
	}

	resolved, err := ResolveStepFields(context.Background(), step, runtime, io, map[string]ladr.Step{})
	if err != nil {
		t.Fatalf("ResolveStepFields error: %v", err)
	}
	got := map[string]string{}
	for _, rf := range resolved {
		if s, ok := rf.Value.(string); ok {
			got[rf.Name] = s
		}
	}
	if got["FieldA"] != "fieldA_passfield" {
		t.Errorf("FieldA: expected passField override %q, got %q", "fieldA_passfield", got["FieldA"])
	}
	if got["FieldB"] != "fieldB_from_input" {
		t.Errorf("FieldB: expected from: value %q, got %q", "fieldB_from_input", got["FieldB"])
	}
	if got["FieldC"] != "fieldC_fallback" {
		t.Errorf("FieldC: expected fallback %q, got %q", "fieldC_fallback", got["FieldC"])
	}
}
