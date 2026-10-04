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

// Package ladr_test — grammar matrix: fills coverage gaps in condition/action
// validation, allowedTargets wildcard rules, knob budget ceilings, relikWrite
// key/match rules, the strictness boundary (root strict vs. body lenient), and
// manifest view references.
//
// Each test pins ONE grammar rule with at least a positive and a negative path.
// Helpers (wrap, validate, assertInvalid, assertValid, containsError) come from
// helpers_test.go / validate_test.go in this package.

package ladr_test

import (
	"encoding/json"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// =============================================================================
// Condition union — regex (matches) + composition (and/or/not)
// 8 of 12 condition keys are covered elsewhere; these 4 are the gap.
// =============================================================================

// stepWithIf builds a normal step carrying an if-gate, wired into a full config.
// relikMap lets the caller declare reliks so relikReferencing conditions resolve.
func stepWithIf(stepID, ifJSON string, reliksJSON string) string {
	if reliksJSON == "" {
		reliksJSON = "[]"
	}
	return `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"` + stepID + `","knobs":{},"reliks":` + reliksJSON + `,"steps":[
		{"id":"` + stepID + `","name":"` + stepID + `","type":"normal","systemPrompt":"x",
		 "if":` + ifJSON + `}
	]}`
}

func TestGrammar_ConditionMatches_Regex(t *testing.T) {
	// Positive: a compilable regex passes validation.
	raw := stepWithIf("s", `{"matches":"^\\d+$"}`, "")
	assertValid(t, raw)

	// Round-trip: the condition parses onto the Matches field.
	cfg := mustValidate(t, raw)
	cond := cfg.Steps[0].Base().If.Condition
	if cond.Kind != ladr.CondMatches {
		t.Fatalf("condition kind = %v, want CondMatches", cond.Kind)
	}
	if cond.Matches != `^\d+$` {
		t.Errorf("Matches = %q, want ^\\d+$", cond.Matches)
	}
}

func TestGrammar_ConditionMatches_EmptyPatternRejected(t *testing.T) {
	raw := stepWithIf("s", `{"matches":""}`, "")
	assertInvalid(t, raw, "if.matches has empty pattern")
}

func TestGrammar_ConditionMatches_InvalidRegexRejected(t *testing.T) {
	// An unbalanced group is not a compilable RE2 pattern.
	raw := stepWithIf("s", `{"matches":"(unclosed"}`, "")
	assertInvalid(t, raw, "if.matches has invalid regex")
}

func TestGrammar_ConditionAnd_Composition(t *testing.T) {
	// and: all sub-conditions validate.
	raw := stepWithIf("s", `{"and":[{"equals":"yes"},{"contains":"y"}]}`, "")
	assertValid(t, raw)

	cfg := mustValidate(t, raw)
	cond := cfg.Steps[0].Base().If.Condition
	if cond.Kind != ladr.CondAnd {
		t.Fatalf("condition kind = %v, want CondAnd", cond.Kind)
	}
	if len(cond.And) != 2 {
		t.Errorf("expected 2 sub-conditions, got %d", len(cond.And))
	}
}

func TestGrammar_ConditionAnd_EmptyArrayRejected(t *testing.T) {
	raw := stepWithIf("s", `{"and":[]}`, "")
	assertInvalid(t, raw, "if.and has empty array")
}

func TestGrammar_ConditionOr_Composition(t *testing.T) {
	raw := stepWithIf("s", `{"or":[{"equals":"a"},{"equals":"b"}]}`, "")
	assertValid(t, raw)

	cfg := mustValidate(t, raw)
	cond := cfg.Steps[0].Base().If.Condition
	if cond.Kind != ladr.CondOr {
		t.Fatalf("condition kind = %v, want CondOr", cond.Kind)
	}
}

func TestGrammar_ConditionOr_EmptyArrayRejected(t *testing.T) {
	raw := stepWithIf("s", `{"or":[]}`, "")
	assertInvalid(t, raw, "if.or has empty array")
}

func TestGrammar_ConditionNot_Composition(t *testing.T) {
	raw := stepWithIf("s", `{"not":{"equals":"no"}}`, "")
	assertValid(t, raw)

	cfg := mustValidate(t, raw)
	cond := cfg.Steps[0].Base().If.Condition
	if cond.Kind != ladr.CondNot {
		t.Fatalf("condition kind = %v, want CondNot", cond.Kind)
	}
	if cond.Not == nil {
		t.Fatal("Not sub-condition is nil after parse")
	}
}

func TestGrammar_ConditionNot_MissingSubRejected(t *testing.T) {
	// null sub-condition — Condition.UnmarshalJSON rejects this at parse time
	// ("no condition key found in object"), before validation runs.
	raw := stepWithIf("s", `{"not":null}`, "")
	_, err := ladr.ParseStoredConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected parse error for not:null condition, got nil")
	}
}

func TestGrammar_ConditionDeepNesting(t *testing.T) {
	// and(or(not(equals)), contains) — deeply composed gate validates + round-trips.
	raw := stepWithIf("s",
		`{"and":[{"or":[{"not":{"equals":"x"}},{"contains":"y"}]},{"matches":"z"}]}`, "")
	assertValid(t, raw)
}

func TestGrammar_ConditionMultiKeyRejected(t *testing.T) {
	// A condition block must have exactly one key.
	raw := stepWithIf("s", `{"equals":"a","contains":"b"}`, "")
	// This is a parse-time error (UnmarshalJSON enforces single-key).
	_, err := ladr.ParseStoredConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected parse error for multi-key condition, got nil")
	}
}

// =============================================================================
// Action union — abort + write + unknown-key rejection
// continue/jump are exercised elsewhere; abort/write and the negative paths
// are the gap.
// =============================================================================

func TestGrammar_ActionAbort_Parses(t *testing.T) {
	raw := stepWithIf("s", `{"equals":"x","then":"abort","else":"continue"}`, "")
	cfg := mustValidate(t, raw)
	if cfg.Steps[0].Base().If.Then.Kind != ladr.ActionAbort {
		t.Errorf("then kind = %v, want ActionAbort", cfg.Steps[0].Base().If.Then.Kind)
	}
}

func TestGrammar_ActionAbort_RoundTrip(t *testing.T) {
	a := ladr.AbortAction()
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal abort: %v", err)
	}
	if string(out) != `"abort"` {
		t.Errorf("abort round-trip = %s, want \"abort\"", string(out))
	}
}

func TestGrammar_ActionUnknownKeywordRejected(t *testing.T) {
	// An unknown bare-string action keyword is a parse error.
	raw := stepWithIf("s", `{"equals":"x","then":"explode"}`, "")
	_, err := ladr.ParseStoredConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected parse error for unknown action keyword, got nil")
	}
}

func TestGrammar_ActionObjectWithoutJumpOrWriteRejected(t *testing.T) {
	raw := stepWithIf("s", `{"equals":"x","then":{"foo":"bar"}}`, "")
	_, err := ladr.ParseStoredConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected parse error for action object without jump/write, got nil")
	}
}

// =============================================================================
// allowedTargets — wildcard mixing rules
// =============================================================================

func TestGrammar_AllowedTargets_WildcardWithExplicitProvidersRejected(t *testing.T) {
	body := step("s")
	raw := `{"name":"T","allowedTargets":{"strategy":"constrained","providers":["*","openai"]},"exit":"s","knobs":{},"steps":[` + body + `]}`
	assertInvalid(t, raw, `cannot combine "*" with explicit providers`)
}

func TestGrammar_AllowedTargets_WildcardWithExplicitModelsRejected(t *testing.T) {
	body := step("s")
	raw := `{"name":"T","allowedTargets":{"strategy":"constrained","models":["*","gpt-4"]},"exit":"s","knobs":{},"steps":[` + body + `]}`
	assertInvalid(t, raw, `cannot combine "*" with explicit models`)
}

func TestGrammar_AllowedTargets_WildcardAloneAccepted(t *testing.T) {
	body := step("s")
	raw := `{"name":"T","allowedTargets":{"strategy":"constrained","providers":["*"],"models":["*"]},"exit":"s","knobs":{},"steps":[` + body + `]}`
	assertValid(t, raw)
}

func TestGrammar_AllowedTargets_ConstrainedEmptyRejected(t *testing.T) {
	body := step("s")
	raw := `{"name":"T","allowedTargets":{"strategy":"constrained","providers":[],"models":[]},"exit":"s","knobs":{},"steps":[` + body + `]}`
	assertInvalid(t, raw, "")
}

// =============================================================================
// Knob budget sanity ceilings (loops ≤ 50, nodes ≤ 20, recursion ≤ 5)
// =============================================================================

func TestGrammar_KnobBudget_LoopsExceeds50(t *testing.T) {
	// A loops knob with default > 50 must be rejected by the budget-sanity check.
	knobs := `{"k":{"name":"k","type":"loops","input":"numerical","default":60,"min":1,"max":100}}`
	body := step("s")
	raw := wrap(body, "s", withKnobs(knobs))
	assertInvalid(t, raw, "exceeds maximum")
}

func TestGrammar_KnobBudget_NodesExceeds20(t *testing.T) {
	knobs := `{"k":{"name":"k","type":"nodes","input":"numerical","default":25,"min":1,"max":50}}`
	body := step("s")
	raw := wrap(body, "s", withKnobs(knobs))
	assertInvalid(t, raw, "exceeds maximum")
}

func TestGrammar_KnobBudget_RecursionExceeds5(t *testing.T) {
	knobs := `{"k":{"name":"k","type":"recursion","input":"numerical","default":8,"min":1,"max":10}}`
	body := step("s")
	raw := wrap(body, "s", withKnobs(knobs))
	assertInvalid(t, raw, "exceeds maximum")
}

func TestGrammar_KnobBudget_WithinCeilingAccepted(t *testing.T) {
	// The boundary value itself (50/20/5) must be accepted.
	knobs := `{"k":{"name":"k","type":"loops","input":"numerical","default":50,"min":1,"max":50}}`
	body := step("s")
	raw := wrap(body, "s", withKnobs(knobs))
	assertValid(t, raw)
}

// =============================================================================
// RelikWrite — mergeByKey key requirement + match requirement
// =============================================================================

func relikWriteStep(stepID, on, key, match string) string {
	return `{"id":"` + stepID + `","name":"` + stepID + `","type":"normal","systemPrompt":"x",
		"relikWrite":{"to":{"relik":"Mem","folder":"docs"},"on":"` + on + `","key":` + key + `,"match":` + match + `}}`
}

func TestGrammar_RelikWrite_MergeByKeyRequiresKey(t *testing.T) {
	reliks := `[{"title":"Mem","folders":[{"name":"docs","tokenLimit":500}]}]`
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,"steps":[` +
		relikWriteStep("s", "mergeByKey", `""`, `{"type":"string"}`) + `]}`
	assertInvalid(t, raw, `relikWrite.on "mergeByKey" requires "key" field`)
}

func TestGrammar_RelikWrite_KeyOnlyValidWithMergeByKey(t *testing.T) {
	reliks := `[{"title":"Mem","folders":[{"name":"docs","tokenLimit":500}]}]`
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,"steps":[` +
		relikWriteStep("s", "append", `"id"`, `{"type":"string"}`) + `]}`
	assertInvalid(t, raw, `relikWrite.key is only valid with on: "mergeByKey"`)
}

func TestGrammar_RelikWrite_MatchRequired(t *testing.T) {
	reliks := `[{"title":"Mem","folders":[{"name":"docs","tokenLimit":500}]}]`
	// match omitted entirely → the RelikWrite.Match field stays nil → validator rejects.
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,"steps":[
		{"id":"s","name":"s","type":"normal","systemPrompt":"x",
		 "relikWrite":{"to":{"relik":"Mem","folder":"docs"},"on":"append"}}
	]}`
	assertInvalid(t, raw, `relikWrite.match must be a non-empty JSON Schema`)
}

func TestGrammar_RelikWrite_ValidMergeByKey(t *testing.T) {
	reliks := `[{"title":"Mem","folders":[{"name":"docs","tokenLimit":500}]}]`
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,"steps":[` +
		relikWriteStep("s", "mergeByKey", `"id"`, `{"type":"string"}`) + `]}`
	assertValid(t, raw)
}

// =============================================================================
// Strictness boundary — root strict, bodies lenient (documented design decision)
// =============================================================================

func TestGrammar_Strictness_RootRejectsUnknownKey(t *testing.T) {
	// Unknown top-level key → rejected (the only strict node).
	body := step("s")
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"bogusTopLevel":true,"steps":[` + body + `]}`
	if err := parseStoredErr(raw); err == nil {
		t.Fatal("expected parse error for unknown top-level key, got nil")
	}
}

func TestGrammar_Strictness_StepBodySilentlyIgnoresUnknownKey(t *testing.T) {
	// Unknown key inside a step body is silently dropped (not an error). This is
	// intentional — only PipelineConfig root is strict.
	body := `{"id":"s","name":"s","type":"normal","systemPrompt":"x","bogusStepKey":123}`
	raw := wrap(body, "s")
	assertValid(t, raw)
}

func TestGrammar_Strictness_AdaptiveKeyBodySilentlyIgnoresUnknownKey(t *testing.T) {
	// Unknown key inside a codeExecution block is silently dropped (step body
	// parsing is lenient — only top-level PipelineConfig is strict).
	body := `{"id":"s","name":"s","type":"normal","systemPrompt":"x",
		"codeExecution":{"maxRounds":3,"runtime":"python","as":"R",
			"requestMatch":{"type":"object","properties":{"code":{"type":"string"}}},"bogusKey":true}}`
	raw := wrap(body, "s")
	assertValid(t, raw)
}

// =============================================================================
// ManifestView — index/tree references validate against declared reliks
// =============================================================================

func TestGrammar_ManifestView_IndexNonexistentRelikRejected(t *testing.T) {
	reliks := `[{"title":"Real","folders":[{"name":"docs","tokenLimit":500}]}]`
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,
		"defaultManifest":[{"index":{"relik":"Nope"}}],"steps":[` + step("s") + `]}`
	assertInvalid(t, raw, `references non-existent relik "Nope"`)
}

func TestGrammar_ManifestView_TreeValidRelikAccepted(t *testing.T) {
	reliks := `[{"title":"Real","folders":[{"name":"docs","tokenLimit":500}]}]`
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,
		"defaultManifest":[{"tree":{"relik":"Real"}}],"steps":[` + step("s") + `]}`
	assertValid(t, raw)
}

func TestGrammar_ManifestView_IndexNonexistentFolderRejected(t *testing.T) {
	reliks := `[{"title":"Real","folders":[{"name":"docs","tokenLimit":500}]}]`
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"reliks":` + reliks + `,
		"defaultManifest":[{"index":{"relik":"Real","folder":"ghost"}}],"steps":[` + step("s") + `]}`
	assertInvalid(t, raw, `references non-existent folder "ghost"`)
}
