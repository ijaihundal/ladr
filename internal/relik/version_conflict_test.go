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

package relik

import (
	"errors"
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// The concurrent-editors contract: edits quote the version they saw. The
// later writer gets a clean, informative conflict — no lost update (the
// first writer's bytes are intact), no deadlock, just a retry.

func seedConflictStore(t *testing.T) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	if err := s.Load([]ladr.RelikDecl{{
		Title: "S", Folders: []ladr.FolderDecl{{
			Name: "f", TokenLimit: 500,
			Files: []ladr.FileDecl{{Name: "doc.md", Init: "line one\nline two"}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func op(content string) []EditOp {
	return []EditOp{{Op: EditOpKind("insertAfter"), Anchor: "line one", Content: content}}
}

func TestEditVersionConflict_LaterWriterGetsCleanRetry(t *testing.T) {
	s := seedConflictStore(t)

	v1 := 1 // both editors saw the seeded file at v1
	a, b := v1, v1

	// Editor A applies first — version bumps.
	r1, err := s.EditFile("S", "f", EditFileParams{File: "doc.md", Ops: op("A was here"), ExpectedVersion: &a})
	if err != nil || !r1.Applied {
		t.Fatalf("first edit should apply: %v %v", r1, err)
	}
	if r1.Version != 2 {
		t.Fatalf("version should bump to 2, got %d", r1.Version)
	}

	// Editor B, quoting the same stale version, gets the informative error.
	_, err = s.EditFile("S", "f", EditFileParams{File: "doc.md", Ops: op("B was here"), ExpectedVersion: &b})
	var vc *VersionConflictError
	if !errors.As(err, &vc) {
		t.Fatalf("expected VersionConflictError, got %v", err)
	}
	if vc.Expected != 1 || vc.Actual != 2 {
		t.Errorf("conflict should name both versions, got expected=%d actual=%d", vc.Expected, vc.Actual)
	}

	// No lost update: A's bytes are intact; B never wrote.
	got, _ := s.ReadFile("S", "f", "doc.md")
	if !strings.Contains(got, "A was here") || strings.Contains(got, "B was here") {
		t.Errorf("lost update or ghost write: %q", got)
	}

	// B re-reads (sees v2), retries quoting it — succeeds.
	two := 2
	r2, err := s.EditFile("S", "f", EditFileParams{File: "doc.md", Ops: op("B was here too"), ExpectedVersion: &two})
	if err != nil || !r2.Applied {
		t.Fatalf("retry after re-read should apply: %v", err)
	}
	got2, _ := s.ReadFile("S", "f", "doc.md")
	if !strings.Contains(got2, "A was here") || !strings.Contains(got2, "B was here too") {
		t.Errorf("both writers should survive: %q", got2)
	}
}

func TestEditWithoutExpectedVersion_BlindWriteStillWorks(t *testing.T) {
	s := seedConflictStore(t)
	if _, err := s.EditFile("S", "f", EditFileParams{File: "doc.md", Ops: op("blind")}); err != nil {
		t.Fatalf("blind write (no expectedVersion) must keep working: %v", err)
	}
}
