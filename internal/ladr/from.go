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

// Package ladr — FromSpec: the unified source vocabulary.
//
// FromSpec is used by:
//   - relikWrite (gated writes via if/then)
//   - dynamic step loading (dynamicFrom)
//   - retrieve result injection
//
// One way to specify a source, everywhere.
package ladr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FromKind discriminates the FromSpec union.
type FromKind int

const (
	// FromKeywordPrevious = bare "previous" — all survivors of the immediately preceding step.
	FromKeywordPrevious FromKind = iota
	// FromKeywordOutput = bare "output" — the current step's own output.
	FromKeywordOutput
	// FromInputKey = a bare string starting with "input." — looked up in the runtime
	// inputs map (e.g. "input.context", "input.systemTime"). Unifies TextField-style
	// input access into the FromSpec vocabulary so fallback: can reference any input.
	FromInputKey
	// FromLiteral = a bare string that is not a keyword and not an input key.
	FromLiteral
	// FromStepRef = { stepId: ..., nodeRef?: ..., node?: ... }.
	FromStepRef
	// FromRelikRef = { relik: ..., folder: ..., file?: ..., metatag?: ... }.
	FromRelikRef
	// FromMailboxRef = { mailbox: true } — drains the ladder's pending
	// messages (consume) and yields the formatted envelopes.
	FromMailboxRef
)

// StepFrom references a previous step's node set.
type StepFrom struct {
	StepID  string  `json:"stepId"`
	NodeRef NodeRef `json:"nodeRef,omitempty"`
	Node    *int    `json:"node,omitempty"`
}

// FromSpec is a union of source specifications.
//
// JSON forms:
//
//	from: previous                      → FromKeywordPrevious
//	from: output                        → FromKeywordOutput
//	from: "input.context"               → FromInputKey  (any string starting with "input.")
//	from: "literal text"                → FromLiteral   (any other string)
//	from: { stepId: "generate" }        → FromStepRef
//	from: { stepId: "x", nodeRef: ... } → FromStepRef
//	from: { relik: ..., folder: ... }   → FromRelikRef
type FromSpec struct {
	Kind     FromKind
	Literal  string      // for FromLiteral
	InputKey string      // for FromInputKey (e.g. "input.context")
	Step     StepFrom    // for FromStepRef
	Relik    RelikTarget // for FromRelikRef
	Mailbox  bool        // for FromMailboxRef ({ mailbox: true })
}

// Known bare-string keywords.
var fromKeywords = map[string]FromKind{
	"previous": FromKeywordPrevious,
	"output":   FromKeywordOutput,
}

// MarshalJSON encodes FromSpec back to its JSON form.
func (f FromSpec) MarshalJSON() ([]byte, error) {
	switch f.Kind {
	case FromKeywordPrevious:
		return json.Marshal("previous")
	case FromKeywordOutput:
		return json.Marshal("output")
	case FromInputKey:
		return json.Marshal(f.InputKey)
	case FromLiteral:
		return json.Marshal(f.Literal)
	case FromStepRef:
		return json.Marshal(f.Step)
	case FromRelikRef:
		return json.Marshal(f.Relik)
	case FromMailboxRef:
		return json.Marshal(map[string]bool{"mailbox": true})
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON dispatches by JSON type and key presence.
func (f *FromSpec) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if kind, ok := fromKeywords[s]; ok {
			f.Kind = kind
			return nil
		}
		// Bare strings starting with "input." are input-key lookups.
		if strings.HasPrefix(s, "input.") {
			f.Kind = FromInputKey
			f.InputKey = s
			return nil
		}
		f.Kind = FromLiteral
		f.Literal = s
		return nil
	}

	// Try object.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("from: expected string or object, got %s", string(data))
	}

	// Check for stepId → step reference.
	if _, hasStepID := obj["stepId"]; hasStepID {
		f.Kind = FromStepRef
		return json.Unmarshal(data, &f.Step)
	}

	// Check for relik → relik reference.
	if _, hasRelik := obj["relik"]; hasRelik {
		f.Kind = FromRelikRef
		return json.Unmarshal(data, &f.Relik)
	}

	// Check for mailbox → drain the ladder's pending messages.
	if _, hasMailbox := obj["mailbox"]; hasMailbox {
		f.Kind = FromMailboxRef
		f.Mailbox = true
		return nil
	}

	return fmt.Errorf("from: object must have \"stepId\", \"relik\", or \"mailbox\" key")
}
