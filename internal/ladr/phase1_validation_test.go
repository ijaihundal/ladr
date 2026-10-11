// Redeo - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package ladr_test

import (
	"fmt"
	"testing"
)

// fullCfg builds a complete PipelineConfig JSON with optional extra top-level
// sections (reliks, executionBudget, defaultManifest) that go INSIDE the config
// object. Each extra should be a complete JSON key-value pair like
// `"reliks":[...]`.
func fullCfg(stepsBody, exit string, extras ...string) string {
	extra := ""
	for _, e := range extras {
		extra += "," + e
	}
	return fmt.Sprintf(
		`{"name":"T","allowedTargets":{"strategy":"universal"},"exit":%q,"knobs":{},"steps":[%s]%s}`,
		exit, stepsBody, extra,
	)
}

// relikDecl is a shorthand JSON snippet for a single relik with one folder.
const relikFacts = `"reliks":[{"title":"S","folders":[{"name":"f","tokenLimit":100,"files":[{"name":"a.md","init":"blank"}]}]}]`

// TestValidateNoContinueIfConflict verifies the rule that a step cannot declare
// BOTH continueIf (deprecated) and if (the new typed gate). They are mutually
// exclusive — authors must migrate to `if` alone.
func TestValidateNoContinueIfConflict(t *testing.T) {
	body := `{"id":"a","name":"A","type":"normal","nodes":3,"continueIf":"1","if":{"equals":"1"}},` + step("b")
	r := validate(t, wrap(body, "b"))
	if r.Valid {
		t.Fatal("expected invalid when both continueIf and if are declared")
	}
	if !containsError(r.Errors, "cannot declare both") {
		t.Errorf("expected 'cannot declare both' error, got: %v", r.Errors)
	}
}

// TestValidateRelikDeclarations covers the structural validation of declared
// reliks: empty titles, duplicate titles, empty folder names, duplicate folders,
// duplicate metatags, duplicate files, missing file names, etc.
func TestValidateRelikDeclarations(t *testing.T) {
	t.Run("empty relik title rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0", `"reliks":[{"title":"","folders":[{"name":"f","tokenLimit":100}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected empty relik title to be rejected")
		}
	})

	t.Run("duplicate relik titles rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"reliks":[{"title":"Dup","folders":[{"name":"f","tokenLimit":100}]},{"title":"Dup","folders":[{"name":"f","tokenLimit":100}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected duplicate relik titles to be rejected")
		}
		if !containsError(r.Errors, "Duplicate relik title") {
			t.Errorf("expected 'Duplicate relik title' error, got: %v", r.Errors)
		}
	})

	t.Run("empty folder name rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0", `"reliks":[{"title":"S","folders":[{"name":"","tokenLimit":100}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected empty folder name to be rejected")
		}
	})

	t.Run("zero tokenLimit rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0", `"reliks":[{"title":"S","folders":[{"name":"f","tokenLimit":0}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected tokenLimit=0 to be rejected")
		}
	})

	t.Run("duplicate folder names rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"reliks":[{"title":"S","folders":[{"name":"f","tokenLimit":100},{"name":"f","tokenLimit":100}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected duplicate folder names to be rejected")
		}
	})

	t.Run("duplicate file names rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"reliks":[{"title":"S","folders":[{"name":"f","tokenLimit":100,"files":[{"name":"a.md","init":"blank"},{"name":"a.md","init":"blank"}]}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected duplicate file names to be rejected")
		}
	})

	t.Run("invalid metatag type rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"reliks":[{"title":"S","folders":[{"name":"f","tokenLimit":100,"metatags":[{"name":"m","type":"banana"}]}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected invalid metatag type to be rejected")
		}
	})

	t.Run("object metatag without schema rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"reliks":[{"title":"S","folders":[{"name":"f","tokenLimit":100,"metatags":[{"name":"m","type":"object"}]}]}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected object metatag without schema to be rejected")
		}
	})

	t.Run("valid relik declaration accepted", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0", relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected valid relik decl to pass, got: %v", r.Errors)
		}
	})
}

// TestValidateRelikReadReferences covers relikRead field static validation.
func TestValidateRelikReadReferences(t *testing.T) {
	t.Run("relikRead on non-existent relik rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[` +
			`{"name":"X","type":"relikRead","from":{"relik":"NoRelik","folder":"f","file":"a.md"}}` +
			`]}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected relikRead on non-existent relik to be rejected")
		}
	})

	t.Run("relikRead on non-existent folder rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[` +
			`{"name":"X","type":"relikRead","from":{"relik":"S","folder":"NoFolder","file":"a.md"}}` +
			`]}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected relikRead on non-existent folder to be rejected")
		}
	})

	t.Run("relikRead on non-existent file rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[` +
			`{"name":"X","type":"relikRead","from":{"relik":"S","folder":"f","file":"ghost.md"}}` +
			`]}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected relikRead on non-existent file to be rejected")
		}
	})

	t.Run("relikRead on undeclared user metatag rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[` +
			`{"name":"X","type":"relikRead","from":{"relik":"S","folder":"f","file":"a.md","metatag":"confidence"}}` +
			`]}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected relikRead on undeclared user metatag to be rejected")
		}
	})

	t.Run("relikRead system metatag always valid", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[` +
			`{"name":"X","type":"relikRead","from":{"relik":"S","folder":"f","metatag":"index"}}` +
			`]}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected system metatag 'index' to be valid, got: %v", r.Errors)
		}
	})

	t.Run("relikRead valid file read accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[` +
			`{"name":"X","type":"relikRead","from":{"relik":"S","folder":"f","file":"a.md"}}` +
			`]}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected valid relikRead to pass, got: %v", r.Errors)
		}
	})
}

// TestValidateRelikWrites covers relikWrite static validation.
func TestValidateRelikWrites(t *testing.T) {
	t.Run("relikWrite on non-existent relik rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikWrite":{"to":{"relik":"Ghost","folder":"f"},"match":{"type":"object"}}}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected non-existent relik target to be rejected")
		}
	})

	t.Run("mergeByKey without key rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikWrite":{"to":{"relik":"S","folder":"f","file":"a.md"},"on":"mergeByKey","match":{"type":"object"}}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected mergeByKey without key to be rejected")
		}
	})

	t.Run("key without mergeByKey rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikWrite":{"to":{"relik":"S","folder":"f","file":"a.md"},"key":"id","match":{"type":"object"}}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected key without mergeByKey to be rejected")
		}
	})

	t.Run("valid mergeByKey accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikWrite":{"to":{"relik":"S","folder":"f","file":"a.md"},"on":"mergeByKey","key":"id","match":{"type":"object"}}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected valid mergeByKey to pass, got: %v", r.Errors)
		}
	})

	t.Run("valid append write accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikWrite":{"to":{"relik":"S","folder":"f","file":"a.md"},"on":"append","match":{"type":"object"}}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected valid append write to pass, got: %v", r.Errors)
		}
	})
}

// TestValidateRelikReadKey covers relikRead: adaptive key static validation.
func TestValidateRelikReadKey(t *testing.T) {
	t.Run("relikRead maxRounds <= 0 rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikRead":{"from":{"relik":"S","folder":"f"},"as":"Docs","maxRounds":0,"requestMatch":{"type":"object"}}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected maxRounds=0 to be rejected")
		}
	})

	t.Run("relikRead empty requestMatch rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikRead":{"from":{"relik":"S","folder":"f"},"as":"Docs","maxRounds":3}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected missing requestMatch to be rejected")
		}
	})

	t.Run("relikRead on non-existent relik rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikRead":{"from":{"relik":"Ghost","folder":"f"},"as":"Docs","maxRounds":3,"requestMatch":{"type":"object"}}}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected relikRead on non-existent relik to be rejected")
		}
	})

	t.Run("valid relikRead accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","relikRead":{"from":{"relik":"S","folder":"f"},"as":"Docs","maxRounds":3,"requestMatch":{"type":"object"}}}`
		raw := fullCfg(body, "s0", relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected valid relikRead to pass, got: %v", r.Errors)
		}
	})
}

// TestValidateDynamicSteps covers dynamic: true structural validation.
func TestValidateDynamicSteps(t *testing.T) {
	t.Run("dynamic without from rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","dynamic":true},` + step("s1")
		r := validate(t, wrap(body, "s1"))
		if r.Valid {
			t.Error("expected dynamic: true without from to be rejected")
		}
		if !containsError(r.Errors, `requires "from"`) {
			t.Errorf("expected 'requires \"from\"' error, got: %v", r.Errors)
		}
	})

	t.Run("dynamic with explicit systemPrompt rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","dynamic":true,"from":"output","systemPrompt":"x"},` + step("s1")
		r := validate(t, wrap(body, "s1"))
		if r.Valid {
			t.Error("expected dynamic: true with systemPrompt to be rejected")
		}
	})

	t.Run("dynamic with explicit fields rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","dynamic":true,"from":"output","fields":[{"name":"X","type":"text","from":"input.context"}]},` + step("s1")
		r := validate(t, wrap(body, "s1"))
		if r.Valid {
			t.Error("expected dynamic: true with fields to be rejected")
		}
	})

	t.Run("dynamic with explicit nodes rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","dynamic":true,"from":"output","nodes":3},` + step("s1")
		r := validate(t, wrap(body, "s1"))
		if r.Valid {
			t.Error("expected dynamic: true with nodes to be rejected")
		}
	})

	t.Run("dynamic with from only accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","dynamic":true,"from":"output"},` + step("s1")
		r := validate(t, wrap(body, "s1"))
		if !r.Valid {
			t.Errorf("expected dynamic: true with only from to pass, got: %v", r.Errors)
		}
	})
}

// TestValidateFallbackReferences covers fallback FromSpec static validation.
func TestValidateFallbackReferences(t *testing.T) {
	t.Run("text fallback step ref to ghost rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"X","type":"text","from":"input.context","fallback":{"stepId":"ghost"}}]}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected text fallback step ref to ghost to be rejected")
		}
	})

	t.Run("text fallback relik ref to ghost rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"X","type":"text","from":"input.context","fallback":{"relik":"Ghost","folder":"f"}}]}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected text fallback relik ref to ghost to be rejected")
		}
	})

	t.Run("text fallback literal always accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"X","type":"text","from":"input.context","fallback":"default value"}]}`
		r := validate(t, wrap(body, "s0"))
		if !r.Valid {
			t.Errorf("expected literal fallback to pass, got: %v", r.Errors)
		}
	})

	t.Run("text fallback input.X always accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"X","type":"text","from":"input.context","fallback":"input.something"}]}`
		r := validate(t, wrap(body, "s0"))
		if !r.Valid {
			t.Errorf("expected input fallback to pass, got: %v", r.Errors)
		}
	})

	t.Run("ingest fallback step ref to ghost rejected", func(t *testing.T) {
		body := `{"id":"real","name":"R","type":"normal","systemPrompt":"x"},` +
			`{"id":"s0","name":"S","type":"normal","fields":[{"name":"X","type":"ingest","from":{"stepId":"real","loopRef":"current"},"fallback":{"stepId":"ghost"}}]}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected ingest fallback ghost step ref to be rejected")
		}
	})
}

// TestValidateManifestViews covers defaultManifest + per-field manifest from:
// validation.
func TestValidateManifestViews(t *testing.T) {
	t.Run("defaultManifest index view on non-existent relik rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"defaultManifest":[{"index":{"relik":"Ghost","folder":"f"}}]`)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected defaultManifest on non-existent relik to be rejected")
		}
	})

	t.Run("defaultManifest index view on non-existent folder rejected", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"defaultManifest":[{"index":{"relik":"S","folder":"ghost"}}]`,
			relikFacts)
		r := validate(t, raw)
		if r.Valid {
			t.Error("expected defaultManifest on non-existent folder to be rejected")
		}
	})

	t.Run("field manifest from on non-existent relik rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"Avail","type":"manifest","from":{"index":{"relik":"Ghost","folder":"f"}}}]}`
		r := validate(t, wrap(body, "s0"))
		if r.Valid {
			t.Error("expected field manifest from on non-existent relik to be rejected")
		}
	})

	t.Run("defaultManifest time+structure always valid", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"defaultManifest":["time","structure"]`)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected defaultManifest with time+structure to pass, got: %v", r.Errors)
		}
	})

	t.Run("valid defaultManifest index accepted", func(t *testing.T) {
		raw := fullCfg(step("s0"), "s0",
			`"defaultManifest":[{"index":{"relik":"S","folder":"f"}}]`,
			relikFacts)
		r := validate(t, raw)
		if !r.Valid {
			t.Errorf("expected valid defaultManifest index to pass, got: %v", r.Errors)
		}
	})
}

// TestValidateConditionIntegerRange_Order exercises the leaf condition range
// validation: if min > max the condition is malformed.
func TestValidateConditionIntegerRange_Order(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"integerRange":[5,1]}},` + step("s1")
	r := validate(t, wrap(body, "s1"))
	if r.Valid {
		t.Error("expected integerRange with min>max to be rejected")
	}
	if !containsError(r.Errors, "integerRange has min") {
		t.Errorf("expected min>max error, got: %v", r.Errors)
	}
}

// TestValidateConditionNumberRange_Order exercises numberRange min>max check.
func TestValidateConditionNumberRange_Order(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"numberRange":[0.9,0.1]}},` + step("s1")
	r := validate(t, wrap(body, "s1"))
	if r.Valid {
		t.Error("expected numberRange with min>max to be rejected")
	}
}

// TestValidateConditionIn_EmptySet exercises in: [] rejection.
func TestValidateConditionIn_EmptySet(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"in":[]}},` + step("s1")
	r := validate(t, wrap(body, "s1"))
	if r.Valid {
		t.Error("expected in: [] to be rejected")
	}
	if !containsError(r.Errors, "if.in has empty") {
		t.Errorf("expected 'if.in has empty' error, got: %v", r.Errors)
	}
}

// TestValidateCrossLadderJump_MalformedLadderId verifies that a cross-ladder
// jump with a malformed ladderId is rejected at static validation time.
func TestValidateCrossLadderJump_MalformedLadderId(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"equals":"GO","then":{"jump":{"ladderId":"not-a-valid-id"}},"else":"abort"}},` + step("s1")
	r := validate(t, wrap(body, "s1"))
	if r.Valid {
		t.Error("expected malformed cross-ladder jump id to be rejected")
	}
	if !containsError(r.Errors, "invalid ladderId") {
		t.Errorf("expected 'invalid ladderId' error, got: %v", r.Errors)
	}
}

// TestValidateCrossLadderJump_ValidLadderId verifies a well-formed cross-ladder
// jump ladderId passes the parse check.
func TestValidateCrossLadderJump_ValidLadderId(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"equals":"GO","then":{"jump":{"ladderId":"@alice/helper"}},"else":"abort"}},` + step("s1")
	r := validate(t, wrap(body, "s1"))
	if !r.Valid {
		// Might have other errors, but should NOT have invalid ladderId.
		if containsError(r.Errors, "invalid ladderId") {
			t.Errorf("should not reject valid @alice/helper ladderId, got: %v", r.Errors)
		}
	}
}

// TestValidateIntraLadderPassField_MissingTarget verifies the static check for
// intra-ladder passField.to — the target step must exist in the same stepMap.
func TestValidateIntraLadderPassField_MissingTarget(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"equals":"GO","then":{"jump":{"stepId":"ghost","passFields":[{"to":"X","from":"output"}]}},"else":"abort"}},` + step("s1")
	r := validate(t, wrap(body, "s1"))
	if r.Valid {
		t.Error("expected intra-ladder passField on missing step to be rejected")
	}
	if !containsError(r.Errors, "non-existent step") {
		t.Errorf("expected 'non-existent step' error, got: %v", r.Errors)
	}
}

// TestValidateIntraLadderPassField_FieldMismatch verifies the static check for
// intra-ladder passField.to — the target field name must match a declared field.
func TestValidateIntraLadderPassField_FieldMismatch(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"equals":"GO","then":{"jump":{"stepId":"target","passFields":[{"to":"Fake","from":"output"}]}},"else":"abort"}},` +
		`{"id":"target","name":"T","type":"normal","fields":[{"name":"Real","type":"text","from":"input.x"}]}`
	r := validate(t, wrap(body, "target"))
	if r.Valid {
		t.Error("expected intra-ladder passField.to mismatch to be rejected")
	}
	if !containsError(r.Errors, "does not match any field") {
		t.Errorf("expected 'does not match any field' error, got: %v", r.Errors)
	}
}

// TestValidateIntraLadderPassField_ValidMatch verifies that a correct
// passField.to (matching a declared field) passes validation.
func TestValidateIntraLadderPassField_ValidMatch(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","nodes":3,"if":{"equals":"GO","then":{"jump":{"stepId":"target","passFields":[{"to":"Real","from":"output"}]}},"else":"abort"}},` +
		`{"id":"target","name":"T","type":"normal","fields":[{"name":"Real","type":"text","from":"input.x"}]}`
	r := validate(t, wrap(body, "target"))
	if !r.Valid {
		t.Errorf("expected valid passField.to match to pass, got: %v", r.Errors)
	}
}

// TestValidateExecutionBudget_Zero verifies that explicitly-zero budgets are
// rejected (would otherwise cause ceiling to trip on the very first call).
func TestValidateExecutionBudget_Zero(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"maxSpend=0", `"executionBudget":{"maxSpend":0}`},
		{"maxHops=0", `"executionBudget":{"maxHops":0}`},
		{"maxLlmCalls=0", `"executionBudget":{"maxLlmCalls":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := fullCfg(step("s0")+","+step("s1"), "s1", tc.json)
			r := validate(t, raw)
			if r.Valid {
				t.Errorf("expected %s to be rejected", tc.name)
			}
		})
	}
}

// TestValidateExecutionBudget_ExceedsPlatform verifies that budgets exceeding
// the platform maximums are rejected.
func TestValidateExecutionBudget_ExceedsPlatform(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"maxSpend too high", `"executionBudget":{"maxSpend":999.0}`},
		{"maxHops too high", `"executionBudget":{"maxHops":99999}`},
		{"maxLlmCalls too high", `"executionBudget":{"maxLlmCalls":99999}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := fullCfg(step("s0")+","+step("s1"), "s1", tc.json)
			r := validate(t, raw)
			if r.Valid {
				t.Errorf("expected %s to be rejected", tc.name)
			}
		})
	}
}

// TestValidateExecutionBudget_Valid verifies that reasonable budgets pass.
func TestValidateExecutionBudget_Valid(t *testing.T) {
	raw := fullCfg(step("s0")+","+step("s1"), "s1",
		`"executionBudget":{"maxSpend":5.0,"maxHops":300,"maxLlmCalls":500,"maxRecursion":5}`)
	r := validate(t, raw)
	if !r.Valid {
		t.Errorf("expected valid budget to pass, got: %v", r.Errors)
	}
}
