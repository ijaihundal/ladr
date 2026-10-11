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
	"encoding/json"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// A gated single-node step without explicit nodes runs UNNUMBERED
// (nodeNumber == nil). The gate decision and the survivor filter must both
// treat it as a real survivor when the condition passes — before GatePassed,
// a passing gate fired the else action and the filter wiped the step's only
// output.
func TestDispatchGate_UnnumberedPassFiresThen(t *testing.T) {
	base := &ladr.BaseStep{
		ID:   "select",
		Name: "Select",
		// no Nodes → unnumbered single node
		If: &ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondMatches, Matches: "ANSWER:"},
			Then:      ladr.ContinueAction(),
			Else:      ladr.ContinueAction(),
		},
	}
	results := []nodeResult{{NodeNumber: nil, Output: "evidence...\nANSWER: 1"}}

	outcome := DispatchGate(base, results)
	if !outcome.GatePassed {
		t.Fatal("gate should pass on ANSWER-containing unnumbered output")
	}
	if outcome.Action != OutcomeAdvance {
		t.Fatalf("then action should fire, got %v", outcome.Action)
	}
}

func TestDispatchGate_UnnumberedFailFiresElse(t *testing.T) {
	base := &ladr.BaseStep{
		ID:   "select",
		Name: "Select",
		If: &ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondMatches, Matches: "ANSWER:"},
			Then:      ladr.ContinueAction(),
			Else:      ladr.ContinueAction(),
		},
	}
	results := []nodeResult{{NodeNumber: nil, Output: "evidence only, no seal"}}

	outcome := DispatchGate(base, results)
	if outcome.GatePassed {
		t.Fatal("gate should not pass without ANSWER")
	}
	if outcome.Action != OutcomeAdvance {
		t.Fatalf("else (continue) action should fire, got %v", outcome.Action)
	}
}

// The number-agnostic contract: a NUMBERED node that passes must also set
// GatePassed and populate Survivors — the fix must not regress the
// fan-out pruning path.
func TestDispatchGate_NumberedPassStillReportsSurvivors(t *testing.T) {
	base := &ladr.BaseStep{
		ID:    "attempts",
		Name:  "Attempts",
		Nodes: &ladr.Nodes{Kind: ladr.NodesNumber, Num: 3},
		If: &ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondMatches, Matches: "ANSWER:"},
			Then:      ladr.ContinueAction(),
			Else:      ladr.ContinueAction(),
		},
	}
	results := []nodeResult{
		{NodeNumber: intPtr(1), Output: "dead end"},
		{NodeNumber: intPtr(2), Output: "ANSWER: 5"},
		{NodeNumber: intPtr(3), Output: "also dead"},
	}

	outcome := DispatchGate(base, results)
	if !outcome.GatePassed {
		t.Fatal("gate should pass")
	}
	if len(outcome.Survivors) != 1 || outcome.Survivors[0] != 2 {
		t.Fatalf("survivors = %v, want [2]", outcome.Survivors)
	}
}

func TestDispatchGate_UnnumberedJumpCarriesOutput(t *testing.T) {
	// The live v7 pattern: select gates on ANSWER: and jumps to reflect on
	// pass. On an unnumbered step the pre-fix engine fired the else branch
	// here — the jump never happened and the output was wiped.
	base := &ladr.BaseStep{
		ID:   "select",
		Name: "Select",
		If: &ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondMatches, Matches: "ANSWER:"},
			Then:      ladr.Action{Kind: ladr.ActionJump, Jump: &ladr.JumpTarget{StepID: "reflect"}},
			Else:      ladr.ContinueAction(),
		},
	}
	results := []nodeResult{{NodeNumber: nil, Output: "reasoning...\nANSWER: 42"}}

	outcome := DispatchGate(base, results)
	if outcome.Action != OutcomeJump || outcome.JumpStepID != "reflect" {
		t.Fatalf("want jump to reflect, got action %v step %q", outcome.Action, outcome.JumpStepID)
	}
	if !outcome.GatePassed {
		t.Fatal("GatePassed must be true on the jump path")
	}
}

// Schema no-ops must come back as *WriteNoOpError with a reason, so the
// run trace shows why nothing was written.
func TestApplyRelikWrite_NoOpsAreTypedAndVisible(t *testing.T) {
	we := NewWriteExecutor(nil) // store never reached on no-op paths
	schema, _ := json.Marshal(map[string]any{
		"type":       "object",
		"required":   []string{"hinge"},
		"properties": map[string]any{"hinge": map[string]any{"type": "string", "maxLength": 180}},
	})
	sw := ladr.RelikWrite{Match: schema}

	if err := we.ApplyRelikWrite("pure prose, no JSON", sw); err == nil {
		t.Fatal("no-JSON output should return an error, not nil")
	} else {
		noop, ok := err.(*WriteNoOpError)
		if !ok {
			t.Fatalf("want *WriteNoOpError, got %T: %v", err, err)
		}
		if noop.Reason == "" {
			t.Fatal("no-op reason must be populated")
		}
	}

	// Valid JSON but hinge over the schema's maxLength — the exact silent
	// no-op that once ate episodes overnight.
	long := `{"hinge": "` + repeat("x", 200) + `"}`
	if err := we.ApplyRelikWrite(long, sw); err == nil {
		t.Fatal("schema-violating output should return an error, not nil")
	} else if _, ok := err.(*WriteNoOpError); !ok {
		t.Fatalf("want *WriteNoOpError, got %T: %v", err, err)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
