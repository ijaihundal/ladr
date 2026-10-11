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

import (
	"fmt"
	"testing"
)

// Relik binding modes: declare (default, owns structure) vs reference
// (require-existing at run time, declares nothing).

func reliksConfig(reliks string) string {
	return fmt.Sprintf(`{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"go","knobs":{},"steps":[{"id":"go","name":"go","type":"normal","systemPrompt":"x"}],"reliks":%s}`, reliks)
}

func TestRelikModeDeclareDefaultValid(t *testing.T) {
	assertValid(t, reliksConfig(`[
		{"title": "M", "folders": [{"name": "facts", "tokenLimit": 100, "files": [{"name": "core.md", "init": "blank"}]}]}
	]`))
}

func TestRelikModeExplicitDeclareValid(t *testing.T) {
	assertValid(t, reliksConfig(`[
		{"title": "M", "mode": "declare", "folders": [{"name": "facts", "tokenLimit": 100}]}
	]`))
}

func TestRelikModeReferenceWithoutFoldersValid(t *testing.T) {
	assertValid(t, reliksConfig(`[{"title": "shared", "mode": "reference"}]`))
}

func TestRelikModeReferenceWithFoldersInvalid(t *testing.T) {
	assertInvalid(t, reliksConfig(`[
		{"title": "shared", "mode": "reference", "folders": [{"name": "facts", "tokenLimit": 100}]}
	]`), `mode "reference" must not declare folders`)
}

func TestRelikModeUnknownInvalid(t *testing.T) {
	assertInvalid(t, reliksConfig(`[
		{"title": "M", "mode": "borrow", "folders": [{"name": "facts", "tokenLimit": 100}]}
	]`), `invalid mode "borrow"`)
}


// --- indexer config + indexSearch validation ---------------------------------

func indexerConfig(indexer string, fields string) string {
	return fmt.Sprintf(`{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"go","knobs":{},`+
		`"steps":[{"id":"go","name":"go","type":"normal","systemPrompt":"x","fields":[%s]}],`+
		`"indexer":%s}`, fields, indexer)
}

func TestIndexSearchFieldValid(t *testing.T) {
	assertValid(t, indexerConfig(`{"enabled":true}`, `{"name":"find","type":"indexSearch","query":"needle"}`))
}

func TestIndexSearchFieldRequiresQuery(t *testing.T) {
	assertInvalid(t, indexerConfig(`{"enabled":true}`, `{"name":"find","type":"indexSearch"}`), `requires a query`)
}

func TestIndexerScopeInvalid(t *testing.T) {
	assertInvalid(t, indexerConfig(`{"enabled":true,"scope":"global"}`, `{"name":"find","type":"indexSearch","query":"x"}`), `invalid scope "global"`)
}
