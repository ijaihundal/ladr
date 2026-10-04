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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// fakeConfigEditor mirrors the pg editor's contract: validate-before-commit,
// protected keys queue, version history — all in memory.
type fakeConfigEditor struct {
	mu          sync.Mutex
	config      string // current stored config (pretty)
	version     int
	approvals   []string // queued protected-key diffs
	lastMessage string
}

func (f *fakeConfigEditor) EditConfig(_ context.Context, _ string, _ []byte, ops []relik.EditOp, message string) (*ConfigEditResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := &ConfigEditResult{}
	working, statuses, ok := relik.ApplyEditOps(f.config, ops)
	res.OpStatuses = statuses
	if !ok {
		res.ValidationErrors = []string{"ops rejected — nothing committed"}
		return res, nil
	}
	parsed, err := ladr.ParseStoredConfig([]byte(working))
	if err != nil {
		res.ValidationErrors = []string{"parse: " + err.Error()}
		return res, nil
	}
	if v := ladr.ValidateConfig(parsed); !v.Valid {
		res.ValidationErrors = v.Errors
		return res, nil
	}
	var oldMap, newMap map[string]any
	_ = json.Unmarshal([]byte(f.config), &oldMap)
	_ = json.Unmarshal([]byte(working), &newMap)
	var protected []string
	for _, pk := range []string{"executionBudget", "allowedTargets"} {
		if fmt.Sprint(oldMap[pk]) != fmt.Sprint(newMap[pk]) {
			protected = append(protected, pk)
		}
	}
	res.ProtectedTouched = protected
	if len(protected) > 0 {
		f.approvals = append(f.approvals, strings.Join(protected, ","))
		res.QueuedApproval = true
		res.ApprovalID = "approval-1"
		return res, nil
	}
	f.config = working
	f.version++
	f.lastMessage = message
	res.Applied = true
	res.Version = f.version
	res.ChangedKeys = []string{"steps"}
	return res, nil
}

func configEditTestSetup(t *testing.T) (*fakeConfigEditor, *ExecutionRuntimeContext) {
	t.Helper()
	editor := &fakeConfigEditor{config: `{
  "name": "self-editor",
  "exit": "go",
  "allowedTargets": {"strategy": "universal"},
  "knobs": {},
  "steps": [{"id": "go", "name": "go", "type": "normal", "systemPrompt": "You are helpful."}]
}`}
	runtime := &ExecutionRuntimeContext{
		InstanceID:   "cfg-edit-1",
		ConfigID:     "config-uuid-1",
		ConfigEditor: editor,
	}
	return editor, runtime
}

func mkConfigEditCall(ops string, message string) string {
	// runConfigEditToolAdaptive receives the INNER object (the dispatch
	// already peeled the "configEdit" key off the tool call).
	return fmt.Sprintf(`{"ops": [%s], "message": %q}`, ops, message)
}

func TestConfigEdit_RewritesOwnPrompt(t *testing.T) {
	editor, runtime := configEditTestSetup(t)
	ced := &ladr.ConfigEditConfig{MaxRounds: 2, As: "EditReceipt"}
	call := json.RawMessage(mkConfigEditCall(`{"op": "replace", "anchor": "You are helpful.", "content": "You are sharply focused."}`, "tighten the prompt"))

	field, err := runConfigEditToolAdaptive(context.Background(), ced, call, runtime, "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(field.Value.(string), `"applied": true`) {
		t.Fatalf("edit should apply, receipt: %s", field.Value)
	}
	if !strings.Contains(editor.config, "sharply focused") {
		t.Error("stored config should carry the new prompt")
	}
	if editor.lastMessage != "tighten the prompt" {
		t.Error("self-authored commit message should be recorded")
	}
}

func TestConfigEdit_ProtectedKeyQueuesNotLands(t *testing.T) {
	editor, runtime := configEditTestSetup(t)
	ced := &ladr.ConfigEditConfig{MaxRounds: 2, As: "EditReceipt"}
	// Attempt to widen allowedTargets (protected).
	call := json.RawMessage(mkConfigEditCall(`{"op": "replace", "anchor": "\"allowedTargets\": {\"strategy\": \"universal\"}", "content": "\"allowedTargets\": {\"strategy\": \"universal\", \"models\": [\"*\"]}"}`, "widen models"))

	field, err := runConfigEditToolAdaptive(context.Background(), ced, call, runtime, "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(field.Value.(string), `"queuedApproval": true`) {
		t.Fatalf("protected edit should queue, receipt: %s", field.Value)
	}
	if strings.Contains(editor.config, `"models"`) {
		t.Error("protected edit must NOT land in the live config")
	}
	if len(editor.approvals) != 1 {
		t.Error("approval should be queued")
	}
}

func TestConfigEdit_BrokenConfigCannotCommit(t *testing.T) {
	_, runtime := configEditTestSetup(t)
	ced := &ladr.ConfigEditConfig{MaxRounds: 2, As: "EditReceipt"}
	// Break the config: replace the exit step id so exit points nowhere.
	call := json.RawMessage(mkConfigEditCall(`{"op": "replace", "anchor": "\"exit\": \"go\"", "content": "\"exit\": \"nonexistent\""} `, "rename exit"))

	field, err := runConfigEditToolAdaptive(context.Background(), ced, call, runtime, "go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(field.Value.(string), `"applied": true`) {
		t.Fatalf("broken config must not commit, receipt: %s", field.Value)
	}
	if !strings.Contains(field.Value.(string), "ValidationErrors") && !strings.Contains(field.Value.(string), "validationErrors") {
		t.Error("receipt should explain why the commit was refused")
	}
}

func TestConfigEdit_ReceiptInjectsAsField(t *testing.T) {
	// The receipt rides the adaptive loop's injection mechanism — the model
	// sees the consequence and produces its final answer next turn.
	_, runtime := configEditTestSetup(t)
	ced := &ladr.ConfigEditConfig{MaxRounds: 2, As: "EditReceipt"}
	call := json.RawMessage(mkConfigEditCall(`{"op": "replace", "anchor": "You are helpful.", "content": "You are terse."}`, "be terse"))
	field, err := runConfigEditToolAdaptive(context.Background(), ced, call, runtime, "go")
	if err != nil {
		t.Fatal(err)
	}
	if field.Name != "EditReceipt" {
		t.Errorf("receipt should inject under the declared as: name, got %q", field.Name)
	}
}
