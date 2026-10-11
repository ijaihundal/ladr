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

package engine

import (
	"encoding/json"
	"testing"
)

func TestExtractJSONFromOutput_DirectJSON(t *testing.T) {
	raw, err := ExtractJSONFromOutput(`{"key": "value"}`)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["key"] != "value" {
		t.Errorf("expected 'value', got %v", m["key"])
	}
}

func TestExtractJSONFromOutput_CodeFenced(t *testing.T) {
	input := "Here's the result:\n```json\n{\"count\": 42}\n```\nDone."
	raw, err := ExtractJSONFromOutput(input)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["count"].(float64) != 42 {
		t.Errorf("expected 42, got %v", m["count"])
	}
}

func TestExtractJSONFromOutput_ProseEmbedded(t *testing.T) {
	input := `I analyzed the data. Here's what I found: {"score": 4, "verdict": "accept"} That's my conclusion.`
	raw, err := ExtractJSONFromOutput(input)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["verdict"] != "accept" {
		t.Errorf("expected 'accept', got %v", m["verdict"])
	}
}

func TestExtractJSONFromOutput_ArrayJSON(t *testing.T) {
	input := `[{"id": 1}, {"id": 2}]`
	raw, err := ExtractJSONFromOutput(input)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	var arr []any
	json.Unmarshal(raw, &arr)
	if len(arr) != 2 {
		t.Errorf("expected 2 items, got %d", len(arr))
	}
}

func TestExtractJSONFromOutput_NoJSON(t *testing.T) {
	_, err := ExtractJSONFromOutput("Just prose, nothing to parse here.")
	if err == nil {
		t.Error("should fail when no JSON found")
	}
}

func TestValidateAgainstJSONSchema_Type(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		schema string
		passes bool
	}{
		{"object matches", map[string]any{"a": 1}, `{"type": "object"}`, true},
		{"object mismatch", "string", `{"type": "object"}`, false},
		{"array matches", []any{1, 2}, `{"type": "array"}`, false}, // wait, it's actually an array
		{"string matches", "hello", `{"type": "string"}`, true},
		{"number matches", 3.14, `{"type": "number"}`, true},
		{"integer matches", 42.0, `{"type": "integer"}`, true},
		{"integer mismatch", 3.14, `{"type": "integer"}`, false},
		{"boolean matches", true, `{"type": "boolean"}`, true},
	}

	for _, tt := range tests {
		var schema any
		json.Unmarshal([]byte(tt.schema), &schema)
		err := ValidateAgainstJSONSchema(tt.value, schema)
		// Fix: array case
		if tt.name == "array matches" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tt.name, err)
			}
			continue
		}
		if tt.passes && err != nil {
			t.Errorf("%s: expected pass, got error: %v", tt.name, err)
		}
		if !tt.passes && err == nil {
			t.Errorf("%s: expected fail, but passed", tt.name)
		}
	}
}

func TestValidateAgainstJSONSchema_Required(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"name", "score"},
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"score": map[string]any{"type": "number"},
		},
	}

	// All required present.
	valid := map[string]any{"name": "test", "score": 4.5}
	if err := ValidateAgainstJSONSchema(valid, schema); err != nil {
		t.Errorf("expected pass: %v", err)
	}

	// Missing required field.
	missing := map[string]any{"name": "test"}
	if err := ValidateAgainstJSONSchema(missing, schema); err == nil {
		t.Error("expected fail on missing required field")
	}
}

func TestValidateAgainstJSONSchema_Enum(t *testing.T) {
	schema := map[string]any{
		"type": "string",
		"enum": []any{"accept", "reject"},
	}

	if err := ValidateAgainstJSONSchema("accept", schema); err != nil {
		t.Errorf("expected pass: %v", err)
	}
	if err := ValidateAgainstJSONSchema("maybe", schema); err == nil {
		t.Error("expected fail for non-enum value")
	}
}

func TestValidateAgainstJSONSchema_MinMax(t *testing.T) {
	schema := map[string]any{
		"type":    "number",
		"minimum": 1.0,
		"maximum": 10.0,
	}

	if err := ValidateAgainstJSONSchema(5.0, schema); err != nil {
		t.Errorf("expected pass for 5: %v", err)
	}
	if err := ValidateAgainstJSONSchema(0.5, schema); err == nil {
		t.Error("expected fail below minimum")
	}
	if err := ValidateAgainstJSONSchema(15.0, schema); err == nil {
		t.Error("expected fail above maximum")
	}
}

func TestValidateAgainstJSONSchema_AdditionalProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}

	// Only declared property.
	valid := map[string]any{"a": "hello"}
	if err := ValidateAgainstJSONSchema(valid, schema); err != nil {
		t.Errorf("expected pass: %v", err)
	}

	// Extra property.
	invalid := map[string]any{"a": "hello", "b": "world"}
	if err := ValidateAgainstJSONSchema(invalid, schema); err == nil {
		t.Error("expected fail with additionalProperties: false")
	}
}

func TestValidateAgainstJSONSchema_NestedItems(t *testing.T) {
	schema := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":     "object",
			"required": []any{"id"},
			"properties": map[string]any{
				"id": map[string]any{"type": "number"},
			},
		},
		"minItems": 1.0,
		"maxItems": 3.0,
	}

	// Valid array.
	valid := []any{
		map[string]any{"id": 1.0},
		map[string]any{"id": 2.0},
	}
	if err := ValidateAgainstJSONSchema(valid, schema); err != nil {
		t.Errorf("expected pass: %v", err)
	}

	// Too many items.
	tooMany := []any{
		map[string]any{"id": 1.0},
		map[string]any{"id": 2.0},
		map[string]any{"id": 3.0},
		map[string]any{"id": 4.0},
	}
	if err := ValidateAgainstJSONSchema(tooMany, schema); err == nil {
		t.Error("expected fail with too many items")
	}

	// Item missing required field.
	invalidItem := []any{
		map[string]any{"name": "no id"},
	}
	if err := ValidateAgainstJSONSchema(invalidItem, schema); err == nil {
		t.Error("expected fail with item missing required field")
	}
}

func TestValidateAgainstJSONSchema_MinLength(t *testing.T) {
	schema := map[string]any{
		"type":      "string",
		"minLength": 3.0,
		"maxLength": 10.0,
	}

	if err := ValidateAgainstJSONSchema("hello", schema); err != nil {
		t.Errorf("expected pass: %v", err)
	}
	if err := ValidateAgainstJSONSchema("hi", schema); err == nil {
		t.Error("expected fail for too-short string")
	}
	if err := ValidateAgainstJSONSchema("this is way too long", schema); err == nil {
		t.Error("expected fail for too-long string")
	}
}
