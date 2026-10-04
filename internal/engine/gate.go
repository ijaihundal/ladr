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

// Package engine — typed gate evaluation for LADR if/then/else conditions.
//
// The gate evaluator checks whether a Condition passes against a step output.
// Used per-node (for continueIf/if pruning) and at step level (for then/else actions).
package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ladr/ladr/internal/ladr"
)

// GateResult is the outcome of evaluating a condition against an output.
type GateResult struct {
	Passed bool
	Reason string // why it passed or failed (for observability)
}

// EvaluateGate checks whether the given condition passes against the output.
func EvaluateGate(output string, cond ladr.Condition) GateResult {
	output = strings.TrimSpace(output)
	switch cond.Kind {
	case ladr.CondEquals:
		return evaluateEquals(output, cond.Equals)

	case ladr.CondIntegerEquals:
		return evaluateIntegerEquals(output, cond.IntegerEquals)

	case ladr.CondIntegerRange:
		return evaluateIntegerRange(output, cond.IntegerRange)

	case ladr.CondNumberEquals:
		return evaluateNumberEquals(output, cond.NumberEquals)

	case ladr.CondNumberRange:
		return evaluateNumberRange(output, cond.NumberRange)

	case ladr.CondContains:
		return evaluateContains(output, cond.Contains)

	case ladr.CondIn:
		return evaluateIn(output, cond.In)

	case ladr.CondJSONMatches:
		return evaluateJSONMatches(output, cond.JSONMatches)

	case ladr.CondMatches:
		return evaluateMatches(output, cond.Matches)

	case ladr.CondAnd:
		return evaluateAnd(output, cond.And)

	case ladr.CondOr:
		return evaluateOr(output, cond.Or)

	case ladr.CondNot:
		return evaluateNot(output, cond.Not)

	default:
		return GateResult{Passed: false, Reason: "unknown condition type"}
	}
}

// --- individual evaluators ---

func evaluateEquals(output, expected string) GateResult {
	if output == expected {
		return GateResult{Passed: true, Reason: "exact match"}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("output %q != expected %q", output, expected)}
}

func evaluateIntegerEquals(output string, expected int) GateResult {
	n, ok := parseIntegerTolerant(output)
	if !ok {
		return GateResult{Passed: false, Reason: fmt.Sprintf("output %q is not a valid integer", output)}
	}
	if n == expected {
		return GateResult{Passed: true, Reason: "integer match"}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("integer %d != expected %d", n, expected)}
}

func evaluateIntegerRange(output string, rng [2]int) GateResult {
	n, ok := parseIntegerTolerant(output)
	if !ok {
		return GateResult{Passed: false, Reason: fmt.Sprintf("output %q is not a valid integer", output)}
	}
	if n >= rng[0] && n <= rng[1] {
		return GateResult{Passed: true, Reason: fmt.Sprintf("integer %d in range [%d, %d]", n, rng[0], rng[1])}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("integer %d outside range [%d, %d]", n, rng[0], rng[1])}
}

func evaluateNumberEquals(output string, expected float64) GateResult {
	n, ok := parseFloatTolerant(output)
	if !ok {
		return GateResult{Passed: false, Reason: fmt.Sprintf("output %q is not a valid number", output)}
	}
	const epsilon = 1e-9
	if abs(n-expected) < epsilon {
		return GateResult{Passed: true, Reason: "number match"}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("number %v != expected %v", n, expected)}
}

func evaluateNumberRange(output string, rng [2]float64) GateResult {
	n, ok := parseFloatTolerant(output)
	if !ok {
		return GateResult{Passed: false, Reason: fmt.Sprintf("output %q is not a valid number", output)}
	}
	if n >= rng[0] && n <= rng[1] {
		return GateResult{Passed: true, Reason: fmt.Sprintf("number %v in range [%v, %v]", n, rng[0], rng[1])}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("number %v outside range [%v, %v]", n, rng[0], rng[1])}
}

func evaluateContains(output, substring string) GateResult {
	if strings.Contains(output, substring) {
		return GateResult{Passed: true, Reason: fmt.Sprintf("output contains %q", substring)}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("output does not contain %q", substring)}
}

func evaluateIn(output string, values []string) GateResult {
	for _, v := range values {
		if output == strings.TrimSpace(v) {
			return GateResult{Passed: true, Reason: fmt.Sprintf("output matches %q in set", v)}
		}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("output %q not in set %v", output, values)}
}

func evaluateJSONMatches(output string, schemaJSON json.RawMessage) GateResult {
	extracted, err := ExtractJSONFromOutput(output)
	if err != nil {
		return GateResult{Passed: false, Reason: fmt.Sprintf("no JSON found in output: %v", err)}
	}

	var value any
	if err := json.Unmarshal(extracted, &value); err != nil {
		return GateResult{Passed: false, Reason: fmt.Sprintf("extracted JSON is invalid: %v", err)}
	}

	var schema any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return GateResult{Passed: false, Reason: fmt.Sprintf("schema is invalid JSON: %v", err)}
	}

	if err := ValidateAgainstJSONSchema(value, schema); err != nil {
		return GateResult{Passed: false, Reason: fmt.Sprintf("JSON schema validation failed: %v", err)}
	}
	return GateResult{Passed: true, Reason: "JSON matches schema"}
}

func evaluateMatches(output, pattern string) GateResult {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return GateResult{Passed: false, Reason: fmt.Sprintf("invalid regex %q: %v", pattern, err)}
	}
	if re.MatchString(output) {
		return GateResult{Passed: true, Reason: fmt.Sprintf("output matches regex %q", pattern)}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("output does not match regex %q", pattern)}
}

func evaluateAnd(output string, conds []ladr.Condition) GateResult {
	for _, c := range conds {
		r := EvaluateGate(output, c)
		if !r.Passed {
			return GateResult{Passed: false, Reason: fmt.Sprintf("and: sub-condition failed — %s", r.Reason)}
		}
	}
	return GateResult{Passed: true, Reason: fmt.Sprintf("and: all %d sub-conditions passed", len(conds))}
}

func evaluateOr(output string, conds []ladr.Condition) GateResult {
	for _, c := range conds {
		r := EvaluateGate(output, c)
		if r.Passed {
			return GateResult{Passed: true, Reason: fmt.Sprintf("or: sub-condition passed — %s", r.Reason)}
		}
	}
	return GateResult{Passed: false, Reason: fmt.Sprintf("or: all %d sub-conditions failed", len(conds))}
}

func evaluateNot(output string, cond *ladr.Condition) GateResult {
	if cond == nil {
		return GateResult{Passed: false, Reason: "not: missing sub-condition"}
	}
	r := EvaluateGate(output, *cond)
	if r.Passed {
		return GateResult{Passed: false, Reason: fmt.Sprintf("not: sub-condition passed, negated — %s", r.Reason)}
	}
	return GateResult{Passed: true, Reason: fmt.Sprintf("not: sub-condition failed, negated — %s", r.Reason)}
}

// --- parsing helpers ---

// parseIntegerTolerant parses an integer from a string, tolerating trailing .0.
func parseIntegerTolerant(s string) (int, bool) {
	s = strings.TrimSpace(s)
	// Try direct int parse
	n, err := strconv.Atoi(s)
	if err == nil {
		return n, true
	}
	// Try float and check if it's a whole number
	f, err := strconv.ParseFloat(s, 64)
	if err == nil && f == float64(int(f)) {
		return int(f), true
	}
	return 0, false
}

// parseFloatTolerant parses a float from a string.
func parseFloatTolerant(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return f, true
	}
	return 0, false
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// JSONRawMessage type alias for convenience.
type JSONRawMessage = json.RawMessage
