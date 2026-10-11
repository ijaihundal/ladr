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

package engine

import "testing"

// Ported from tests/unit/assemble-prompt.test.ts.
func TestAssemblePrompt_ScalarFieldsAndSystemInstruction(t *testing.T) {
	result := AssemblePrompt(
		[]ResolvedField{
			{Name: "Query", Value: "hello"},
			{Name: "Node", Value: "2"},
		},
		"Respond in JSON",
	)

	want := "Query: hello\n\nNode: 2\n\n[System Instruction]\nRespond in JSON"
	if result != want {
		t.Fatalf("\ngot:  %q\nwant: %q", result, want)
	}
}

func TestAssemblePrompt_NumbersMultiIngestValuesInOrder(t *testing.T) {
	result := AssemblePrompt(
		[]ResolvedField{
			{Name: "Draft", Value: []string{"A", "B", "C"}},
			{Name: "Goal", Value: "Ship"},
		},
		"",
	)

	want := "Draft 1: A\n\nDraft 2: B\n\nDraft 3: C\n\nGoal: Ship\n\n[System Instruction]\n"
	if result != want {
		t.Fatalf("\ngot:  %q\nwant: %q", result, want)
	}
}
