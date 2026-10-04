// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Standalone indexer surface: the chunker is pure and always available;
// the content-addressed store behind Search is postgres-backed and lives
// with the server stack. In standalone (CLI) mode indexSearch fields
// resolve to this explicit not-configured error instead of silently
// pretending nothing matched.
package indexer

import (
	"context"
	"errors"
)

// SearchHit mirrors one indexed chunk matched by a query.
type SearchHit struct {
	Hash     string         `json:"hash"`
	Snippet  string         `json:"snippet"`
	Tokens   int            `json:"tokens"`
	TurnID   string         `json:"turn_id"`
	Session  string         `json:"session_id"`
	Kind     string         `json:"kind"`
	Metadata map[string]any `json:"metadata"`
}

var ErrNotConfigured = errors.New("indexer not configured in standalone mode (indexSearch requires the postgres-backed store)")

// Search is grep-grade full-text search over the owner's indexed chunks.
// Standalone builds have no store; callers surface this error as field
// resolution feedback rather than run failure.
func Search(ctx context.Context, owner, query string, limit int) ([]SearchHit, error) {
	return nil, ErrNotConfigured
}
