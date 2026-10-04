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

package engine

import (
	"github.com/ladr/ladr/internal/relik"
)

// editCall is the LLM-emitted relikEdit request. It accepts either a batch of
// ops under "ops" or a single flat op (op/anchor/content/start/end) for
// convenience; the flat form is normalized to a one-element ops list by the
// adaptive loop's runRelikEditToolAdaptive dispatch.
type editCall struct {
	File    string          `json:"file"`
	Ops     []relik.EditOp  `json:"ops,omitempty"`
	Op      string          `json:"op,omitempty"`
	Anchor  string          `json:"anchor,omitempty"`
	Content string          `json:"content,omitempty"`
	Start   int             `json:"start,omitempty"`
	End     int             `json:"end,omitempty"`
	// ExpectedVersion quotes the version the model saw (from the last
	// receipt/read). Mismatch = someone wrote first → ToolError feedback,
	// the model re-reads and retries. Optional — omit to blind-write.
	ExpectedVersion *int `json:"expectedVersion,omitempty"`
}
