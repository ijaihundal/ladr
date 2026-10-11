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

// Package engine — JSON extraction and lightweight JSON Schema validation.
//
// Shared between:
//   - if.jsonMatches (gate evaluation)
//   - relikWrite.match (schema-matched writes)
//
// ExtractJSONFromOutput: extracts the first JSON block from an LLM output.
// ValidateAgainstJSONSchema: validates a parsed JSON value against a schema.
package engine

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSONFromOutput extracts the first JSON block from an LLM output string.
//
// Extraction order:
//  1. Strict JSON parse (entire output is JSON)
//  2. Strip ```json ... ``` code fences, then try strict parse
//  3. Find the first balanced { ... } or [ ... ] block
//
// Returns the raw JSON bytes on success.
func ExtractJSONFromOutput(output string) (json.RawMessage, error) {
	output = strings.TrimSpace(output)

	// 1. Try strict parse of entire output.
	if json.Valid([]byte(output)) {
		return json.RawMessage(output), nil
	}

	// 2. Strip markdown code fences.
	if strings.HasPrefix(output, "```") {
		lines := strings.Split(output, "\n")
		var inner []string
		inFence := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				if inFence {
					break // closing fence
				}
				inFence = true
				continue // skip opening fence (and language tag)
			}
			if inFence {
				inner = append(inner, line)
			}
		}
		if len(inner) > 0 {
			extracted := strings.TrimSpace(strings.Join(inner, "\n"))
			if json.Valid([]byte(extracted)) {
				return json.RawMessage(extracted), nil
			}
		}
	}

	// 3. Find first balanced { ... } or [ ... ] block.
	if obj, ok := findBalancedBlock(output, '{', '}'); ok {
		if json.Valid([]byte(obj)) {
			return json.RawMessage(obj), nil
		}
	}
	if arr, ok := findBalancedBlock(output, '[', ']'); ok {
		if json.Valid([]byte(arr)) {
			return json.RawMessage(arr), nil
		}
	}

	return nil, fmt.Errorf("no valid JSON block found in output")
}

// findBalancedBlock finds the first balanced openChar...closeChar block.
func findBalancedBlock(s string, openChar, closeChar byte) (string, bool) {
	start := strings.IndexByte(s, openChar)
	if start < 0 {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if ch == openChar {
			depth++
		} else if ch == closeChar {
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}

	return "", false
}

// ---------------------------------------------------------------------------
// Lightweight JSON Schema validation
// ---------------------------------------------------------------------------

// ValidateAgainstJSONSchema validates a parsed JSON value against a JSON Schema.
//
// Supported keywords:
//   - type: "object" | "array" | "string" | "number" | "integer" | "boolean"
//   - required: [field names]
//   - properties: { field: schema }
//   - additionalProperties: false (rejects unknown fields when false)
//   - enum: [values]
//   - minimum, maximum (numbers)
//   - minLength, maxLength (strings)
//   - minItems, maxItems (arrays)
//   - items: schema (for arrays — all items must match)
//
// This is NOT a full JSON Schema implementation — it covers the practical subset
// used in LADR ladder configs. Missing keywords are silently ignored.
func ValidateAgainstJSONSchema(value any, schema any) error {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return nil // non-object schema = no validation
	}

	// type check
	if typeStr, hasType := schemaMap["type"].(string); hasType {
		if err := checkJSONType(value, typeStr); err != nil {
			return err
		}
	}

	// enum check
	if enumVal, hasEnum := schemaMap["enum"]; hasEnum {
		if arr, ok := enumVal.([]any); ok {
			matched := false
			for _, v := range arr {
				if jsonEqual(value, v) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("value %v not in enum %v", value, arr)
			}
		}
	}

	// Object-specific checks
	obj, isObj := value.(map[string]any)
	if isObj {
		// required
		if reqArr, hasReq := schemaMap["required"].([]any); hasReq {
			for _, req := range reqArr {
				if reqStr, ok := req.(string); ok {
					if _, present := obj[reqStr]; !present {
						return fmt.Errorf("missing required field %q", reqStr)
					}
				}
			}
		}

		// properties
		if props, hasProps := schemaMap["properties"].(map[string]any); hasProps {
			for fieldName, fieldVal := range obj {
				if propSchema, hasProp := props[fieldName].(map[string]any); hasProp {
					if err := ValidateAgainstJSONSchema(fieldVal, propSchema); err != nil {
						return fmt.Errorf("field %q: %w", fieldName, err)
					}
				} else {
					// additionalProperties check
					addlProps, hasAddl := schemaMap["additionalProperties"]
					if hasAddl {
						if allowed, ok := addlProps.(bool); ok && !allowed {
							return fmt.Errorf("additional property %q not allowed (additionalProperties: false)", fieldName)
						}
					}
				}
			}
		}

		// additionalProperties: false (when no properties declared)
		if _, hasProps := schemaMap["properties"]; !hasProps {
			if addlProps, hasAddl := schemaMap["additionalProperties"]; hasAddl {
				if allowed, ok := addlProps.(bool); ok && !allowed && len(obj) > 0 {
					return fmt.Errorf("additionalProperties: false but object has fields")
				}
			}
		}
	}

	// Number-specific checks
	if num, isNum := value.(float64); isNum {
		if min, ok := schemaMap["minimum"].(float64); ok {
			if num < min {
				return fmt.Errorf("value %v < minimum %v", num, min)
			}
		}
		if max, ok := schemaMap["maximum"].(float64); ok {
			if num > max {
				return fmt.Errorf("value %v > maximum %v", num, max)
			}
		}
	}

	// String-specific checks
	if str, isStr := value.(string); isStr {
		if minLen, ok := schemaMap["minLength"].(float64); ok {
			if len(str) < int(minLen) {
				return fmt.Errorf("string length %d < minLength %v", len(str), minLen)
			}
		}
		if maxLen, ok := schemaMap["maxLength"].(float64); ok {
			if len(str) > int(maxLen) {
				return fmt.Errorf("string length %d > maxLength %v", len(str), maxLen)
			}
		}
	}

	// Array-specific checks
	if arr, isArr := value.([]any); isArr {
		if minItems, ok := schemaMap["minItems"].(float64); ok {
			if len(arr) < int(minItems) {
				return fmt.Errorf("array length %d < minItems %v", len(arr), minItems)
			}
		}
		if maxItems, ok := schemaMap["maxItems"].(float64); ok {
			if len(arr) > int(maxItems) {
				return fmt.Errorf("array length %d > maxItems %v", len(arr), maxItems)
			}
		}
		if itemsSchema, hasItems := schemaMap["items"].(map[string]any); hasItems {
			for i, item := range arr {
				if err := ValidateAgainstJSONSchema(item, itemsSchema); err != nil {
					return fmt.Errorf("array[%d]: %w", i, err)
				}
			}
		}
	}

	return nil
}

// checkJSONType verifies that a value matches a JSON Schema type string.
func checkJSONType(value any, typeStr string) error {
	switch typeStr {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("expected object, got %T", value)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("expected array, got %T", value)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("expected number, got %T", value)
		}
	case "integer":
		if f, ok := value.(float64); ok {
			if f != float64(int(f)) {
				return fmt.Errorf("expected integer, got non-integer number %v", f)
			}
		} else {
			return fmt.Errorf("expected integer, got %T", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	default:
		// Unknown type — skip
	}
	return nil
}

// jsonEqual does a deep equality check on parsed JSON values.
func jsonEqual(a, b any) bool {
	return jsonDeepEqual(a, b)
}

func jsonDeepEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !jsonDeepEqual(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i, v := range av {
			if !jsonDeepEqual(v, bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
