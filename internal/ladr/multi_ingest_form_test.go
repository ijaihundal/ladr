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

package ladr

import (
	"encoding/json"
	"strings"
	"testing"
)

// A published deliberation ladder carried multi_ingest from: as a single
// object and was unparseable — and the element error was masked as
// "fields: expected string or array". Both the tolerance and the honest
// error wrapping are grammar-level contracts now.
func TestParse_MultiIngestFromObjectForm(t *testing.T) {
	cfg := `{
		"name": "t", "exit": "s",
		"allowedTargets": {"strategy": "universal"},
		"knobs": {},
		"steps": [{
			"id": "s", "name": "S", "type": "normal",
			"fields": [
				{"type": "text", "name": "Q", "from": "input.context"},
				{"type": "multi_ingest", "name": "D",
				 "from": {"stepId": "src", "loopRef": "current", "nodeRef": "accumulate"}}
			]
		}]
	}`
	parsed, err := ParseConfig(json.RawMessage(cfg))
	if err != nil {
		t.Fatalf("object-form multi_ingest from must parse: %v", err)
	}
	f := parsed.Steps[0].Base().Fields.Fields[1]
	if len(f.MultiIngest.From) != 1 || f.MultiIngest.From[0].StepID != "src" {
		t.Fatalf("object form must normalize to a 1-element array, got %+v", f.MultiIngest.From)
	}
}

func TestParse_BadFieldElementReportsItself(t *testing.T) {
	cfg := `{
		"name": "t", "exit": "s",
		"allowedTargets": {"strategy": "universal"},
		"knobs": {},
		"steps": [{
			"id": "s", "name": "S", "type": "normal",
			"fields": [{"type": "no_such_type", "name": "X", "from": "q"}]
		}]
	}`
	_, err := ParseConfig(json.RawMessage(cfg))
	if err == nil {
		t.Fatal("unknown field type must fail parse")
	}
	if strings.Contains(err.Error(), "expected string or array") {
		t.Fatalf("error must name the real cause, not the container shape: %v", err)
	}
}
