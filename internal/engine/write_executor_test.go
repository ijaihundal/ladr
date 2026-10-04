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
	"github.com/ladr/ladr/internal/relik"
)

func TestWriteExecutor_SchemaMatchedWrite(t *testing.T) {
	store := relik.NewMemoryStore()
	_ = store.Load([]ladr.RelikDecl{{
		Title: "Memory",
		Folders: []ladr.FolderDecl{{
			Name:       "facts",
			TokenLimit: 500,
			Access:     ladr.FolderAccessReadWrite,
			Files:      []ladr.FileDecl{{Name: "core.md", Init: "blank"}},
		}},
	}})

	we := NewWriteExecutor(store)

	sw := ladr.RelikWrite{
		To:    ladr.RelikTarget{Relik: "Memory", Folder: "facts", File: "core.md"},
		Field: "fact",
		On:    ladr.WriteAppend,
		Match: json.RawMessage(`{
			"type": "object",
			"required": ["fact", "confidence"],
			"properties": {
				"fact": {"type": "string", "minLength": 1},
				"confidence": {"type": "number", "minimum": 0, "maximum": 1}
			},
			"additionalProperties": false
		}`),
	}

	// Valid output — should write the "fact" field
	output := `{"fact": "The Earth is round", "confidence": 0.95}`
	if err := we.ApplyRelikWrite(output, sw); err != nil {
		t.Fatalf("ApplyRelikWrite failed: %v", err)
	}

	content, _ := store.ReadFile("Memory", "facts", "core.md")
	if content != "The Earth is round" {
		t.Errorf("expected 'The Earth is round', got %q", content)
	}

	// Append another fact
	output2 := `{"fact": "Water is wet", "confidence": 0.99}`
	we.ApplyRelikWrite(output2, sw)
	content, _ = store.ReadFile("Memory", "facts", "core.md")
	if content != "The Earth is round\n\nWater is wet" {
		t.Errorf("expected appended content, got %q", content)
	}
}

func TestWriteExecutor_NoMatch_NoOp(t *testing.T) {
	store := relik.NewMemoryStore()
	_ = store.Load([]ladr.RelikDecl{{
		Title: "Memory",
		Folders: []ladr.FolderDecl{{
			Name:       "facts",
			TokenLimit: 500,
			Files:      []ladr.FileDecl{{Name: "core.md", Init: "blank"}},
		}},
	}})

	we := NewWriteExecutor(store)

	sw := ladr.RelikWrite{
		To:    ladr.RelikTarget{Relik: "Memory", Folder: "facts", File: "core.md"},
		On:    ladr.WriteAppend,
		Match: json.RawMessage(`{"type": "object", "required": ["fact"]}`),
	}

	// Output that doesn't match schema (no JSON at all)
	we.ApplyRelikWrite("This is just prose, no JSON here.", sw)

	// Content should be unchanged (empty/blank)
	content, _ := store.ReadFile("Memory", "facts", "core.md")
	if content != "" {
		t.Errorf("expected no write on no-match, got %q", content)
	}

	// Output with wrong schema (missing required field)
	we.ApplyRelikWrite(`{"wrong": "field"}`, sw)
	content, _ = store.ReadFile("Memory", "facts", "core.md")
	if content != "" {
		t.Errorf("expected no write on schema mismatch, got %q", content)
	}
}

func TestWriteExecutor_MergeByKey(t *testing.T) {
	store := relik.NewMemoryStore()
	_ = store.Load([]ladr.RelikDecl{{
		Title: "Scores",
		Folders: []ladr.FolderDecl{{
			Name:       "data",
			TokenLimit: 1000,
			Files:      []ladr.FileDecl{{Name: "entries.md", Init: "blank"}},
		}},
	}})

	we := NewWriteExecutor(store)

	sw := ladr.RelikWrite{
		To:    ladr.RelikTarget{Relik: "Scores", Folder: "data", File: "entries.md"},
		On:    ladr.WriteMergeByKey,
		Key:   "id",
		Match: json.RawMessage(`{"type": "object", "required": ["id", "score"]}`),
	}

	// Write first item
	we.ApplyRelikWrite(`{"id": "a", "score": 10}`, sw)

	// Write second item
	we.ApplyRelikWrite(`{"id": "b", "score": 20}`, sw)

	// Update first item (same key "a")
	we.ApplyRelikWrite(`{"id": "a", "score": 15}`, sw)

	content, _ := store.ReadFile("Scores", "data", "entries.md")
	// Should have 2 items: a (score 15, updated) and b (score 20)
	if !containsStr(content, `"id": "a"`) || !containsStr(content, `"score": 15`) {
		t.Errorf("expected updated item a with score 15, got: %s", content)
	}
	if !containsStr(content, `"id": "b"`) || !containsStr(content, `"score": 20`) {
		t.Errorf("expected item b with score 20, got: %s", content)
	}
}

func TestWriteExecutor_MetatagWrite(t *testing.T) {
	store := relik.NewMemoryStore()
	_ = store.Load([]ladr.RelikDecl{{
		Title: "Memory",
		Folders: []ladr.FolderDecl{{
			Name:       "facts",
			TokenLimit: 500,
			Metatags:   []ladr.Metatag{{Name: "confidence", Type: ladr.MetatagTypeNumber}},
			Files:      []ladr.FileDecl{{Name: "core.md", Init: "blank"}},
		}},
	}})

	we := NewWriteExecutor(store)

	sw := ladr.RelikWrite{
		To:    ladr.RelikTarget{Relik: "Memory", Folder: "facts", File: "core.md", Metatag: "confidence"},
		On:    ladr.WriteOverwrite,
		Match: json.RawMessage(`{"type": "object", "required": ["confidence"], "properties": {"confidence": {"type": "number"}}}`),
	}

	we.ApplyRelikWrite(`{"confidence": 0.92}`, sw)

	val, _ := store.ReadMetatag("Memory", "facts", "core.md", "confidence")
	if val == nil {
		t.Fatal("expected metatag value, got nil")
	}
	// The value is the whole JSON object {confidence: 0.92} since no field: was specified
	// It's stored as the parsed value
}

func TestDispatchGate_NoGate(t *testing.T) {
	step := &ladr.BaseStep{ID: "test"}
	nodes := []nodeResult{
		{NodeNumber: intPtr(1), Output: "result1"},
		{NodeNumber: intPtr(2), Output: "result2"},
	}
	outcome := DispatchGate(step, nodes)
	if outcome.Action != OutcomeAdvance {
		t.Errorf("expected advance, got %v", outcome.Action)
	}
	if len(outcome.Survivors) != 2 {
		t.Errorf("expected 2 survivors, got %d", len(outcome.Survivors))
	}
}

func TestDispatchGate_ContinueIf_Desugar(t *testing.T) {
	// continueIf: "READY" should desugar to if: { equals: "READY", then: continue, else: abort }
	step := &ladr.BaseStep{ID: "test", ContinueIf: "READY"}
	nodes := []nodeResult{
		{NodeNumber: intPtr(1), Output: "READY"},
		{NodeNumber: intPtr(2), Output: "NOT READY"},
	}
	outcome := DispatchGate(step, nodes)
	if outcome.Action != OutcomeAdvance {
		t.Error("should advance when at least 1 survivor passes")
	}
	if len(outcome.Survivors) != 1 {
		t.Errorf("expected 1 survivor, got %d", len(outcome.Survivors))
	}
	if outcome.Survivors[0] != 1 {
		t.Errorf("expected survivor node 1, got %d", outcome.Survivors[0])
	}
}

func TestDispatchGate_AllPruned_Else(t *testing.T) {
	step := &ladr.BaseStep{
		ID: "test",
		If: &ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondEquals, Equals: "READY"},
			Then:      ladr.ContinueAction(),
			Else:      ladr.AbortAction(),
		},
	}
	nodes := []nodeResult{
		{NodeNumber: intPtr(1), Output: "NOPE"},
		{NodeNumber: intPtr(2), Output: "ALSO NOPE"},
	}
	outcome := DispatchGate(step, nodes)
	if outcome.Action != OutcomeAbort {
		t.Error("should abort when all nodes pruned and else is abort")
	}
}

func TestDispatchGate_Jump(t *testing.T) {
	step := &ladr.BaseStep{
		ID: "test",
		If: &ladr.If{
			Condition: ladr.Condition{Kind: ladr.CondIntegerRange, IntegerRange: [2]int{4, 5}},
			Then:      ladr.ContinueAction(),
			Else: ladr.Action{
				Kind: ladr.ActionJump,
				Jump: &ladr.JumpTarget{StepID: "refine"},
			},
		},
	}
	// Node scores below range → else fires → jump to refine
	nodes := []nodeResult{
		{NodeNumber: intPtr(1), Output: "2"},
	}
	outcome := DispatchGate(step, nodes)
	if outcome.Action != OutcomeJump {
		t.Errorf("expected jump, got %v", outcome.Action)
	}
	if outcome.JumpStepID != "refine" {
		t.Errorf("expected jump to 'refine', got %q", outcome.JumpStepID)
	}
}

// helpers

func intPtr(i int) *int { return &i }

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
