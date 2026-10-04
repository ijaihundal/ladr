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

package ladr_test

import (
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// Helper: a valid 3-step slider knob JSON snippet (for use inside withKnobs).
const sliderKnob = `"cov":{"name":"Cov","type":"nodes","input":"slider","steps":[{"title":"A","value":3,"default":true},{"title":"B","value":6},{"title":"C","value":12}]}`

func numericalKnob(name, ktype string) string {
	return `"k":{"name":"` + name + `","type":"` + ktype + `","input":"numerical","default":5,"min":1,"max":10}`
}

func assertValid(t *testing.T, raw string) {
	t.Helper()
	v := validate(t, raw)
	if !v.Valid {
		t.Errorf("expected valid, got: %v", v.Errors)
	}
}

func assertInvalid(t *testing.T, raw string, substr string) {
	t.Helper()
	v := validate(t, raw)
	if v.Valid {
		t.Fatalf("expected invalid (want %q), got valid", substr)
	}
	if substr != "" && !containsError(v.Errors, substr) {
		t.Errorf("expected error containing %q, got: %v", substr, v.Errors)
	}
}

// --- 1. step uniqueness ----------------------------------------------------

func TestValidateStepUniqueness(t *testing.T) {
	t.Run("duplicate top-level ids", func(t *testing.T) {
		assertInvalid(t, wrap(step("dup")+","+step("dup"), "dup"), "Duplicate step id")
	})
	t.Run("duplicate inside group children", func(t *testing.T) {
		body := `{"id":"g","name":"G","type":"group","steps":[` + step("x") + `,` + step("x") + `]},` + step("s1")
		assertInvalid(t, wrap(body, "s1"), "Duplicate step id")
	})
	t.Run("unique ids valid", func(t *testing.T) {
		assertValid(t, wrap(step("a")+","+step("b"), "b"))
	})
}

// --- 2. nested groups ------------------------------------------------------

func TestValidateNestedGroups(t *testing.T) {
	body := `{"id":"outer","name":"O","type":"group","steps":[{"id":"inner","name":"I","type":"group","steps":[` + step("a") + `,` + step("b") + `]},` + step("c") + `]},` + step("s1")
	assertInvalid(t, wrap(body, "s1"), "Nested group")
}

// --- 3. exit ---------------------------------------------------------------

func TestValidateExit(t *testing.T) {
	t.Run("non-existent exit", func(t *testing.T) {
		assertInvalid(t, wrap(step("s0"), "ghost"), "non-existent step id")
	})
	t.Run("normal step with nodes as exit rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","nodes":5}`
		assertInvalid(t, wrap(body, "s0"), "sequential")
	})
	t.Run("sequential step with nodes as exit accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"sequential","nodes":3}`
		assertValid(t, wrap(body, "s0"))
	})
	t.Run("normal step without nodes as exit accepted", func(t *testing.T) {
		assertValid(t, wrap(step("s0"), "s0"))
	})
}

// --- 4. timeline init ------------------------------------------------------

func TestValidateTimelineInit(t *testing.T) {
	t.Run("multiple init rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","timeline":"init"},{"id":"b","name":"B","type":"normal","timeline":"init"},` + step("c")
		assertInvalid(t, wrap(body, "c"), "init")
	})
	t.Run("init with nodes rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","timeline":"init","nodes":3},` + step("b")
		assertInvalid(t, wrap(body, "b"), "cannot have nodes")
	})
	t.Run("init same as exit rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","timeline":"init"}`
		assertInvalid(t, wrap(body, "a"), "same as exit")
	})
	t.Run("single init valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","timeline":"init"},` + step("b")
		assertValid(t, wrap(body, "b"))
	})
	t.Run("timeline circle valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","timeline":"circle"},` + step("b")
		assertValid(t, wrap(body, "b"))
	})
}

// --- 5. allowedTargets -----------------------------------------------------

func TestValidateAllowedTargets(t *testing.T) {
	t.Run("universal valid", func(t *testing.T) {
		assertValid(t, wrap(step("s0"), "s0"))
	})
	t.Run("constrained empty providers rejected", func(t *testing.T) {
		assertInvalid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":[],"models":["x"]}`)),
			"providers must include at least one")
	})
	t.Run("constrained empty models rejected", func(t *testing.T) {
		assertInvalid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":["x"],"models":[]}`)),
			"models must include at least one")
	})
	t.Run("wildcard mixed providers rejected", func(t *testing.T) {
		assertInvalid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":["*","openrouter"],"models":["x"]}`)),
			`cannot combine "*" with explicit providers`)
	})
	t.Run("wildcard mixed models rejected", func(t *testing.T) {
		assertInvalid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":["x"],"models":["*","y"]}`)),
			`cannot combine "*" with explicit models`)
	})
	t.Run("wildcard-only providers valid", func(t *testing.T) {
		assertValid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":["*"],"models":["x"]}`)))
	})
	t.Run("wildcard-only models valid", func(t *testing.T) {
		assertValid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":["x"],"models":["*"]}`)))
	})
	// Go-specific (stricter than TS): whitespace-only entries rejected.
	t.Run("whitespace-only provider rejected", func(t *testing.T) {
		assertInvalid(t,
			wrap(step("s0"), "s0", withAllowed(`{"strategy":"constrained","providers":[" "],"models":["x"]}`)),
			"non-empty")
	})
}

// --- 6. ingest / multi_ingest references -----------------------------------

func TestValidateIngestReferences(t *testing.T) {
	t.Run("ingest non-existent step rejected", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"ghost","loopRef":"current"}}]}`
		assertInvalid(t, wrap(body, "s1"), "references non-existent step")
	})
	t.Run("multi_ingest non-existent step rejected", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"multi_ingest","from":[{"stepId":"ghost","loopRef":"current"}]}]}`
		assertInvalid(t, wrap(body, "s1"), "references non-existent step")
	})
	t.Run("ingest existing step valid", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"current"}}]}`
		assertValid(t, wrap(body, "s1"))
	})
}

// --- 7. knobInfo references ------------------------------------------------

func TestValidateKnobInfoReferences(t *testing.T) {
	t.Run("non-existent knob rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"K","type":"knobInfo","from":"nope"}]},` + step("s1")
		assertInvalid(t, wrap(body, "s1"), "non-existent knob")
	})
	t.Run("existing knob valid", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"K","type":"knobInfo","from":"cov"}]},` + step("s1")
		assertValid(t, wrap(body, "s1", withKnobs(`{`+sliderKnob+`}`)))
	})
}

// --- 8. forward references / self references -------------------------------

func TestValidateForwardReferences(t *testing.T) {
	t.Run("forward reference rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s1","loopRef":"current"}}]},` + step("s1")
		assertInvalid(t, wrap(body, "s1"), "forward-references")
	})
	t.Run("backward reference valid", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"current"}}]}`
		assertValid(t, wrap(body, "s1"))
	})
	t.Run("same-group siblings rejected", func(t *testing.T) {
		body := `{"id":"g","name":"G","type":"group","steps":[{"id":"a","name":"A","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"b","loopRef":"current"}}]},` + step("b") + `]},` + step("s1")
		assertInvalid(t, wrap(body, "s1"), "same group")
	})
	t.Run("group child referencing outside-group step valid", func(t *testing.T) {
		body := step("pre") + `,{"id":"g","name":"G","type":"group","steps":[{"id":"a","name":"A","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"pre","loopRef":"current"}}]},` + step("b") + `]},` + step("s1")
		assertValid(t, wrap(body, "s1"))
	})
	t.Run("non-sequential self-reference rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"current"}}]},` + step("s1")
		assertInvalid(t, wrap(body, "s1"), "non-sequential step cannot self-reference")
	})
	t.Run("sequential self-reference valid", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"sequential","nodes":3,"fields":[{"name":"P","type":"ingest","from":{"stepId":"s0","loopRef":"current","nodeRef":"previous"},"skipFirstNode":true}],` + `"systemPrompt":"x"},` + step("s1")
		assertValid(t, wrap(body, "s1"))
	})
}

// --- 9. knob counts --------------------------------------------------------

func TestValidateKnobCounts(t *testing.T) {
	t.Run("two loops knobs rejected", func(t *testing.T) {
		knobs := `{"a":{"name":"A","type":"loops","input":"numerical","default":3,"min":1,"max":10},"b":{"name":"B","type":"loops","input":"numerical","default":3,"min":1,"max":10}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), `type "loops"`)
	})
	t.Run("two recursion knobs rejected", func(t *testing.T) {
		knobs := `{"a":{"name":"A","type":"recursion","input":"numerical","default":2,"min":1,"max":5},"b":{"name":"B","type":"recursion","input":"numerical","default":2,"min":1,"max":5}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), `type "recursion"`)
	})
}

// --- 10. slider & numerical rules ------------------------------------------

func TestValidateSliderRules(t *testing.T) {
	t.Run("no default rejected", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"nodes","input":"slider","steps":[{"title":"A","value":3},{"title":"B","value":6},{"title":"C","value":12}]}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "found none")
	})
	t.Run("multiple defaults rejected", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"nodes","input":"slider","steps":[{"title":"A","value":3,"default":true},{"title":"B","value":6,"default":true},{"title":"C","value":12}]}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "found 2")
	})
	// Go-specific (TS enforces at schema): slider step count in validate.
	t.Run("2 steps rejected", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"nodes","input":"slider","steps":[{"title":"A","value":3,"default":true},{"title":"B","value":6}]}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "3-5 steps")
	})
	t.Run("6 steps rejected", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"nodes","input":"slider","steps":[{"title":"A","value":1,"default":true},{"title":"B","value":2},{"title":"C","value":3},{"title":"D","value":4},{"title":"E","value":5},{"title":"F","value":6}]}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "3-5 steps")
	})
	t.Run("valid slider accepted", func(t *testing.T) {
		assertValid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(`{`+sliderKnob+`}`)))
	})
}

func TestValidateNumericalRules(t *testing.T) {
	t.Run("min > default rejected", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"generic","input":"numerical","default":2,"min":5,"max":10}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "min")
	})
	t.Run("default > max rejected", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"generic","input":"numerical","default":15,"min":1,"max":10}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "default")
	})
	t.Run("valid range accepted", func(t *testing.T) {
		assertValid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(`{`+numericalKnob("K", "generic")+`}`)))
	})
}

// --- 11. group size --------------------------------------------------------

func TestValidateGroupSize(t *testing.T) {
	t.Run("fewer than 2 rejected", func(t *testing.T) {
		body := `{"id":"g","name":"G","type":"group","steps":[` + step("only") + `]},` + step("s1")
		assertInvalid(t, wrap(body, "s1"), "at least 2 steps")
	})
	t.Run("exactly 2 valid", func(t *testing.T) {
		body := `{"id":"g","name":"G","type":"group","steps":[` + step("a") + `,` + step("b") + `]},` + step("s1")
		assertValid(t, wrap(body, "s1"))
	})
}

// --- 12. recursion ---------------------------------------------------------

func TestValidateRecursion(t *testing.T) {
	t.Run("two recursion steps rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","recursion":{"maxDepth":2}},{"id":"b","name":"B","type":"normal","recursion":{"maxDepth":3}},` + step("c")
		assertInvalid(t, wrap(body, "c"), "At most one step can have recursion")
	})
	t.Run("single recursion step valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","recursion":{"maxDepth":2}},` + step("b")
		assertValid(t, wrap(body, "b"))
	})
	t.Run("nodes knob ref missing rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","nodes":"{{knobs.nope}}"},` + step("b")
		assertInvalid(t, wrap(body, "b"), "nodes references non-existent knob")
	})
	t.Run("nodes knob ref existing valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","nodes":"{{knobs.cov}}"},` + step("b")
		assertValid(t, wrap(body, "b", withKnobs(`{`+sliderKnob+`}`)))
	})
	t.Run("maxDepth knob ref missing rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","recursion":{"maxDepth":"{{knobs.nope}}"}},` + step("b")
		assertInvalid(t, wrap(body, "b"), "recursion.maxDepth references non-existent knob")
	})
	t.Run("maxDepth knob ref existing valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","recursion":{"maxDepth":"{{knobs.it}}"}},` + step("b")
		assertValid(t, wrap(body, "b", withKnobs(`{"it":{"name":"It","type":"recursion","input":"numerical","default":2,"min":1,"max":5}}`)))
	})
}

// --- 13. gates -------------------------------------------------------------

func TestValidateGates(t *testing.T) {
	t.Run("empty continueIf rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","nodes":3,"continueIf":"   "},` + step("b")
		assertInvalid(t, wrap(body, "b"), "continueIf must be a non-empty string")
	})
	t.Run("continueIf without nodes rejected", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","continueIf":"1"},` + step("b")
		assertInvalid(t, wrap(body, "b"), "single-node step")
	})
	t.Run("continueIf multi-node normal valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"normal","nodes":5,"continueIf":"1"},` + step("b")
		assertValid(t, wrap(body, "b"))
	})
	t.Run("continueIf sequential multi-node valid", func(t *testing.T) {
		body := `{"id":"a","name":"A","type":"sequential","nodes":3,"continueIf":"1"},` + step("b")
		assertValid(t, wrap(body, "b"))
	})
}

// --- 14. budget limits -----------------------------------------------------

func TestValidateBudgetLimits(t *testing.T) {
	t.Run("loops exceeds 50 rejected", func(t *testing.T) {
		knobs := `{"r":{"name":"R","type":"loops","input":"numerical","default":60,"min":1,"max":100}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "loop count")
	})
	t.Run("nodes exceeds 20 rejected", func(t *testing.T) {
		knobs := `{"n":{"name":"N","type":"nodes","input":"numerical","default":25,"min":1,"max":50}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "node count")
	})
	t.Run("recursion exceeds 5 rejected", func(t *testing.T) {
		knobs := `{"d":{"name":"D","type":"recursion","input":"numerical","default":8,"min":1,"max":10}}`
		assertInvalid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)), "recursion depth")
	})
	t.Run("loops at boundary 50 valid", func(t *testing.T) {
		knobs := `{"r":{"name":"R","type":"loops","input":"numerical","default":50,"min":1,"max":50}}`
		assertValid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)))
	})
}

// --- 15. clone -------------------------------------------------------------

func TestValidateCloneReferences(t *testing.T) {
	t.Run("non-existent step rejected", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":"clone:ghost"}`
		assertInvalid(t, wrap(body, "s1"), "clone references non-existent step")
	})
	t.Run("group step rejected", func(t *testing.T) {
		body := `{"id":"g","name":"G","type":"group","steps":[` + step("a") + `,` + step("b") + `]},` +
			`{"id":"s0","name":"S","type":"normal","fields":[{"name":"Q","type":"text","from":"input.context"}]},` +
			`{"id":"s1","name":"S","type":"normal","fields":"clone:g"}`
		assertInvalid(t, wrap(body, "s1"), "clone references group")
	})
	t.Run("valid clone accepted", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"Q","type":"text","from":"input.context"}]},` +
			`{"id":"s1","name":"S","type":"normal","fields":"clone:s0"}`
		assertValid(t, wrap(body, "s1"))
	})
}

// --- 16. skipFirstNode -----------------------------------------------------

func TestValidateSkipFirstNode(t *testing.T) {
	t.Run("on non-sequential rejected", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"current"},"skipFirstNode":true}]}`
		assertInvalid(t, wrap(body, "s1"), "sequential")
	})
	t.Run("on sequential valid", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"sequential","nodes":3,"fields":[{"name":"F","type":"ingest","from":{"stepId":"s1","loopRef":"current","nodeRef":"previous"},"skipFirstNode":true}]}`
		assertValid(t, wrap(body, "s1"))
	})
}

// --- 17. accumulate on ingest ---------------------------------------------

func TestValidateAccumulateUsage(t *testing.T) {
	t.Run("loopRef accumulate on ingest rejected", func(t *testing.T) {
		body := step("s0") + `,{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"accumulate"}}]}`
		assertInvalid(t, wrap(body, "s1"), `cannot use loopRef: "accumulate"`)
	})
	t.Run("nodeRef accumulate on ingest rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","nodes":3},` + `{"id":"s1","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"current","nodeRef":"accumulate"}}]}`
		assertInvalid(t, wrap(body, "s1"), `cannot use nodeRef: "accumulate"`)
	})
}

// --- 18. pruned nodes.from -------------------------------------------------

func TestValidatePrunedNodesFrom(t *testing.T) {
	t.Run("pruned without continueIf rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","nodes":5},` +
			`{"id":"s1","name":"S","type":"normal","nodes":{"from":{"stepId":"s0","loopRef":"current","pruned":true}}},` + step("s2")
		assertInvalid(t, wrap(body, "s2"), "nodes.from.pruned requires source step")
	})
	t.Run("pruned with continueIf valid", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","nodes":5,"continueIf":"1"},` +
			`{"id":"s1","name":"S","type":"normal","nodes":{"from":{"stepId":"s0","loopRef":"current","pruned":true}}},` + step("s2")
		assertValid(t, wrap(body, "s2"))
	})
}

// --- 19. multiple errors ---------------------------------------------------

func TestValidateMultipleErrors(t *testing.T) {
	body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":"accumulate"}}]},` + step("s0")
	v := validate(t, wrap(body, "ghost"))
	if v.Valid {
		t.Fatal("expected invalid")
	}
	if len(v.Errors) < 2 {
		t.Errorf("expected >=2 errors, got %d: %v", len(v.Errors), v.Errors)
	}
}

// --- Fix #1: invalid knob type rejected by ValidateConfig ------------------

func TestValidateInvalidKnobType(t *testing.T) {
	t.Run("bad type rejected (fix #1)", func(t *testing.T) {
		knobs := `{"k":{"name":"K","type":"BAD","input":"numerical","default":5,"min":1,"max":10}}`
		v := validate(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)))
		if v.Valid {
			t.Fatal("expected invalid for bad knob type")
		}
		if !containsError(v.Errors, "invalid type") {
			t.Errorf("missing invalid-type error: %v", v.Errors)
		}
	})
	t.Run("all four valid types accepted", func(t *testing.T) {
		for _, kt := range []string{"loops", "recursion", "nodes", "generic"} {
			t.Run(kt, func(t *testing.T) {
				// Avoid budget/quantity collisions: use generic base + single knob.
				knobs := `{"k":{"name":"K","type":"` + kt + `","input":"numerical","default":2,"min":1,"max":4}}`
				assertValid(t, wrap(step("s0")+","+step("s1"), "s1", withKnobs(knobs)))
			})
		}
	})
}

// --- Fix #2 defense: validate rejects in-memory bad strategy ----------------

func TestValidateInMemoryBadStrategy(t *testing.T) {
	cfg := mustValidate(t, minimalConfig)
	cfg.AllowedTargets.Strategy = ladr.AllowedTargetsStrategy("MIXED")
	v := ladr.ValidateConfig(cfg)
	if v.Valid {
		t.Fatal("expected validate to reject in-memory bad strategy")
	}
	if !containsError(v.Errors, "strategy") {
		t.Errorf("missing strategy error: %v", v.Errors)
	}
}

// --- Behavior pinning: shared-with-TS gaps (documented, NOT fixed) ---------
// These configs violate the docs but are accepted by BOTH the Go and TS ports
// today. They are pinned here so any future fix is detected.

func TestValidateKnownSharedGaps(t *testing.T) {
	// FIXED: nodeInfo with `from` is now rejected (spec §5.7).
	t.Run("nodeInfo with from rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","fields":[{"name":"N","type":"nodeInfo","from":"x"}]},` + step("s1")
		v := validate(t, wrap(body, "s1"))
		if v.Valid {
			t.Errorf("expected validation error for nodeInfo with from; got valid")
		}
		if !containsStr(v.Errors, "nodeInfo") {
			t.Errorf("expected error mentioning nodeInfo; got %v", v.Errors)
		}
	})
	// FIXED: nodes.from.stepId referencing non-existent step is now rejected (§7.2).
	t.Run("nodes.from ghost stepId rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","nodes":{"from":{"stepId":"ghost","loopRef":"current"}}},` + step("s1")
		v := validate(t, wrap(body, "s1"))
		if v.Valid {
			t.Errorf("expected validation error for nodes.from ghost stepId; got valid")
		}
		if !containsStr(v.Errors, "ghost") {
			t.Errorf("expected error mentioning ghost step; got %v", v.Errors)
		}
	})
	// FIXED: maxDepth:0 is now rejected (spec §6.7).
	t.Run("maxDepth zero rejected", func(t *testing.T) {
		body := `{"id":"s0","name":"S","type":"normal","recursion":{"maxDepth":0}},` + step("s1")
		v := validate(t, wrap(body, "s1"))
		if v.Valid {
			t.Errorf("expected validation error for maxDepth:0; got valid")
		}
		if !containsStr(v.Errors, "maxDepth") {
			t.Errorf("expected error mentioning maxDepth; got %v", v.Errors)
		}
	})
}

func containsStr(slice []string, sub string) bool {
	for _, s := range slice {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
