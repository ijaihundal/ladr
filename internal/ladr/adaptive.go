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

// Package ladr — the four flat step-level adaptive keys.
//
// codeExecution:, relikRead:, relikEdit:, and files: are flat step-level keys
// that each enable a bounded multi-turn loop where the LLM can call a tool (run
// code, read reliks, edit reliks, work with local files), receive results, and
// continue reasoning — until it produces a final answer or hits the round
// limit.
//
// When a step declares more than one adaptive key, they share ONE bounded loop
// with ONE maxRounds and ONE requestMatch. The engine dispatches each turn by
// inspecting which property of the LLM's JSON output is populated:
//
//	code     → codeExecution
//	request  → relikRead
//	edit     → relikEdit
//	read / write / list → files
//
// This is the function-calling pattern done LADR's way: text + JSON-Schema
// matching, provider-agnostic, fully visible in the trace.
package ladr

import "encoding/json"

// CodeExecutionConfig is the codeExecution: step key. It enables a bounded loop
// where the LLM can emit code to run in a sandboxed persistent container on the
// executing host. The runtime process is kept alive across the step's turns so
// REPL state (imports, variables) persists within the loop.
//
// YAML:
//
//	codeExecution:
//	  maxRounds: 6
//	  runtime: python
//	  as: ComputedResult
//	  requestMatch: { type: object, properties: { code: { type: string } } }
//	  autoPrompt: true
//	  prompt: "..."            # override the auto-generated usage text
type CodeExecutionConfig struct {
	// MaxRounds is the hard cap on adaptive-loop turns; guarantees termination
	// and bounds cost (each turn is one LLM call). Must be > 0. When multiple
	// adaptive keys coexist on a step, this must agree across all of them.
	MaxRounds int `json:"maxRounds"`

	// RequestMatch is the JSON Schema the engine uses to decide whether the
	// LLM's output is a tool call (match) or the final answer (no match). It is
	// also used to route the call by property name (code/request/edit). When
	// multiple adaptive keys coexist, this must be the same across all of them.
	RequestMatch json.RawMessage `json:"requestMatch"`

	// AutoPrompt, when non-nil, controls whether the engine auto-injects a
	// default tool-spec block into the prompt. Nil or true → inject defaults;
	// false → the author writes their own usage instructions in the step's
	// systemPrompt.
	AutoPrompt *bool `json:"autoPrompt,omitempty"`

	// Runtime selects the language container: "python", "node", or "bash".
	// Empty defaults to "python".
	Runtime string `json:"runtime,omitempty"`

	// As is the field name the computed result is injected under on subsequent
	// turns. Required.
	As string `json:"as,omitempty"`

	// Prompt, when set, replaces the engine's default usage instructions for
	// this tool.
	Prompt string `json:"prompt,omitempty"`
}

// RelikReadConfig is the relikRead: step key (adaptive multi-turn read). The LLM
// sees a folder index, requests specific files/subfolders/metatags/globs, and
// the engine injects their contents as the field named by As on subsequent turns.
//
// This is the adaptive counterpart to the declarative relikRead field type.
// Same capability, two surfaces: declarative (config-time, one-shot) and adaptive
// (runtime, multi-turn, LLM-controlled).
//
// YAML:
//
//	relikRead:
//	  maxRounds: 5
//	  from: { relik: Knowledge, folder: docs }
//	  as: RetrievedDocuments
//	  requestMatch: { ... }
type RelikReadConfig struct {
	// MaxRounds is the hard cap on adaptive-loop turns. Must be > 0. When
	// multiple adaptive keys coexist on a step, must agree across all of them.
	MaxRounds int `json:"maxRounds"`

	// RequestMatch is the shared JSON Schema for tool-call dispatch. See
	// CodeExecutionConfig.RequestMatch.
	RequestMatch json.RawMessage `json:"requestMatch"`

	// AutoPrompt controls auto-injection of the tool-spec prompt. See
	// CodeExecutionConfig.AutoPrompt.
	AutoPrompt *bool `json:"autoPrompt,omitempty"`

	// From identifies the relik folder to read from (a READ source).
	From RelikTarget `json:"from"`

	// As is the field name retrieved documents are injected under on subsequent
	// turns. Required.
	As string `json:"as"`

	// Prompt, when set, replaces the engine's default usage instructions.
	Prompt string `json:"prompt,omitempty"`
}

// RelikEditConfig is the relikEdit: step key (adaptive multi-turn surgical edit).
// The LLM emits edit operations against files in the declared folder; each turn
// it receives an EditReceipt showing what applied, what didn't and why, and a
// numbered view of the changed file so the next edit targets current state.
//
// Every file carries a version and content hash so diffs are materializable;
// last-writer-wins stays — version/hash is observability, not locking.
//
// YAML:
//
//	relikEdit:
//	  maxRounds: 4
//	  to: { relik: Memory, folder: drafts }
//	  as: EditReceipt
//	  diff: false
//	  requestMatch: { ... }
type RelikEditConfig struct {
	// MaxRounds is the hard cap on adaptive-loop turns. Must be > 0. When
	// multiple adaptive keys coexist on a step, must agree across all of them.
	MaxRounds int `json:"maxRounds"`

	// RequestMatch is the shared JSON Schema for tool-call dispatch. See
	// CodeExecutionConfig.RequestMatch.
	RequestMatch json.RawMessage `json:"requestMatch"`

	// AutoPrompt controls auto-injection of the tool-spec prompt. See
	// CodeExecutionConfig.AutoPrompt.
	AutoPrompt *bool `json:"autoPrompt,omitempty"`

	// To scopes the tool to a single relik folder. The LLM names the target
	// file inside each edit op (bounded + validated).
	To RelikTarget `json:"to"`

	// As is the field name the EditReceipt is injected under on subsequent
	// turns. Required.
	As string `json:"as"`

	// Diff, when true, includes a compact unified diff in the receipt for each
	// applied op. Default (false) is token-lean: per-op status + changed line
	// numbers only.
	Diff bool `json:"diff,omitempty"`

	// Prompt, when set, replaces the engine's default usage instructions.
	Prompt string `json:"prompt,omitempty"`
}

// ConfigEditConfig is the configEdit: step key — the fifth adaptive tool.
// The LLM edits ITS OWN LADDER'S STORED CONFIG with relikEdit's anchor/line
// op vocabulary. Validate-before-commit (a broken skeleton cannot commit;
// errors return as tool feedback), immutable version history with
// self-authored commit messages, and protected keys: executionBudget /
// allowedTargets edits queue as pending user approval instead of landing.
//
// YAML:
//
//	configEdit:
//	  maxRounds: 4
//	  as: ConfigEditReceipt
//	  requestMatch: { ... }
type ConfigEditConfig struct {
	MaxRounds     int             `json:"maxRounds"`
	RequestMatch  json.RawMessage `json:"requestMatch"`
	AutoPrompt    *bool           `json:"autoPrompt,omitempty"`
	As            string          `json:"as"`
	Diff          bool            `json:"diff,omitempty"`
	Prompt        string          `json:"prompt,omitempty"`
}

// FilesConfig is the files: step key (adaptive multi-turn local-file access).
// The LLM can read, write, and list files relative to a scope it does not
// choose: on a hosted run the device client resolves paths against the
// user-granted folder for this ladder (per-ladder consent, enforced
// device-side); on a local ladr run paths resolve against the engine's
// per-session workspace root. The grammar carries no scope fields — the same
// ladder reads "path/to/file" wherever it runs.
//
// YAML:
//
//	files:
//	  maxRounds: 8
//	  as: FileResult
//	  requestMatch: { ... }
type FilesConfig struct {
	// MaxRounds is the hard cap on adaptive-loop turns. Must be > 0. When
	// multiple adaptive keys coexist on a step, must agree across all of them.
	MaxRounds int `json:"maxRounds"`

	// RequestMatch is the shared JSON Schema for tool-call dispatch. See
	// CodeExecutionConfig.RequestMatch.
	RequestMatch json.RawMessage `json:"requestMatch"`

	// AutoPrompt controls auto-injection of the tool-spec prompt. See
	// CodeExecutionConfig.AutoPrompt.
	AutoPrompt *bool `json:"autoPrompt,omitempty"`

	// As is the field name file results are injected under on subsequent turns.
	// Required.
	As string `json:"as,omitempty"`

	// Prompt, when set, replaces the engine's default usage instructions.
	Prompt string `json:"prompt,omitempty"`
}

// HasAdaptiveKeys reports whether the step declares any adaptive key.
func (b *BaseStep) HasAdaptiveKeys() bool {
	return b.CodeExecution != nil || b.RelikRead != nil || b.RelikEdit != nil || b.Files != nil || b.ConfigEdit != nil
}
