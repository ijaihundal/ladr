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

func TestEvaluateGate_Equals(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondEquals, Equals: "READY"}
	if !EvaluateGate("READY", cond).Passed {
		t.Error("equals should pass on exact match")
	}
	if EvaluateGate("NOT READY", cond).Passed {
		t.Error("equals should fail on non-match")
	}
}

func TestEvaluateGate_IntegerEquals(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondIntegerEquals, IntegerEquals: 5}
	tests := []struct {
		input string
		pass  bool
	}{
		{"5", true},
		{"5.0", true}, // tolerates trailing .0
		{" 5 ", true}, // tolerates whitespace
		{"4", false},
		{"5.5", false},
		{"abc", false},
	}
	for _, tt := range tests {
		result := EvaluateGate(tt.input, cond)
		if result.Passed != tt.pass {
			t.Errorf("integerEquals(%q) = %v, want %v (reason: %s)", tt.input, result.Passed, tt.pass, result.Reason)
		}
	}
}

func TestEvaluateGate_IntegerRange(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondIntegerRange, IntegerRange: [2]int{1, 5}}
	tests := []struct {
		input string
		pass  bool
	}{
		{"1", true},  // inclusive lower
		{"5", true},  // inclusive upper
		{"3", true},  // middle
		{"0", false}, // below
		{"6", false}, // above
	}
	for _, tt := range tests {
		result := EvaluateGate(tt.input, cond)
		if result.Passed != tt.pass {
			t.Errorf("integerRange [1,5](%q) = %v, want %v", tt.input, result.Passed, tt.pass)
		}
	}
}

func TestEvaluateGate_NumberEquals(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondNumberEquals, NumberEquals: 0.95}
	if !EvaluateGate("0.95", cond).Passed {
		t.Error("numberEquals should pass on exact match")
	}
	if EvaluateGate("0.94", cond).Passed {
		t.Error("numberEquals should fail on non-match")
	}
}

func TestEvaluateGate_NumberRange(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondNumberRange, NumberRange: [2]float64{0.7, 1.0}}
	if !EvaluateGate("0.85", cond).Passed {
		t.Error("0.85 should be in [0.7, 1.0]")
	}
	if EvaluateGate("0.5", cond).Passed {
		t.Error("0.5 should not be in [0.7, 1.0]")
	}
}

func TestEvaluateGate_Contains(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondContains, Contains: "READY"}
	tests := []struct {
		input string
		pass  bool
	}{
		{"READY", true},
		{"The status is READY now", true}, // substring in prose
		{"NOT READY YET", true},           // contains substring
		{"NOTFOUND", false},
	}
	for _, tt := range tests {
		result := EvaluateGate(tt.input, cond)
		if result.Passed != tt.pass {
			t.Errorf("contains(%q) = %v, want %v", tt.input, result.Passed, tt.pass)
		}
	}
}

func TestEvaluateGate_In(t *testing.T) {
	cond := ladr.Condition{Kind: ladr.CondIn, In: []string{"yes", "Y", "accept", "ACCEPT"}}
	if !EvaluateGate("yes", cond).Passed {
		t.Error("'yes' should be in set")
	}
	if !EvaluateGate("ACCEPT", cond).Passed {
		t.Error("'ACCEPT' should be in set")
	}
	if EvaluateGate("no", cond).Passed {
		t.Error("'no' should not be in set")
	}
}

func TestEvaluateGate_JSONMatches(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["verdict", "score"],
		"properties": {
			"verdict": {"type": "string", "enum": ["accept"]},
			"score": {"type": "number", "minimum": 4, "maximum": 5}
		},
		"additionalProperties": false
	}`)
	cond := ladr.Condition{Kind: ladr.CondJSONMatches, JSONMatches: schema}

	// Valid JSON matching schema
	valid := `{"verdict": "accept", "score": 4}`
	if !EvaluateGate(valid, cond).Passed {
		t.Error("valid JSON should pass jsonMatches")
	}

	// JSON with wrong enum value
	invalidEnum := `{"verdict": "reject", "score": 4}`
	if EvaluateGate(invalidEnum, cond).Passed {
		t.Error("wrong enum value should fail jsonMatches")
	}

	// JSON with score out of range
	invalidRange := `{"verdict": "accept", "score": 3}`
	if EvaluateGate(invalidRange, cond).Passed {
		t.Error("score below minimum should fail jsonMatches")
	}

	// JSON missing required field
	missingField := `{"verdict": "accept"}`
	if EvaluateGate(missingField, cond).Passed {
		t.Error("missing required field should fail jsonMatches")
	}

	// JSON with additional properties
	addlProps := `{"verdict": "accept", "score": 4, "extra": true}`
	if EvaluateGate(addlProps, cond).Passed {
		t.Error("additionalProperties: false should reject extra fields")
	}
}

func TestEvaluateGate_JSONMatches_ProseEmbedded(t *testing.T) {
	schema := json.RawMessage(`{"type": "object", "required": ["fact"]}`)
	cond := ladr.Condition{Kind: ladr.CondJSONMatches, JSONMatches: schema}

	// JSON embedded in prose
	proseOutput := `I found something interesting. Here it is: {"fact": "The sky is blue"} That's all.`
	if !EvaluateGate(proseOutput, cond).Passed {
		t.Error("should extract JSON from prose and pass")
	}
}

func TestEvaluateGate_JSONMatches_CodeFenced(t *testing.T) {
	schema := json.RawMessage(`{"type": "object", "required": ["count"]}`)
	cond := ladr.Condition{Kind: ladr.CondJSONMatches, JSONMatches: schema}

	fencedOutput := "Here's the result:\n```json\n{\"count\": 42}\n```\nDone."
	if !EvaluateGate(fencedOutput, cond).Passed {
		t.Error("should extract JSON from code fence and pass")
	}
}

func TestEvaluateGate_NoJSON(t *testing.T) {
	schema := json.RawMessage(`{"type": "object"}`)
	cond := ladr.Condition{Kind: ladr.CondJSONMatches, JSONMatches: schema}

	if EvaluateGate("This is just prose, no JSON here.", cond).Passed {
		t.Error("should fail when no JSON is found")
	}
}
