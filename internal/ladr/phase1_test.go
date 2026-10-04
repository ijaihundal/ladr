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

package ladr

import (
	"encoding/json"
	"testing"
)

// TestParseFullLADRConfig tests parsing a complete LADR config with all Phase 1 features.
func TestParseFullLADRConfig(t *testing.T) {
	configJSON := `{
		"name": "Test Ladder",
		"allowedTargets": {"strategy": "universal"},
		"exit": "answer",
		"knobs": {
			"branches": {
				"name": "Branches",
				"type": "nodes",
				"input": "slider",
				"steps": [
					{"title": "Lean", "value": 3},
					{"title": "Balanced", "value": 5, "default": true},
					{"title": "Deep", "value": 8}
				]
			}
		},
		"reliks": [
			{
				"title": "Memory",
				"folders": [
					{
						"name": "facts",
						"tokenLimit": 500,
						"access": "readWrite",
						"metatags": [
							{"name": "tags", "type": "string[]"},
							{"name": "confidence", "type": "number"}
						],
						"files": [
							{"name": "core.md", "init": "blank"}
						]
					}
				]
			}
		],
		"executionBudget": {
			"maxHops": 300,
			"maxSpend": 10.0
		},
		"steps": [
			{
				"id": "generate",
				"name": "Generate",
				"type": "normal",
				"nodes": 5,
				"fields": [
					{"name": "Context", "type": "text", "from": "input.context"},
					{"name": "PriorArt", "type": "relikRead", "from": {"relik": "Memory", "folder": "facts", "file": "core.md"}},
					{"name": "Tags", "type": "relikRead", "from": {"relik": "Memory", "folder": "facts", "metatag": "tags"}},
					{"name": "Available", "type": "manifest"}
				],
				"systemPrompt": "Generate a solution."
			},
			{
				"id": "verify",
				"name": "Verify",
				"type": "normal",
				"nodes": 3,
				"systemPrompt": "Emit JSON {\"score\": 1-5, \"verdict\": \"accept|reject\"}.",
				"if": {
					"integerRange": [4, 5],
					"then": "continue",
					"else": {
						"jump": {"stepId": "refine"}
					}
				},
				"relikWrite": {
					"to": {"relik": "Memory", "folder": "facts", "file": "core.md"},
					"field": "score",
					"on": "append",
					"match": {
						"type": "object",
						"required": ["score"],
						"properties": {"score": {"type": "number"}}
					}
				}
			},
			{
				"id": "refine",
				"name": "Refine",
				"type": "sequential",
				"nodes": 3,
				"systemPrompt": "Refine the solution."
			},
			{
				"id": "answer",
				"name": "Answer",
				"type": "normal",
				"systemPrompt": "Return the solution."
			}
		]
	}`

	cfg, err := ParseConfig(json.RawMessage(configJSON))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	// Verify basic fields.
	if cfg.Name != "Test Ladder" {
		t.Errorf("expected name 'Test Ladder', got %q", cfg.Name)
	}

	// Verify reliks.
	if len(cfg.Reliks) != 1 {
		t.Fatalf("expected 1 relik, got %d", len(cfg.Reliks))
	}
	if cfg.Reliks[0].Title != "Memory" {
		t.Errorf("expected relik title 'Memory', got %q", cfg.Reliks[0].Title)
	}
	if len(cfg.Reliks[0].Folders) != 1 {
		t.Fatalf("expected 1 folder")
	}
	folder := cfg.Reliks[0].Folders[0]
	if folder.TokenLimit != 500 {
		t.Errorf("expected tokenLimit 500, got %d", folder.TokenLimit)
	}
	if len(folder.Metatags) != 2 {
		t.Errorf("expected 2 folder metatags, got %d", len(folder.Metatags))
	}

	// Verify execution budget.
	if cfg.ExecutionBudget == nil {
		t.Fatal("expected executionBudget")
	}
	if cfg.ExecutionBudget.MaxHops == nil || *cfg.ExecutionBudget.MaxHops != 300 {
		t.Error("expected maxHops 300")
	}
	if cfg.ExecutionBudget.MaxSpend == nil || *cfg.ExecutionBudget.MaxSpend != 10.0 {
		t.Error("expected maxSpend 10.0")
	}

	// Verify steps.
	if len(cfg.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(cfg.Steps))
	}

	// Step 0: generate — has relikRead + manifest fields.
	genBase := cfg.Steps[0].Base()
	if genBase == nil {
		t.Fatal("generate step has no base")
	}
	if !genBase.Fields.IsSet() || genBase.Fields.IsString {
		t.Fatal("generate step should have field array")
	}
	fields := genBase.Fields.Fields
	foundRelikRead := false
	foundManifest := false
	for _, f := range fields {
		if f.Kind == FieldRelikRead {
			foundRelikRead = true
			if f.RelikRead.From.Relik != "Memory" {
				t.Errorf("expected relik 'Memory', got %q", f.RelikRead.From.Relik)
			}
		}
		if f.Kind == FieldManifest {
			foundManifest = true
		}
	}
	if !foundRelikRead {
		t.Error("expected at least one relikRead field")
	}
	if !foundManifest {
		t.Error("expected a manifest field")
	}

	// Step 1: verify — has if block with jump + relikWrite.
	verifyBase := cfg.Steps[1].Base()
	if verifyBase.If == nil {
		t.Fatal("verify step should have if block")
	}
	if verifyBase.If.Condition.Kind != CondIntegerRange {
		t.Errorf("expected integerRange condition, got %v", verifyBase.If.Condition.Kind)
	}
	if verifyBase.If.Condition.IntegerRange != [2]int{4, 5} {
		t.Errorf("expected range [4,5], got %v", verifyBase.If.Condition.IntegerRange)
	}
	if verifyBase.If.Then.Kind != ActionContinue {
		t.Error("expected then: continue")
	}
	if verifyBase.If.Else.Kind != ActionJump {
		t.Error("expected else: jump")
	}
	if verifyBase.If.Else.Jump == nil || verifyBase.If.Else.Jump.StepID != "refine" {
		t.Error("expected jump to 'refine'")
	}
	if verifyBase.RelikWrite == nil {
		t.Fatal("verify step should have relikWrite")
	}
	if verifyBase.RelikWrite.To.Relik != "Memory" {
		t.Errorf("expected relikWrite to 'Memory', got %q", verifyBase.RelikWrite.To.Relik)
	}
	if verifyBase.RelikWrite.Field != "score" {
		t.Errorf("expected field 'score', got %q", verifyBase.RelikWrite.Field)
	}
}

func TestParseContinueIfShorthand(t *testing.T) {
	// Old-style continueIf should still parse.
	configJSON := `{
		"name": "Legacy",
		"allowedTargets": {"strategy": "universal"},
		"exit": "s",
		"knobs": {},
		"steps": [{"id": "s", "name": "S", "type": "normal", "nodes": 3, "continueIf": "READY"}]
	}`
	cfg, err := ParseConfig(json.RawMessage(configJSON))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	base := cfg.Steps[0].Base()
	if base.ContinueIf != "READY" {
		t.Errorf("expected continueIf 'READY', got %q", base.ContinueIf)
	}
	if base.If != nil {
		t.Error("expected no If block (continueIf and If are separate)")
	}
}

func TestParseIfShorthand(t *testing.T) {
	// Shorthand: if: "1" = equals "1", then continue, else abort.
	ifBlock := If{}
	if err := json.Unmarshal([]byte(`"1"`), &ifBlock); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ifBlock.Condition.Kind != CondEquals {
		t.Errorf("expected equals, got %v", ifBlock.Condition.Kind)
	}
	if ifBlock.Condition.Equals != "1" {
		t.Errorf("expected '1', got %q", ifBlock.Condition.Equals)
	}
	if ifBlock.Then.Kind != ActionContinue {
		t.Error("expected then continue")
	}
	if ifBlock.Else.Kind != ActionAbort {
		t.Error("expected else abort")
	}
}

func TestParseLadderID(t *testing.T) {
	tests := []struct {
		input   string
		author  string
		name    string
		version string
		isLocal bool
	}{
		{"@alice/helper", "alice", "helper", "", false},
		{"@alice/helper@v3", "alice", "helper", "v3", false},
		{"@org-name/ladder-name", "org-name", "ladder-name", "", false},
		{"local/my-experiment", "local", "my-experiment", "", true},
	}

	for _, tt := range tests {
		id, err := ParseLadderID(tt.input)
		if err != nil {
			t.Errorf("ParseLadderID(%q) error: %v", tt.input, err)
			continue
		}
		if id.Author != tt.author {
			t.Errorf("ParseLadderID(%q): author = %q, want %q", tt.input, id.Author, tt.author)
		}
		if id.Name != tt.name {
			t.Errorf("ParseLadderID(%q): name = %q, want %q", tt.input, id.Name, tt.name)
		}
		if id.Version != tt.version {
			t.Errorf("ParseLadderID(%q): version = %q, want %q", tt.input, id.Version, tt.version)
		}
		if id.IsLocal != tt.isLocal {
			t.Errorf("ParseLadderID(%q): isLocal = %v, want %v", tt.input, id.IsLocal, tt.isLocal)
		}
	}

	// Invalid IDs.
	invalid := []string{"", "no-at-sign", "@", "@/name", "@author/", "local/"}
	for _, s := range invalid {
		if _, err := ParseLadderID(s); err == nil {
			t.Errorf("ParseLadderID(%q) should fail", s)
		}
	}
}

func TestValidateFullConfig(t *testing.T) {
	configJSON := `{
		"name": "Test Ladder",
		"allowedTargets": {"strategy": "universal"},
		"exit": "answer",
		"knobs": {},
		"reliks": [
			{
				"title": "Memory",
				"folders": [{
					"name": "facts",
					"tokenLimit": 500,
					"metatags": [{"name": "score", "type": "number"}],
					"files": [{"name": "core.md", "init": "blank"}]
				}]
			}
		],
		"steps": [
			{
				"id": "generate", "name": "Gen", "type": "normal", "nodes": 3,
				"fields": [
					{"name": "Ctx", "type": "text", "from": "input.context"},
					{"name": "Score", "type": "relikRead", "from": {"relik": "Memory", "folder": "facts", "file": "core.md", "metatag": "score"}}
				],
				"systemPrompt": "Generate."
			},
			{
				"id": "answer", "name": "Ans", "type": "normal",
				"systemPrompt": "Answer."
			}
		]
	}`
	cfg, err := ParseConfig(json.RawMessage(configJSON))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	result := ValidateConfig(cfg)
	if !result.Valid {
		t.Errorf("validation should pass, got errors: %v", result.Errors)
	}
}

func TestValidateInvalidRelikReference(t *testing.T) {
	configJSON := `{
		"name": "Bad",
		"allowedTargets": {"strategy": "universal"},
		"exit": "s",
		"knobs": {},
		"steps": [{
			"id": "s", "name": "S", "type": "normal",
			"fields": [
				{"name": "Bad", "type": "relikRead", "from": {"relik": "Nonexistent", "folder": "facts"}}
			],
			"systemPrompt": "Test."
		}]
	}`
	cfg, _ := ParseConfig(json.RawMessage(configJSON))
	result := ValidateConfig(cfg)
	if result.Valid {
		t.Error("should fail with nonexistent relik reference")
	}
}

func TestValidateJumpToNonexistentStep(t *testing.T) {
	configJSON := `{
		"name": "Bad Jump",
		"allowedTargets": {"strategy": "universal"},
		"exit": "s",
		"knobs": {},
		"steps": [{
			"id": "s", "name": "S", "type": "normal", "nodes": 2,
			"systemPrompt": "Test.",
			"if": {
				"equals": "READY",
				"then": "continue",
				"else": {"jump": {"stepId": "nonexistent"}}
			}
		}]
	}`
	cfg, _ := ParseConfig(json.RawMessage(configJSON))
	result := ValidateConfig(cfg)
	if result.Valid {
		t.Error("should fail with jump to nonexistent step")
	}
}

func TestValidateExecutionBudgetExceedsMax(t *testing.T) {
	configJSON := `{
		"name": "Expensive",
		"allowedTargets": {"strategy": "universal"},
		"exit": "s",
		"knobs": {},
		"executionBudget": {"maxSpend": 999.0},
		"steps": [{"id": "s", "name": "S", "type": "normal", "systemPrompt": "Test."}]
	}`
	cfg, _ := ParseConfig(json.RawMessage(configJSON))
	result := ValidateConfig(cfg)
	if result.Valid {
		t.Error("should fail with maxSpend exceeding platform max")
	}
}
