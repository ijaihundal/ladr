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

// Package ladr — control flow types: conditions, actions, if-blocks, jumps.
package ladr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Condition — the 8 typed condition keys
// ---------------------------------------------------------------------------

// ConditionKind discriminates the Condition union.
type ConditionKind int

const (
	CondEquals        ConditionKind = iota // equals: string
	CondIntegerEquals                      // integerEquals: int
	CondIntegerRange                       // integerRange: [a, b]
	CondNumberEquals                       // numberEquals: float64
	CondNumberRange                        // numberRange: [a, b]
	CondContains                           // contains: string (substring)
	CondIn                                 // in: []string
	CondJSONMatches                        // jsonMatches: JSON Schema
	CondMatches                            // matches: string (RE2 regex)
	CondAnd                                // and: []Condition (all must pass)
	CondOr                                 // or: []Condition (any must pass)
	CondNot                                // not: Condition (negation)
)

// Condition is a typed predicate evaluated against a step output.
//
// Leaf conditions (equals, integerEquals, etc.) test the output directly.
// Compound conditions (and, or, not) compose other conditions recursively,
// making Condition a tree.
type Condition struct {
	Kind ConditionKind

	Equals        string          // exact string match
	IntegerEquals int             // parsed as int, tolerates trailing .0
	IntegerRange  [2]int          // inclusive [a, b]
	NumberEquals  float64         // parsed as float, epsilon tolerance
	NumberRange   [2]float64      // inclusive [a, b]
	Contains      string          // substring match
	In            []string        // output must be one of these
	JSONMatches   json.RawMessage // JSON Schema; first JSON block extracted from output, validated
	Matches       string          // RE2 regex — search (partial match, not full-match)

	// Compound conditions — Condition becomes a recursive tree.
	And []Condition // CondAnd:  every sub-condition must pass
	Or  []Condition // CondOr:   at least one sub-condition must pass
	Not *Condition  // CondNot:  negates the single sub-condition
}

// conditionKeyName maps ConditionKind to the JSON key name.
var conditionKeyName = map[ConditionKind]string{
	CondEquals:        "equals",
	CondIntegerEquals: "integerEquals",
	CondIntegerRange:  "integerRange",
	CondNumberEquals:  "numberEquals",
	CondNumberRange:   "numberRange",
	CondContains:      "contains",
	CondIn:            "in",
	CondJSONMatches:   "jsonMatches",
	CondMatches:       "matches",
	CondAnd:           "and",
	CondOr:            "or",
	CondNot:           "not",
}

// keyNameToCondition reverses the map.
var keyNameToCondition = map[string]ConditionKind{}

func init() {
	for k, v := range conditionKeyName {
		keyNameToCondition[v] = k
	}
}

// MarshalJSON encodes the condition as a single-key object.
func (c Condition) MarshalJSON() ([]byte, error) {
	key := conditionKeyName[c.Kind]
	m := map[string]any{}
	switch c.Kind {
	case CondEquals:
		m[key] = c.Equals
	case CondIntegerEquals:
		m[key] = c.IntegerEquals
	case CondIntegerRange:
		m[key] = []int{c.IntegerRange[0], c.IntegerRange[1]}
	case CondNumberEquals:
		m[key] = c.NumberEquals
	case CondNumberRange:
		m[key] = []float64{c.NumberRange[0], c.NumberRange[1]}
	case CondContains:
		m[key] = c.Contains
	case CondIn:
		m[key] = c.In
	case CondJSONMatches:
		if len(c.JSONMatches) > 0 {
			var schema any
			if err := json.Unmarshal(c.JSONMatches, &schema); err == nil {
				m[key] = schema
			}
		}
	case CondMatches:
		m[key] = c.Matches
	case CondAnd:
		m[key] = c.And
	case CondOr:
		m[key] = c.Or
	case CondNot:
		if c.Not != nil {
			m[key] = c.Not
		}
	}
	return json.Marshal(m)
}

// UnmarshalJSON expects a single-key object where the key names the condition.
func (c *Condition) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("condition: expected object, got %s", string(data))
	}

	var foundKey string
	var foundKind ConditionKind
	var found bool

	for key := range obj {
		if key == "then" || key == "else" {
			continue // These belong to If, not Condition
		}
		kind, ok := keyNameToCondition[key]
		if !ok {
			return fmt.Errorf("condition: unknown key %q", key)
		}
		if found {
			return fmt.Errorf("condition: multiple condition keys found (at least %q and %q); only one allowed", foundKey, key)
		}
		foundKey = key
		foundKind = kind
		found = true
	}

	if !found {
		return fmt.Errorf("condition: no condition key found in object")
	}

	c.Kind = foundKind
	raw := obj[foundKey]

	switch foundKind {
	case CondEquals:
		return json.Unmarshal(raw, &c.Equals)
	case CondIntegerEquals:
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return fmt.Errorf("integerEquals: expected number, got %s", string(raw))
		}
		i, err := n.Int64()
		if err != nil {
			f, ferr := n.Float64()
			if ferr != nil {
				return fmt.Errorf("integerEquals: invalid number %s", n.String())
			}
			i = int64(f)
		}
		c.IntegerEquals = int(i)
		return nil
	case CondIntegerRange:
		var arr []json.Number
		if err := json.Unmarshal(raw, &arr); err != nil {
			return fmt.Errorf("integerRange: expected [a, b]")
		}
		if len(arr) != 2 {
			return fmt.Errorf("integerRange: expected exactly 2 elements, got %d", len(arr))
		}
		a, _ := arr[0].Int64()
		b, _ := arr[1].Int64()
		c.IntegerRange = [2]int{int(a), int(b)}
		return nil
	case CondNumberEquals:
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return fmt.Errorf("numberEquals: expected number")
		}
		f, _ := n.Float64()
		c.NumberEquals = f
		return nil
	case CondNumberRange:
		var arr []json.Number
		if err := json.Unmarshal(raw, &arr); err != nil {
			return fmt.Errorf("numberRange: expected [a, b]")
		}
		if len(arr) != 2 {
			return fmt.Errorf("numberRange: expected exactly 2 elements, got %d", len(arr))
		}
		a, _ := arr[0].Float64()
		b, _ := arr[1].Float64()
		c.NumberRange = [2]float64{a, b}
		return nil
	case CondContains:
		return json.Unmarshal(raw, &c.Contains)
	case CondIn:
		return json.Unmarshal(raw, &c.In)
	case CondJSONMatches:
		c.JSONMatches = raw
		return nil
	case CondMatches:
		return json.Unmarshal(raw, &c.Matches)
	case CondAnd:
		return json.Unmarshal(raw, &c.And)
	case CondOr:
		return json.Unmarshal(raw, &c.Or)
	case CondNot:
		var inner Condition
		if err := json.Unmarshal(raw, &inner); err != nil {
			return fmt.Errorf("not: %w", err)
		}
		c.Not = &inner
		return nil
	}

	return nil
}

// ---------------------------------------------------------------------------
// LadderID — @author/name[@v3] or local/name
// ---------------------------------------------------------------------------

// LadderID is a parsed ladder identifier.
type LadderID struct {
	Author  string
	Name    string
	Version string // "" means latest
	IsLocal bool
}

// String formats the LadderID back to its canonical string form.
func (l LadderID) String() string {
	if l.IsLocal {
		return "local/" + l.Name
	}
	s := "@" + l.Author + "/" + l.Name
	if l.Version != "" {
		s += "@" + l.Version
	}
	return s
}

// ParseLadderID parses "@author/name[@v3]" or "local/name".
func ParseLadderID(s string) (*LadderID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("ladderId: empty")
	}

	// local/ namespace
	if strings.HasPrefix(s, "local/") {
		name := strings.TrimPrefix(s, "local/")
		if name == "" || strings.Contains(name, "/") {
			return nil, fmt.Errorf("ladderId: invalid local name %q", s)
		}
		return &LadderID{Author: "local", Name: name, IsLocal: true}, nil
	}

	// @author/name[@vN]
	if !strings.HasPrefix(s, "@") {
		return nil, fmt.Errorf("ladderId: must start with @ or local/, got %q", s)
	}
	rest := s[1:]

	// Extract version suffix if present (@v3 at the end)
	version := ""
	// Find the LAST @ to handle names that might contain @ (unlikely but safe)
	// Actually, version is always @vN where N is a number.
	// Look for @v followed by digits at the end.
	if idx := strings.LastIndex(rest, "@v"); idx > 0 {
		suffix := rest[idx+1:] // "v3"
		if len(suffix) > 1 && suffix[0] == 'v' {
			version = suffix
			rest = rest[:idx]
		}
	}

	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return nil, fmt.Errorf("ladderId: expected @author/name, got %q", s)
	}
	author := rest[:slash]
	name := rest[slash+1:]
	if author == "" || name == "" {
		return nil, fmt.Errorf("ladderId: empty author or name in %q", s)
	}

	return &LadderID{Author: author, Name: name, Version: version}, nil
}

// ---------------------------------------------------------------------------
// JumpTarget
// ---------------------------------------------------------------------------

// JumpTarget identifies where a jump goes.
//
// JSON forms:
//
//	{ stepId: "refine" }                                → within-ladder jump
//	{ ladderId: "@alice/helper" }                        → cross-ladder from start
//	{ ladderId: "@alice/helper", stepId: "step3" }       → cross-ladder specific step
//
// Any jump (intra- or cross-ladder) may declare passFields to wire named values
// directly into the target step's fields. See PassField for the wiring grammar.
type JumpTarget struct {
	StepID     string      `json:"stepId,omitempty"`
	LadderID   string      `json:"ladderId,omitempty"`
	PassFields []PassField `json:"passFields,omitempty"`
}

// PassField is a single name-to-source binding on a jump.
//
// `to` is the field name on the TARGET step that the value will be written into.
// `from` is the source spec resolved in the CALLER's context, using the unified
// FromSpec vocabulary (previous, output, "input.X", literal, {stepId}, {relik}).
//
// The resolved value overrides the target field's declared `from:` for the
// duration of this one execution — passField is highest-priority in the
// resolution order (passField > from > fallback).
type PassField struct {
	To   string   `json:"to"`
	From FromSpec `json:"from"`
}

// IsCrossLadder returns true if this jump targets a different ladder.
func (j JumpTarget) IsCrossLadder() bool { return j.LadderID != "" }

// ParseLadderID parses the ladderId field, returning nil if not a cross-ladder jump.
func (j JumpTarget) ParsedLadderID() (*LadderID, error) {
	if j.LadderID == "" {
		return nil, nil
	}
	return ParseLadderID(j.LadderID)
}

// ---------------------------------------------------------------------------
// Action — what to do after a gate evaluation
// ---------------------------------------------------------------------------

// ActionKind discriminates the Action union.
type ActionKind int

const (
	ActionContinue ActionKind = iota
	ActionJump
	ActionAbort
)

// Action is what fires under then/else/onInvalid. Gates are control-flow only:
// they decide where execution goes next (continue, jump, or abort). Data
// operations (relik writes) are step-level capabilities, not gate actions.
//
// JSON forms:
//
//	"continue"                    → ActionContinue
//	"abort"                       → ActionAbort
//	{ jump: { stepId: "x" } }     → ActionJump
type Action struct {
	Kind ActionKind
	Jump *JumpTarget
}

// ContinueAction returns a continue action.
func ContinueAction() Action { return Action{Kind: ActionContinue} }

// AbortAction returns an abort action.
func AbortAction() Action { return Action{Kind: ActionAbort} }

// MarshalJSON encodes the action.
func (a Action) MarshalJSON() ([]byte, error) {
	switch a.Kind {
	case ActionContinue:
		return json.Marshal("continue")
	case ActionAbort:
		return json.Marshal("abort")
	case ActionJump:
		if a.Jump != nil {
			return json.Marshal(map[string]any{"jump": a.Jump})
		}
		return json.Marshal("continue")
	default:
		return json.Marshal("continue")
	}
}

// UnmarshalJSON dispatches by JSON type.
func (a *Action) UnmarshalJSON(data []byte) error {
	// Try bare string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch s {
		case "continue":
			a.Kind = ActionContinue
			return nil
		case "abort":
			a.Kind = ActionAbort
			return nil
		default:
			return fmt.Errorf("action: unknown keyword %q (expected continue or abort)", s)
		}
	}

	// Try object with jump or write key.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("action: expected string or object, got %s", string(data))
	}

	if raw, ok := obj["jump"]; ok {
		a.Kind = ActionJump
		a.Jump = &JumpTarget{}
		return json.Unmarshal(raw, a.Jump)
	}

	return fmt.Errorf("action: object must have \"jump\" key")
}

// ---------------------------------------------------------------------------
// If — the full typed gate construct
// ---------------------------------------------------------------------------

// If is a typed gate: condition + then/else actions.
//
// JSON forms:
//
//	"1"                                              → shorthand: equals "1", then continue, else abort
//	{ equals: "1" }                                  → condition only, then continue, else abort
//	{ integerRange: [4,5], then: continue }          → condition + then
//	{ integerRange: [4,5], then: continue, else: { jump: { stepId: "x" } } }
type If struct {
	Condition Condition
	Then      Action // default: continue
	Else      Action // default: abort
}

// IsShorthandContinueIf returns true if this If is equivalent to the old
// continueIf: "X" — i.e., equals condition, then continue, else abort.
func (i If) IsShorthandContinueIf() bool {
	return i.Condition.Kind == CondEquals &&
		i.Then.Kind == ActionContinue &&
		i.Else.Kind == ActionAbort
}

// ShorthandValue returns the string value if this is a shorthand continueIf;
// otherwise returns "".
func (i If) ShorthandValue() string {
	if i.IsShorthandContinueIf() {
		return i.Condition.Equals
	}
	return ""
}

// MarshalJSON encodes the If block.
func (i If) MarshalJSON() ([]byte, error) {
	// Shorthand: if it's equivalent to continueIf: "X", emit as bare string.
	if i.IsShorthandContinueIf() {
		return json.Marshal(i.Condition.Equals)
	}

	// Full form: merge condition keys + then + else into one object.
	condJSON, err := i.Condition.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(condJSON, &m); err != nil {
		return nil, err
	}

	// Add then/else only if non-default.
	if !(i.Then.Kind == ActionContinue) {
		thenJSON, err := i.Then.MarshalJSON()
		if err != nil {
			return nil, err
		}
		m["then"] = thenJSON
	}
	if !(i.Else.Kind == ActionAbort) {
		elseJSON, err := i.Else.MarshalJSON()
		if err != nil {
			return nil, err
		}
		m["else"] = elseJSON
	}
	return json.Marshal(m)
}

// UnmarshalJSON handles both shorthand string and full object form.
func (i *If) UnmarshalJSON(data []byte) error {
	// Try shorthand: bare string.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		i.Condition = Condition{Kind: CondEquals, Equals: s}
		i.Then = ContinueAction()
		i.Else = AbortAction()
		return nil
	}

	// Full object form.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("if: expected string or object, got %s", string(data))
	}

	// Extract the condition (all keys except then/else).
	condData, err := extractConditionData(obj)
	if err != nil {
		return err
	}
	if err := i.Condition.UnmarshalJSON(condData); err != nil {
		return err
	}

	// Defaults.
	i.Then = ContinueAction()
	i.Else = AbortAction()

	// Parse then.
	if raw, ok := obj["then"]; ok {
		if err := i.Then.UnmarshalJSON(raw); err != nil {
			return fmt.Errorf("if.then: %w", err)
		}
	}

	// Parse else.
	if raw, ok := obj["else"]; ok {
		if err := i.Else.UnmarshalJSON(raw); err != nil {
			return fmt.Errorf("if.else: %w", err)
		}
	}

	return nil
}

// extractConditionData rebuilds a JSON object containing only the condition key
// (everything except "then" and "else").
func extractConditionData(obj map[string]json.RawMessage) (json.RawMessage, error) {
	cond := make(map[string]json.RawMessage)
	for k, v := range obj {
		if k == "then" || k == "else" {
			continue
		}
		cond[k] = v
	}
	if len(cond) == 0 {
		return nil, fmt.Errorf("if: no condition key found")
	}
	return json.Marshal(cond)
}
