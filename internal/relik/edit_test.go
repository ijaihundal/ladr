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
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

func loadEditStore(t *testing.T) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	if err := m.Load([]ladr.RelikDecl{{
		Title: "Mem",
		Folders: []ladr.FolderDecl{{
			Name:       "drafts",
			TokenLimit: 1000,
			Files: []ladr.FileDecl{{
				Name: "core.md",
				Init: "alpha\nbeta\ngamma",
			}},
		}},
	}}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestEditFile_ReplaceAnchor(t *testing.T) {
	m := loadEditStore(t)
	before, _ := m.ReadFileMeta("Mem", "drafts", "core.md")

	r, err := m.EditFile("Mem", "drafts", EditFileParams{
		File: "core.md",
		Ops:  []EditOp{{Op: EditOpReplace, Anchor: "beta", Content: "BETA"}},
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if !r.Applied {
		t.Fatalf("expected Applied=true, got false; ops=%v", r.Ops)
	}
	got, _ := m.ReadFile("Mem", "drafts", "core.md")
	if got != "alpha\nBETA\ngamma" {
		t.Fatalf("content after replace = %q", got)
	}
	if r.Version != before.Version+1 {
		t.Fatalf("version should bump: before=%d after=%d", before.Version, r.Version)
	}
	if r.Hash == before.Hash {
		t.Fatalf("hash should change after a content edit")
	}
	if r.LineCount != 3 {
		t.Fatalf("LineCount = %d, want 3", r.LineCount)
	}
	if !strings.Contains(r.FileView, "2\tBETA") {
		t.Fatalf("numbered view missing changed line: %q", r.FileView)
	}
}

func TestEditFile_AtomicRollback(t *testing.T) {
	m := loadEditStore(t)
	orig, _ := m.ReadFile("Mem", "drafts", "core.md")

	// First op is valid; second op references a non-existent anchor → whole
	// batch must roll back, leaving the file byte-for-byte unchanged.
	r, err := m.EditFile("Mem", "drafts", EditFileParams{
		File: "core.md",
		Ops: []EditOp{
			{Op: EditOpReplace, Anchor: "alpha", Content: "ALPHA"},
			{Op: EditOpDelete, Anchor: "does-not-exist"},
		},
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if r.Applied {
		t.Fatalf("expected Applied=false (atomic rollback)")
	}
	got, _ := m.ReadFile("Mem", "drafts", "core.md")
	if got != orig {
		t.Fatalf("file changed despite rollback: %q", got)
	}
	// The valid op should still report its status; the bad one its reason.
	if r.Ops[0].Status != "applied" {
		t.Fatalf("op[0] status = %q, want applied", r.Ops[0].Status)
	}
	if r.Ops[1].Status != "rejected" || r.Ops[1].Reason != "anchor_not_found" {
		t.Fatalf("op[1] = %q / %q, want rejected/anchor_not_found", r.Ops[1].Status, r.Ops[1].Reason)
	}
}

func TestEditFile_AnchorNotUnique(t *testing.T) {
	m := loadEditStore(t)
	// Make "x" appear twice.
	_ = m.WriteFile("Mem", "drafts", "core.md", "x\nx", ladr.WriteOverwrite)

	r, err := m.EditFile("Mem", "drafts", EditFileParams{
		File: "core.md",
		Ops:  []EditOp{{Op: EditOpReplace, Anchor: "x", Content: "y"}},
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if r.Applied {
		t.Fatalf("expected rejection for non-unique anchor")
	}
	if r.Ops[0].Reason != "anchor_not_unique" {
		t.Fatalf("reason = %q, want anchor_not_unique", r.Ops[0].Reason)
	}
}

func TestEditFile_LineOps(t *testing.T) {
	m := loadEditStore(t) // "alpha\nbeta\ngamma"

	// lineReplace line 2.
	r, err := m.EditFile("Mem", "drafts", EditFileParams{
		File: "core.md",
		Ops:  []EditOp{{Op: EditOpLineReplace, Start: 2, End: 2, Content: "BETA"}},
	})
	if err != nil || !r.Applied {
		t.Fatalf("lineReplace failed: %v / applied=%v", err, r.Applied)
	}

	// lineDelete line 1.
	r, err = m.EditFile("Mem", "drafts", EditFileParams{
		File: "core.md",
		Ops:  []EditOp{{Op: EditOpLineDelete, Start: 1, End: 1}},
	})
	if err != nil || !r.Applied {
		t.Fatalf("lineDelete failed: %v / applied=%v", err, r.Applied)
	}

	got, _ := m.ReadFile("Mem", "drafts", "core.md")
	// After replace: alpha\nBETA\ngamma ; after delete line 1: BETA\ngamma
	if got != "BETA\ngamma" {
		t.Fatalf("content = %q, want %q", got, "BETA\ngamma")
	}

	// Out-of-range line op must reject.
	r, err = m.EditFile("Mem", "drafts", EditFileParams{
		File: "core.md",
		Ops:  []EditOp{{Op: EditOpLineDelete, Start: 10, End: 20}},
	})
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if r.Applied || r.Ops[0].Reason != "out_of_range" {
		t.Fatalf("expected out_of_range rejection, got applied=%v reason=%q", r.Applied, r.Ops[0].Reason)
	}
}

func TestEditFile_VersionMonotonic(t *testing.T) {
	m := loadEditStore(t)
	v0, _ := m.ReadFileMeta("Mem", "drafts", "core.md")
	for i := 0; i < 3; i++ {
		r, err := m.EditFile("Mem", "drafts", EditFileParams{
			File: "core.md",
			Ops:  []EditOp{{Op: EditOpInsertAfter, Anchor: "alpha", Content: "x"}},
		})
		if err != nil || !r.Applied {
			t.Fatalf("edit %d: %v / %v", i, err, r.Applied)
		}
		if r.Version != v0.Version+1+i {
			t.Fatalf("edit %d: version = %d, want %d", i, r.Version, v0.Version+1+i)
		}
	}
}

func TestNumberedView(t *testing.T) {
	got := numberedView("a\nb\nc")
	want := "1\ta\n2\tb\n3\tc"
	if got != want {
		t.Fatalf("numberedView = %q, want %q", got, want)
	}
}
