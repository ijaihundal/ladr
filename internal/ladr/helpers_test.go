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
	"fmt"
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// mustParse parses JSON via ParseConfig, failing the test on error.
func mustParse(t *testing.T, raw string) *ladr.PipelineConfig {
	t.Helper()
	cfg, err := ladr.ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return cfg
}

// mustParseStored parses JSON via ParseStoredConfig, failing the test on error.
func mustParseStored(t *testing.T, raw string) *ladr.PipelineConfig {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	return cfg
}

// mustValidate parses + validates, failing the test if the config is not valid.
func mustValidate(t *testing.T, raw string) *ladr.PipelineConfig {
	t.Helper()
	cfg := mustParseStored(t, raw)
	v := ladr.ValidateConfig(cfg)
	if !v.Valid {
		t.Fatalf("expected valid config, got errors: %v", v.Errors)
	}
	return cfg
}

// parseErr returns the error from ParseConfig (nil on success).
func parseErr(raw string) error {
	_, err := ladr.ParseConfig([]byte(raw))
	return err
}

// parseStoredErr returns the error from ParseStoredConfig (nil on success).
func parseStoredErr(raw string) error {
	_, err := ladr.ParseStoredConfig([]byte(raw))
	return err
}

// validate parses (stored) + validates and returns the result.
func validate(t *testing.T, raw string) ladr.ValidationResult {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(raw))
	if err != nil {
		t.Fatalf("parse failed (validate helper): %v", err)
	}
	return ladr.ValidateConfig(cfg)
}

// containsError reports whether any error string contains substr.
func containsError(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// errContains reports whether err is non-nil and its message contains substr.
func errContains(err error, substr string) bool {
	return err != nil && strings.Contains(err.Error(), substr)
}

// --- config builders -------------------------------------------------------

type cfgParts struct {
	exit    string
	knobs   string
	allowed string
}

// wrap assembles a complete PipelineConfig JSON from a steps body (the array
// contents) plus an exit id, with optional knobs/allowedTargets overrides.
func wrap(stepsBody, exit string, opts ...func(*cfgParts)) string {
	c := cfgParts{exit: exit, knobs: "{}", allowed: `{"strategy":"universal"}`}
	for _, o := range opts {
		o(&c)
	}
	return fmt.Sprintf(
		`{"name":"T","allowedTargets":%s,"exit":%q,"knobs":%s,"steps":[%s]}`,
		c.allowed, c.exit, c.knobs, stepsBody,
	)
}

func withKnobs(knobs string) func(*cfgParts) {
	return func(c *cfgParts) { c.knobs = knobs }
}

func withAllowed(allowed string) func(*cfgParts) {
	return func(c *cfgParts) { c.allowed = allowed }
}

// step is a bare normal step with no fields/nodes.
func step(id string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"type":"normal"}`, id, id)
}
