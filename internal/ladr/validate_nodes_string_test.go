// Redeo - the programming layer for inference-time reasoning.
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

package ladr

import (
	"strings"
	"testing"
)

// `nodes: attempts` (a bare name) parses cleanly and then silently resolves
// to ONE node at runtime — a fan-out ladder quietly serialized. Validation
// must reject it at publish time.
func TestValidate_BareNodesStringRejected(t *testing.T) {
	max := 6.0
	min := 2.0
	def := 5.0
	cfg := &PipelineConfig{
		Name:           "fanout",
		AllowedTargets: AllowedTargetsPolicy{Strategy: AllowedTargetsUniversal},
		Exit:           "done",
		Knobs: map[string]Knob{
			"attempts": {
				Name: "Attempts",
				Type: KnobTypeNodes,
				Input: KnobInput{Kind: KnobInputNumerical, Numerical: NumericalInput{
					Min: min, Max: max, Default: &def,
				}},
			},
		},
		Steps: []Step{
			{Kind: StepTypeNormal, Normal: &NormalStep{BaseStep{
				ID: "attempt", Name: "Attempt",
				// THE BUG SHAPE: bare name, not {{knobs.attempts}}
				Nodes: &Nodes{Kind: NodesString, Str: "attempts"},
			}}},
			{Kind: StepTypeSequential, Sequential: &SequentialStep{BaseStep{
				ID: "done", Name: "Done",
			}}},
		},
	}

	result := ValidateConfig(cfg)
	if result.Valid {
		t.Fatal("bare nodes string must fail validation")
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e, "neither a knob reference") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bare-name guidance in errors, got: %v", result.Errors)
	}

	// The template form stays legal.
	cfg.Steps[0].Normal.Nodes = &Nodes{Kind: NodesString, Str: "{{knobs.attempts}}"}
	if result := ValidateConfig(cfg); !result.Valid {
		t.Fatalf("{{knobs.attempts}} must validate: %v", result.Errors)
	}

	// Step-output reference stays legal.
	cfg.Steps[0].Normal.Nodes = &Nodes{Kind: NodesString, Str: "{{planner.output}}"}
	if result := ValidateConfig(cfg); !result.Valid {
		t.Fatalf("{{X.output}} must validate: %v", result.Errors)
	}
}
