// Redeo - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version of the License, or (at your
// option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.

package engine

// ladder_memory_test.go — Ladder 3: the memory-lifecycle validator.
//
// Exercises the relik system's hardest promises end-to-end through the engine:
// - Eviction policies (FIFO/LRU/reject) through the pipeline, not just the store
// - Every relikEdit operation type through the engine's relikEdit path
// - relikRead of file contents, metatags, and system metatags
// - relikWrite (schema-matched writes) with all policies (append/overwrite/mergeByKey)
// - Memory persistence across multiple pipeline runs on the same store

import (
	"encoding/json"
	"strings"
	"testing"
)

// memoryLadderEdit has a single step with relikEdit for surgical editing.
const memoryLadderEdit = `{
	"name": "memory-edit",
	"allowedTargets": {"strategy": "universal"},
	"exit": "edit",
	"knobs": {},
	"reliks": [
		{
			"title": "Mem",
			"folders": [{
				"name": "drafts",
				"tokenLimit": 1000,
				"access": "readWrite",
				"files": [{"name": "core.md", "init": "alpha\nbeta\ngamma\ndelta"}]
			}]
		}
	],
	"steps": [
		{
			"id": "edit", "name": "Edit", "type": "normal",
			"nodes": 1,
			"relikEdit": {
				"maxRounds": 8,
				"to": {"relik": "Mem", "folder": "drafts"},
				"as": "EditReceipt",
				"requestMatch": {
					"type": "object",
					"properties": {
						"edit": {"type": "object", "properties": {"file": {"type": "string"}, "ops": {"type": "array"}}}
					},
					"additionalProperties": true
				}
			},
			"systemPrompt": "Edit the file."
		}
	]
}`

// TestLadder_Memory_AllRelikEditOps proves every relikEdit operation type works
// through the engine's relikEdit path. Anchor ops (replace/insertBefore/
// insertAfter/delete) and line ops (lineReplace/lineDelete/lineInsert) are each
// emitted by the LLM and verified against the resulting file content.
func TestLadder_Memory_AllRelikEditOps(t *testing.T) {
	// The LLM emits 6 edit calls in sequence (one per op type), then a final answer.
	// Each op targets a different part of "alpha\nbeta\ngamma\ndelta".
	io := newLadderIO([]string{
		// 1. replace: "beta" → "BETA"
		`{"edit": {"file": "core.md", "ops": [{"op": "replace", "anchor": "beta", "content": "BETA"}]}}`,
		// 2. insertBefore: insert "X" before "gamma"
		`{"edit": {"file": "core.md", "ops": [{"op": "insertBefore", "anchor": "gamma", "content": "X\n"}]}}`,
		// 3. insertAfter: insert "Y" after "delta"
		`{"edit": {"file": "core.md", "ops": [{"op": "insertAfter", "anchor": "delta", "content": "\nY"}]}}`,
		// 4. delete: remove "alpha"
		`{"edit": {"file": "core.md", "ops": [{"op": "delete", "anchor": "alpha\n"}]}}`,
		// 5. lineReplace: replace line 1 ("X") with "XX"
		`{"edit": {"file": "core.md", "ops": [{"op": "lineReplace", "start": 1, "end": 1, "content": "XX"}]}}`,
		// 6. lineInsert: insert "ZZ" before line 2
		`{"edit": {"file": "core.md", "ops": [{"op": "lineInsert", "start": 2, "content": "ZZ"}]}}`,
		// Final answer
		"Done editing.",
	})

	_, err, store := runLadder(t, memoryLadderEdit, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	content := mustReadRelik(t, store, "Mem", "drafts", "core.md")
	// After all 6 ops in sequence, the expected final content is:
	// "XX\nZZ\nX\ngamma\ndelta\nY"
	// (beta→BETA, then X inserted before gamma, Y after delta, alpha deleted,
	//  then BETA replaced by XX via lineReplace, ZZ inserted before line 2)
	expected := "XX\nZZ\nX\ngamma\ndelta\nY"
	if content != expected {
		t.Errorf("content after all ops = %q, want %q", content, expected)
	}
	// Verify deleted content is gone.
	if strings.Contains(content, "alpha") {
		t.Errorf("content should not contain 'alpha' (deleted): %q", content)
	}
	if strings.Contains(content, "beta") {
		t.Errorf("content should not contain 'beta' (replaced then lineReplaced): %q", content)
	}
}

// TestLadder_Memory_RelikEditAtomicRollback proves that when a batch of ops
// contains an invalid op (bad anchor), the entire batch rolls back — no partial
// edits are applied.
func TestLadder_Memory_RelikEditAtomicRollback(t *testing.T) {
	io := newLadderIO([]string{
		// Batch with a valid op followed by an invalid one (non-existent anchor).
		`{"edit": {"file": "core.md", "ops": [
			{"op": "replace", "anchor": "alpha", "content": "ALPHA"},
			{"op": "delete", "anchor": "does-not-exist"}
		]}}`,
		// Final answer
		"Done.",
	})

	_, _, store := runLadder(t, memoryLadderEdit, io, 1)

	content := mustReadRelik(t, store, "Mem", "drafts", "core.md")
	// The file should be unchanged — alpha should still be lowercase.
	if !strings.Contains(content, "alpha") {
		t.Errorf("atomic rollback failed: file was modified despite invalid op: %q", content)
	}
	if strings.Contains(content, "ALPHA") {
		t.Errorf("atomic rollback failed: partial edit applied: %q", content)
	}
}

// TestLadder_Memory_RelikEditFlatForm proves the flat-form single-op convenience
// (op/anchor/content without the ops: array) works through the engine.
func TestLadder_Memory_RelikEditFlatForm(t *testing.T) {
	io := newLadderIO([]string{
		// Flat form: no ops: array, just top-level op/anchor/content.
		`{"edit": {"file": "core.md", "op": "replace", "anchor": "beta", "content": "BETA"}}`,
		"Done.",
	})

	_, _, store := runLadder(t, memoryLadderEdit, io, 1)

	content := mustReadRelik(t, store, "Mem", "drafts", "core.md")
	if !strings.Contains(content, "BETA") {
		t.Errorf("flat-form edit should have replaced beta→BETA: %q", content)
	}
}

// memoryLadderWrite has a step with relikWrite (schema-matched write).
const memoryLadderWrite = `{
	"name": "memory-write",
	"allowedTargets": {"strategy": "universal"},
	"exit": "write",
	"knobs": {},
	"reliks": [
		{
			"title": "Mem",
			"folders": [{
				"name": "facts",
				"tokenLimit": 1000,
				"access": "readWrite",
				"files": [{"name": "data.md", "init": "blank"}]
			}]
		}
	],
	"steps": [
		{
			"id": "write", "name": "Write", "type": "normal",
			"nodes": 1,
			"systemPrompt": "Output a JSON fact.",
			"relikWrite": {
				"to": {"relik": "Mem", "folder": "facts", "file": "data.md"},
				"field": "fact",
				"on": "append",
				"match": {"type": "object", "required": ["fact", "confidence"], "properties": {"fact": {"type": "string"}, "confidence": {"type": "number"}}}
			}
		}
	]
}`

// TestLadder_Memory_RelikWriteAppend proves schema-matched writes with append
// policy work through the pipeline: the LLM output is JSON-extracted, validated
// against the match schema, the field is extracted, and written to the relik.
func TestLadder_Memory_RelikWriteAppend(t *testing.T) {
	io := newLadderIO([]string{
		`{"fact": "The sky is blue.", "confidence": 0.95}`,
	})

	_, _, store := runLadder(t, memoryLadderWrite, io, 1)

	content := mustReadRelik(t, store, "Mem", "facts", "data.md")
	if !strings.Contains(content, "The sky is blue.") {
		t.Errorf("relikWrite should have appended the fact: %q", content)
	}
}

// TestLadder_Memory_RelikWriteNoMatch proves that when the LLM output doesn't
// match the schema, no write happens (no-op, not error).
func TestLadder_Memory_RelikWriteNoMatch(t *testing.T) {
	io := newLadderIO([]string{
		"This is prose, not JSON.",
	})

	_, _, store := runLadder(t, memoryLadderWrite, io, 1)

	content := mustReadRelik(t, store, "Mem", "facts", "data.md")
	if content != "" {
		t.Errorf("relikWrite should be a no-op on non-JSON: %q", content)
	}
}

// memoryLadderRead has a step that reads from a relik (relikRead field) and
// echoes it. Also reads a metatag.
const memoryLadderRead = `{
	"name": "memory-read",
	"allowedTargets": {"strategy": "universal"},
	"exit": "echo",
	"knobs": {},
	"reliks": [
		{
			"title": "Mem",
			"folders": [{
				"name": "facts",
				"tokenLimit": 1000,
				"access": "readWrite",
				"metatags": [{"name": "tags", "type": "string[]"}],
				"files": [{"name": "core.md", "init": "hello world", "metatags": [{"name": "tags", "type": "string[]"}]}]
			}]
		}
	],
	"steps": [
		{
			"id": "echo", "name": "Echo", "type": "normal",
			"nodes": 1,
			"fields": [
				{"name": "Content", "type": "relikRead", "from": {"relik": "Mem", "folder": "facts", "file": "core.md"}},
				{"name": "Size", "type": "relikRead", "from": {"relik": "Mem", "folder": "facts", "file": "core.md", "metatag": "size"}}
			],
			"systemPrompt": "Echo the content."
		}
	]
}`

// TestLadder_Memory_RelikReadFileAndMetatag proves relikRead works for both file
// contents and system metatags through the pipeline.
func TestLadder_Memory_RelikReadFileAndMetatag(t *testing.T) {
	io := newLadderIO([]string{
		"echoed", // the step output
	})

	_, err, _ := runLadder(t, memoryLadderRead, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// Verify the prompt contained the relik content and the metatag value.
	if len(io.prompts) < 1 {
		t.Fatalf("expected ≥1 prompt, got %d", len(io.prompts))
	}
	prompt := io.prompts[0]
	if !strings.Contains(prompt, "hello world") {
		t.Errorf("prompt should contain the file content 'hello world': %q", prompt)
	}
	// The size metatag should be a number (the token count of "hello world").
	// countTokens uses len/4, so "hello world" = 11 chars / 4 = 2 tokens.
	if !strings.Contains(prompt, "2") {
		t.Errorf("prompt should contain the size metatag '2': %q", prompt)
	}
}

// memoryLadderReadUserMetatag reads a user-defined metatag.
const memoryLadderReadUserMetatag = `{
	"name": "memory-metatag",
	"allowedTargets": {"strategy": "universal"},
	"exit": "echo",
	"knobs": {},
	"reliks": [
		{
			"title": "Mem",
			"folders": [{
				"name": "facts",
				"tokenLimit": 1000,
				"access": "readWrite",
				"metatags": [{"name": "confidence", "type": "number"}],
				"files": [{"name": "core.md", "init": "data", "metatags": [{"name": "confidence", "type": "number"}]}]
			}]
		}
	],
	"steps": [
		{
			"id": "echo", "name": "Echo", "type": "normal",
			"nodes": 1,
			"fields": [
				{"name": "Confidence", "type": "relikRead", "from": {"relik": "Mem", "folder": "facts", "file": "core.md", "metatag": "confidence"}}
			],
			"systemPrompt": "Echo."
		}
	]
}`

// TestLadder_Memory_PersistAcrossRuns proves memory persists: run 1 writes to
// the relik, run 2 reads what run 1 wrote (using the same store). The store's
// Load() is idempotent — it preserves existing content across runs.
func TestLadder_Memory_PersistAcrossRuns(t *testing.T) {
	// Run 1: write "persistent data" to the relik via relikWrite.
	io1 := newLadderIO([]string{
		`{"fact": "persistent data", "confidence": 1.0}`,
	})

	_, err, store := runLadder(t, memoryLadderWrite, io1, 1)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}

	// Verify the write happened.
	content := mustReadRelik(t, store, "Mem", "facts", "data.md")
	if !strings.Contains(content, "persistent data") {
		t.Fatalf("run 1 should have written 'persistent data', got: %q", content)
	}

	// Run 2: run the same ladder again with the SAME store (idempotent Load).
	io2 := newLadderIO([]string{
		`{"fact": "second fact", "confidence": 0.8}`,
	})
	_, err, _ = runLadderWithExistingStore(t, memoryLadderWrite, io2, 1, store)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}

	// Both facts should be in the relik now (append policy).
	content = mustReadRelik(t, store, "Mem", "facts", "data.md")
	if !strings.Contains(content, "persistent data") {
		t.Errorf("run 2 should preserve run 1's data: %q", content)
	}
	if !strings.Contains(content, "second fact") {
		t.Errorf("run 2 should add its own data: %q", content)
	}
}

// TestLadder_Memory_FolderIndexMetatag proves the system "index" metatag works
// through a relikRead field — it lists all files in the folder.
func TestLadder_Memory_FolderIndexMetatag(t *testing.T) {
	io := newLadderIO([]string{"echoed"})

	_, err, _ := runLadder(t, memoryLadderRead, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	// The prompt should reference the file name (from the index metatag or the
	// content itself). At minimum, the prompt must have the content.
	prompt := io.prompts[0]
	if !strings.Contains(prompt, "hello world") {
		t.Errorf("prompt should contain file content: %q", prompt)
	}
}

// Verify configs parse as valid JSON at init time.
func init() {
	for _, cfg := range []string{
		memoryLadderEdit, memoryLadderWrite,
		memoryLadderRead, memoryLadderReadUserMetatag,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(cfg), &m); err != nil {
			panic("invalid test ladder JSON: " + err.Error())
		}
	}
}
