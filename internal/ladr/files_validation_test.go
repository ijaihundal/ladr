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

package ladr_test

// files_validation_test.go — the files: adaptive key's validator rows
// (mirrors the codeExecution/relikRead/relikEdit rows) plus the shared-loop
// agreement across all four adaptive keys.

import (
	"testing"
)

// filesStep builds a normal step declaring a files: key from the given JSON
// body (the object assigned to "files").
func filesStep(filesJSON string) string {
	return `{"id":"s","name":"s","type":"normal","systemPrompt":"x","files":` + filesJSON + `}`
}

const filesValidBody = `{"maxRounds":4,"as":"FileResult","requestMatch":{"type":"object","properties":{"read":{"type":"object"}}}}`

func TestFiles_KeyValid(t *testing.T) {
	raw := wrap(filesStep(filesValidBody), "s")
	assertValid(t, raw)

	cfg := mustValidate(t, raw)
	fc := cfg.Steps[0].Base().Files
	if fc == nil {
		t.Fatal("files key did not parse onto BaseStep.Files")
	}
	if fc.MaxRounds != 4 || fc.As != "FileResult" {
		t.Errorf("parsed files config = %+v", fc)
	}
	if !cfg.Steps[0].Base().HasAdaptiveKeys() {
		t.Error("HasAdaptiveKeys should be true with only files declared")
	}
}

func TestFiles_MaxRoundsRequired(t *testing.T) {
	raw := wrap(filesStep(`{"as":"F","requestMatch":{"type":"object"}}`), "s")
	assertInvalid(t, raw, `files.maxRounds must be > 0`)
}

func TestFiles_AsRequired(t *testing.T) {
	raw := wrap(filesStep(`{"maxRounds":4,"requestMatch":{"type":"object"}}`), "s")
	assertInvalid(t, raw, `files.as must be non-empty`)
}

func TestFiles_RequestMatchRequired(t *testing.T) {
	raw := wrap(filesStep(`{"maxRounds":4,"as":"F"}`), "s")
	assertInvalid(t, raw, `files.requestMatch must be a non-empty JSON Schema`)
}

func TestFiles_SharedLoopAgreementWithCodeExecution(t *testing.T) {
	// files + codeExecution on one step: maxRounds must agree (shared loop).
	// The error names whichever key iterates second, so assert the invariant
	// phrase, not the key order.
	body := `{"id":"s","name":"s","type":"normal","systemPrompt":"x",
		"codeExecution":{"maxRounds":3,"as":"R","requestMatch":{"type":"object"}},
		"files":{"maxRounds":5,"as":"F","requestMatch":{"type":"object"}}}`
	assertInvalid(t, wrap(body, "s"), `adaptive keys share one loop`)

	// Same value on both → valid.
	body = `{"id":"s","name":"s","type":"normal","systemPrompt":"x",
		"codeExecution":{"maxRounds":3,"as":"R","requestMatch":{"type":"object"}},
		"files":{"maxRounds":3,"as":"F","requestMatch":{"type":"object"}}}`
	assertValid(t, wrap(body, "s"))
}

func TestFiles_SharedLoopRequestMatchAgreement(t *testing.T) {
	// requestMatch must also agree when files coexists with another key.
	body := `{"id":"s","name":"s","type":"normal","systemPrompt":"x",
		"files":{"maxRounds":3,"as":"F","requestMatch":{"type":"object","properties":{"read":{"type":"object"}}}},
		"codeExecution":{"maxRounds":3,"as":"R","requestMatch":{"type":"object"}}}`
	assertInvalid(t, wrap(body, "s"), `adaptive keys share one loop`)

	// Identical requestMatch on both → valid.
	body = `{"id":"s","name":"s","type":"normal","systemPrompt":"x",
		"files":{"maxRounds":3,"as":"F","requestMatch":{"type":"object"}},
		"codeExecution":{"maxRounds":3,"as":"R","requestMatch":{"type":"object"}}}`
	assertValid(t, wrap(body, "s"))
}
