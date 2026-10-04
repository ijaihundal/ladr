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

// Package engine — shared types for the adaptive tool-call dispatch.
//
// The bounded multi-turn loop lives in adaptive.go (ExecuteAdaptiveLoop). This
// file holds the toolCall struct and parseToolCall function that the loop uses
// to decide whether LLM output is a tool call (match) or a final answer (no
// match), and to extract the call's dispatch-relevant fields.
package engine

import (
	"encoding/json"
)

// toolCall is the parsed LLM output when it matches the requestMatch schema.
// Fields are optional; dispatch decides which tool fires based on which is
// populated: Code/Action → codeExecution (a code run or a terminal action),
// Request → relikRead, Edit → relikEdit, Read/Write/List → files.
type toolCall struct {
	Code       string          `json:"code,omitempty"`
	Language   string          `json:"language,omitempty"`
	Terminal   string          `json:"terminal,omitempty"`   // codeExecution: named terminal ("" → the ladder's default)
	Background bool            `json:"background,omitempty"` // codeExecution: long-running command, immediate receipt
	Action     string          `json:"action,omitempty"`     // codeExecution terminal action: peek|kill|status
	Request    json.RawMessage `json:"request,omitempty"`
	Edit       json.RawMessage `json:"edit,omitempty"`        // relikEdit: {file, ops:[...]} or flat single-op
	Read       json.RawMessage `json:"read,omitempty"`        // files: {path}
	Write      json.RawMessage `json:"write,omitempty"`       // files: {path, content, append?}
	List       json.RawMessage `json:"list,omitempty"`        // files: {path?, glob}
	ConfigEdit json.RawMessage `json:"configEdit,omitempty"`  // configEdit: {ops:[...], message}
}

// parseToolCall extracts JSON from the output and validates it against the
// requestMatch schema. Returns (matched, call, err). matched=false means the
// output is a final answer, not a tool call.
func parseToolCall(output string, requestMatch json.RawMessage) (bool, toolCall, error) {
	rawJSON, err := ExtractJSONFromOutput(output)
	if err != nil {
		return false, toolCall{}, nil
	}
	var value any
	if err := json.Unmarshal(rawJSON, &value); err != nil {
		return false, toolCall{}, nil
	}
	var schema any
	if err := json.Unmarshal(requestMatch, &schema); err != nil {
		return false, toolCall{}, err
	}
	if err := ValidateAgainstJSONSchema(value, schema); err != nil {
		return false, toolCall{}, nil
	}
	var call toolCall
	if err := json.Unmarshal(rawJSON, &call); err != nil {
		return false, toolCall{}, nil
	}
	return true, call, nil
}
