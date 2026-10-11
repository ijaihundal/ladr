// relikedit_guard_test.go — proves a relikEdit-declared step can never end as a
// silent no-write. Live failure this guards against: the climb's consolidate
// ladder minted moves in its report while apply_moves' edit call never matched
// the request shape, so the loop returned the prose as a "final answer" and the
// playbook silently stayed at init for two consolidate cycles.
package engine

import (
	"strings"
	"testing"
)

// guardLadder is memoryLadderEdit with a small maxRounds so the retry bound is
// reachable in a short scripted sequence.
const guardLadder = `{
	"name": "memory-edit-guard",
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
				"maxRounds": 3,
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

// The model never emits an edit call: the step must consume exactly maxRounds
// rounds (feedback + retry, not instant silent return), leave the file
// untouched, and publish a loud non-fatal error event.
func TestLadder_Memory_RelikEditNoEditIsLoudAndBounded(t *testing.T) {
	io := newLadderIO([]string{
		"Here are the moves I would apply.",
		"Moves: replace beta with BETA (strength 2).",
		"Cannot produce an edit call.",
	})

	_, err, store := runLadder(t, guardLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if got := io.llmIdx; got != 3 {
		t.Errorf("llm calls = %d, want 3 (feedback rounds, not an instant final answer)", got)
	}
	if content := mustReadRelik(t, store, "Mem", "drafts", "core.md"); content != "alpha\nbeta\ngamma\ndelta" {
		t.Errorf("file must be untouched, got %q", content)
	}
	found := false
	for _, ev := range io.events {
		if ev == string(EventError) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an %q event when a relikEdit step ends without applying any edit, got events %v", string(EventError), io.events)
	}
}

// The model misses the shape once, gets the ToolError feedback, applies the
// edit on the retry, and only then is a non-matching output a real final
// answer — with no error event.
func TestLadder_Memory_RelikEditRescueThenApply(t *testing.T) {
	io := newLadderIO([]string{
		"I will edit the file now.",
		`{"edit": {"file": "core.md", "ops": [{"op": "replace", "anchor": "beta", "content": "BETA"}]}}`,
		"Done editing.",
	})

	_, err, store := runLadder(t, guardLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if got := io.llmIdx; got != 3 {
		t.Errorf("llm calls = %d, want 3 (miss, edit, final)", got)
	}
	content := mustReadRelik(t, store, "Mem", "drafts", "core.md")
	if !strings.Contains(content, "BETA") {
		t.Errorf("edit from the retry round should be applied, got %q", content)
	}
	for _, ev := range io.events {
		if ev == string(EventError) {
			t.Errorf("no error event expected when the edit lands, got events %v", io.events)
		}
	}
}
