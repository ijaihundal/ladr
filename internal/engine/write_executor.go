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

// Package engine — schema-matched write executor.
//
// Bridges step outputs, the JSON extraction + validation engine, and the relik store.
// Handles both schema-matched writes (relikWrite.match) and mergeByKey.
package engine

import (
	"encoding/json"
	"fmt"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// WriteExecutor applies schema-matched writes to the relik store.
type WriteExecutor struct {
	store relik.Store
}

// NewWriteExecutor creates a WriteExecutor backed by the given store.
func NewWriteExecutor(store relik.Store) *WriteExecutor {
	return &WriteExecutor{store: store}
}

// ApplyRelikWrite extracts JSON from output, validates against the schema,
// and writes to the relik if valid. Returns nil on no-match (no-op, not error).
//
// Steps:
//  1. Extract first JSON block from output (ExtractJSONFromOutput).
//  2. Parse and validate against sw.Match (ValidateAgainstJSONSchema).
//  3. If field: specified, extract that field's value.
//  4. Write to target (file contents or metatag) using sw.On policy.
func (we *WriteExecutor) ApplyRelikWrite(output string, sw ladr.RelikWrite) error {
	if !sw.HasMatch() {
		return fmt.Errorf("relikWrite: no match schema declared")
	}

	// 1. Extract JSON.
	rawJSON, err := ExtractJSONFromOutput(output)
	if err != nil {
		// No-match is no-op, not error.
		return nil
	}

	var value any
	if err := json.Unmarshal(rawJSON, &value); err != nil {
		// Malformed JSON — no-op.
		return nil
	}

	// 2. Validate against schema.
	var schema any
	if err := json.Unmarshal(sw.Match, &schema); err != nil {
		return fmt.Errorf("relikWrite: invalid match schema: %w", err)
	}

	if err := ValidateAgainstJSONSchema(value, schema); err != nil {
		// Validation failed — no-op (opportunistic write).
		return nil
	}

	// 3. Extract field if specified.
	contentToWrite := value
	if sw.Field != "" {
		extracted, err := extractField(value, sw.Field)
		if err != nil {
			// Field not found — no-op.
			return nil
		}
		contentToWrite = extracted
	}

	// 4. Convert to string for writing.
	contentStr, err := valueToString(contentToWrite)
	if err != nil {
		return err
	}

	// 5. Write to target.
	target := sw.To
	if target.HasMetatag() {
		// Writing a metatag value. sw.On drives the policy (empty → overwrite,
		// matching the historical default before the policy param existed).
		return we.store.WriteMetatag(target.Relik, target.Folder, target.File, target.Metatag, contentToWrite, sw.On)
	}

	// Writing file contents.
	if sw.IsMergeByKey() {
		return we.applyMergeByKey(target, contentToWrite, sw.Key)
	}

	return we.store.WriteFile(target.Relik, target.Folder, target.File, contentStr, sw.On)
}

// applyMergeByKey loads existing JSON content, merges by key, writes back.
func (we *WriteExecutor) applyMergeByKey(target ladr.RelikTarget, newItem any, key string) error {
	// Load existing content.
	existing, err := we.store.ReadFile(target.Relik, target.Folder, target.File)
	if err != nil {
		existing = ""
	}

	// Parse existing as JSON array (or start fresh).
	var arr []any
	if existing != "" {
		if err := json.Unmarshal([]byte(existing), &arr); err != nil {
			// Existing content isn't a JSON array — start fresh with new item.
			arr = nil
		}
	}

	// Normalize new item to an array.
	newItems, ok := newItem.([]any)
	if !ok {
		newItems = []any{newItem}
	}

	// Merge by key.
	for _, ni := range newItems {
		niObj, ok := ni.(map[string]any)
		if !ok {
			arr = append(arr, ni)
			continue
		}
		niKey, ok := niObj[key].(string)
		if !ok {
			// Item doesn't have the key field — just append.
			arr = append(arr, ni)
			continue
		}

		// Find and replace existing item with matching key.
		found := false
		for i, existing := range arr {
			existingObj, ok := existing.(map[string]any)
			if !ok {
				continue
			}
			existingKey, ok := existingObj[key].(string)
			if ok && existingKey == niKey {
				arr[i] = ni // replace
				found = true
				break
			}
		}
		if !found {
			arr = append(arr, ni)
		}
	}

	// Write merged result.
	merged, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	return we.store.WriteFile(target.Relik, target.Folder, target.File, string(merged), ladr.WriteOverwrite)
}

// --- helpers ---

func extractField(value any, field string) (any, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("cannot extract field %q from non-object", field)
	}
	val, exists := obj[field]
	if !exists {
		return nil, fmt.Errorf("field %q not found", field)
	}
	return val, nil
}

func valueToString(v any) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case nil:
		return "", nil
	default:
		// For non-string types, marshal as JSON.
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}
