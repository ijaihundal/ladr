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

// Package ladr — schema-matched relik writes.
package ladr

import "encoding/json"

// RelikWrite is the step-level schema-matched write operation.
//
// After the step runs, the engine inspects the LLM's output, extracts the first
// JSON block, validates against Match, and if valid, parses and writes the
// result to the declared target. If no match, no-op (not error).
//
// YAML:
//
//	relikWrite:
//	  to: { relik: "Memory", folder: facts, file: core.md }
//	  field: fact           # extract one field from JSON
//	  on: append            # append | overwrite | mergeByKey
//	  key: id               # for mergeByKey: field name INSIDE the JSON objects
//	  atomic: true          # for array writes: all-or-nothing
//	  match: { type: object, required: [fact, confidence], ... }
type RelikWrite struct {
	To     RelikTarget     `json:"to"`
	Field  string          `json:"field,omitempty"`  // extract one field before writing
	On     WritePolicy     `json:"on,omitempty"`     // default: append
	Key    string          `json:"key,omitempty"`    // only for mergeByKey
	Atomic bool            `json:"atomic,omitempty"` // for array writes
	Match  json.RawMessage `json:"match"`            // JSON Schema for validation
}

// HasMatch returns true if a match schema is declared.
func (w RelikWrite) HasMatch() bool { return len(w.Match) > 0 }

// IsMergeByKey returns true if this write uses mergeByKey policy.
func (w RelikWrite) IsMergeByKey() bool { return w.On == WriteMergeByKey }
