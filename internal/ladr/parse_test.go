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
	"testing"
)

const minimalConfig = `{
	"name":"Test Ladder",
	"allowedTargets":{"strategy":"universal"},
	"exit":"step0",
	"knobs":{},
	"steps":[
		{"id":"step0","name":"Answer","type":"normal",
		 "fields":[{"name":"Context","type":"text","from":"input.context"}],
		 "systemPrompt":"Answer clearly."}
	]
}`

func TestParseConfigAccepts(t *testing.T) {
	t.Run("valid minimal config", func(t *testing.T) {
		cfg := mustParse(t, minimalConfig)
		if cfg.Name != "Test Ladder" {
			t.Errorf("name = %q", cfg.Name)
		}
		if cfg.Exit != "step0" {
			t.Errorf("exit = %q", cfg.Exit)
		}
		if len(cfg.Steps) != 1 {
			t.Errorf("steps = %d", len(cfg.Steps))
		}
	})

	t.Run("accepts optional id", func(t *testing.T) {
		cfg := mustParse(t, `{"id":"my-ladder","name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`)
		if cfg.ID != "my-ladder" {
			t.Errorf("id = %q", cfg.ID)
		}
	})

	t.Run("accepts optional description", func(t *testing.T) {
		cfg := mustParse(t, `{"name":"X","description":"d","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`)
		if cfg.Description != "d" {
			t.Errorf("desc = %q", cfg.Description)
		}
	})

	t.Run("accepts constrained allowedTargets", func(t *testing.T) {
		cfg := mustParse(t, `{"name":"X","allowedTargets":{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`)
		if cfg.AllowedTargets.Strategy != "constrained" {
			t.Errorf("strategy = %v", cfg.AllowedTargets.Strategy)
		}
	})
}

func TestParseConfigRejectsMissing(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing name", `{"allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`},
		{"missing exit", `{"name":"X","allowedTargets":{"strategy":"universal"},"knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`},
		{"missing allowedTargets", `{"name":"X","exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`},
		{"missing steps", `{"name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{}}`},
		{"missing knobs", `{"name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","steps":[{"id":"s","name":"S","type":"normal"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := parseErr(c.raw); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestParseConfigStrictUnknownKeys(t *testing.T) {
	for _, key := range []string{"surprise", "foo", "_meta"} {
		t.Run(key, func(t *testing.T) {
			// We inject the unknown key by editing the minimal config's prefix.
			raw := `{"name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}],"` + key + `":1}`
			err := parseErr(raw)
			if err == nil {
				t.Fatal("expected unknown-key error, got nil")
			}
			if !errContains(err, "unknown top-level key") {
				t.Errorf("error does not mention unknown key: %v", err)
			}
		})
	}
}

func TestParseConfigRejectsBadInput(t *testing.T) {
	for _, c := range []struct{ name, raw string }{
		{"null", `null`},
		{"array", `[1,2,3]`},
		{"string", `"not an object"`},
		{"number", `42`},
		{"bool", `true`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := parseErr(c.raw); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestParseConfigRejectsBadStepType(t *testing.T) {
	raw := `{"name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"invalid"}]}`
	err := parseErr(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errContains(err, "unknown type") {
		t.Errorf("error: %v", err)
	}
}

// Fix #2: invalid allowedTargets.strategy rejected at parse.
func TestParseConfigRejectsInvalidStrategy(t *testing.T) {
	raw := `{"name":"X","allowedTargets":{"strategy":"MIXED"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`
	err := parseErr(raw)
	if err == nil {
		t.Fatal("expected parse error for invalid strategy")
	}
	if !errContains(err, "invalid strategy") {
		t.Errorf("error: %v", err)
	}
}

// Fix #3: numerical knob missing min/max rejected at parse.
func TestParseConfigRejectsNumericalMissingBounds(t *testing.T) {
	t.Run("missing min", func(t *testing.T) {
		raw := `{"name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{"k":{"name":"K","type":"generic","input":"numerical","default":5,"max":10}},"steps":[{"id":"s","name":"S","type":"normal"}]}`
		err := parseErr(raw)
		if err == nil {
			t.Fatal("expected parse error for missing min")
		}
		if !errContains(err, "min") {
			t.Errorf("error: %v", err)
		}
	})
	t.Run("missing max", func(t *testing.T) {
		raw := `{"name":"X","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{"k":{"name":"K","type":"generic","input":"numerical","default":5,"min":1}},"steps":[{"id":"s","name":"S","type":"normal"}]}`
		err := parseErr(raw)
		if err == nil {
			t.Fatal("expected parse error for missing max")
		}
		if !errContains(err, "max") {
			t.Errorf("error: %v", err)
		}
	})
}

func TestParseStoredConfigStripsMeta(t *testing.T) {
	t.Run("strips author and version", func(t *testing.T) {
		stored := `{"author":"LADR","version":1,"name":"Test Ladder","allowedTargets":{"strategy":"universal"},"exit":"step0","knobs":{},"steps":[{"id":"step0","name":"A","type":"normal"}]}`
		cfg := mustParseStored(t, stored)
		if cfg.Name != "Test Ladder" {
			t.Errorf("name = %q", cfg.Name)
		}
	})
	t.Run("works without author/version", func(t *testing.T) {
		cfg := mustParseStored(t, minimalConfig)
		if cfg.Name != "Test Ladder" {
			t.Errorf("name = %q", cfg.Name)
		}
	})
	t.Run("preserves id", func(t *testing.T) {
		stored := `{"id":"abc","author":"x","version":1,"name":"N","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{},"steps":[{"id":"s","name":"S","type":"normal"}]}`
		cfg := mustParseStored(t, stored)
		if cfg.ID != "abc" {
			t.Errorf("id = %q", cfg.ID)
		}
	})
	t.Run("rejects invalid after strip", func(t *testing.T) {
		if err := parseStoredErr(`{"author":"x","version":1}`); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("rejects null", func(t *testing.T) {
		if err := parseStoredErr(`null`); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("rejects array", func(t *testing.T) {
		if err := parseStoredErr(`[1,2]`); err == nil {
			t.Error("expected error")
		}
	})
}
