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

// files_loop_test.go — the files: adaptive key through a full ExecutePipeline
// run (mirrors ladder_tools_test.go's pattern: not ExecuteAdaptiveLoop in
// isolation, but the loop fired as a step inside a pipeline).
//
// Covers:
//   - read dispatch ({read:{path}} → FilesOp → content injected as `as` field)
//   - write dispatch ({write:{path,content}} → receipt injected)
//   - list dispatch ({list:{glob}} → entries injected)
//   - tool failure → ToolError feedback, loop continues (non-fatal)

import (
	"errors"
	"strings"
	"testing"
)

// filesLadder is a 1-step ladder with only the files: key enabled.
const filesLadder = `{
	"name": "tools-files",
	"allowedTargets": {"strategy": "universal"},
	"exit": "work",
	"knobs": {},
	"steps": [
		{
			"id": "work", "name": "Work", "type": "normal",
			"nodes": 1,
			"files": {
				"maxRounds": 4,
				"as": "FileResult",
				"requestMatch": {
					"type": "object",
					"properties": {
						"read":  {"type": "object", "properties": {"path": {"type": "string"}}},
						"write": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}}},
						"list":  {"type": "object", "properties": {"glob": {"type": "string"}}}
					},
					"additionalProperties": true
				}
			},
			"systemPrompt": "Work with local files."
		}
	]
}`

func TestLadder_Files_ReadThroughPipeline(t *testing.T) {
	io := newLadderIO([]string{
		`{"read": {"path": "docs/readme.md"}}`,
		"The readme says hello.",
	}).withFilesResult(FilesOpResult{Content: "# hello\nworld"})

	result, err, _ := runLadder(t, filesLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "The readme says hello." {
		t.Errorf("result = %q", result)
	}
	if len(io.filesCalls) != 1 {
		t.Fatalf("expected 1 files call, got %d", len(io.filesCalls))
	}
	call := io.filesCalls[0]
	if call.Op != "read" || call.Path != "docs/readme.md" {
		t.Errorf("files call = op %q path %q, want read/docs/readme.md", call.Op, call.Path)
	}
	// The read content must be injected into the next turn's prompt.
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], "# hello") {
		t.Errorf("round-2 prompt should contain injected file content, got: %q", io.prompts)
	}
}

func TestLadder_Files_WriteThroughPipeline(t *testing.T) {
	io := newLadderIO([]string{
		`{"write": {"path": "notes/todo.txt", "content": "ship it"}}`,
		"Wrote the note.",
	}).withFilesResult(FilesOpResult{Bytes: 7})

	result, err, _ := runLadder(t, filesLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Wrote the note." {
		t.Errorf("result = %q", result)
	}
	call := io.filesCalls[0]
	if call.Op != "write" || call.Path != "notes/todo.txt" || call.Content != "ship it" {
		t.Errorf("files call = %+v", call)
	}
	// Receipt injected into the next turn.
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], "wrote 7 bytes to notes/todo.txt") {
		t.Errorf("round-2 prompt should contain the write receipt, got: %q", io.prompts)
	}
}

func TestLadder_Files_ListThroughPipeline(t *testing.T) {
	io := newLadderIO([]string{
		`{"list": {"glob": "**/*.py"}}`,
		"Found the scripts.",
	}).withFilesResult(FilesOpResult{Entries: []string{"a.py", "b/c.py"}})

	result, err, _ := runLadder(t, filesLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Found the scripts." {
		t.Errorf("result = %q", result)
	}
	call := io.filesCalls[0]
	if call.Op != "list" || call.Glob != "**/*.py" {
		t.Errorf("files call = %+v", call)
	}
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], "b/c.py") {
		t.Errorf("round-2 prompt should contain the entries, got: %q", io.prompts)
	}
}

func TestLadder_Files_ErrorIsFeedbackNotFailure(t *testing.T) {
	// The op is denied/fails; the loop must surface ToolError and let the LLM
	// produce a final answer — not kill the run.
	io := newLadderIO([]string{
		`{"read": {"path": "secret.txt"}}`,
		"Could not read the file; answering from context.",
	}).withFilesErr(errors.New("denied by user"))

	result, err, _ := runLadder(t, filesLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v (files failure must be non-fatal)", err)
	}
	if result != "Could not read the file; answering from context." {
		t.Errorf("result = %q", result)
	}
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], "ToolError") {
		t.Errorf("round-2 prompt should contain the ToolError feedback, got: %q", io.prompts)
	}
}

func TestLadder_Files_AutoPromptSpecInjected(t *testing.T) {
	io := newLadderIO([]string{
		`{"list": {"glob": "*"}}`,
		"done",
	}).withFilesResult(FilesOpResult{Entries: []string{"x"}})

	_, _, _ = runLadder(t, filesLadder, io, 1)

	if len(io.prompts) < 1 || !strings.Contains(io.prompts[0], "files:") {
		t.Errorf("prompt should contain the auto-injected files tool spec, got: %q", io.prompts)
	}
}
