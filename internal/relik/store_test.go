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
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

func testRelikDecls() []ladr.RelikDecl {
	return []ladr.RelikDecl{
		{
			Title: "Test Memory",
			Folders: []ladr.FolderDecl{
				{
					Name:       "facts",
					TokenLimit: 100,
					Access:     ladr.FolderAccessReadWrite,
					Metatags: []ladr.Metatag{
						{Name: "tags", Type: ladr.MetatagTypeStringA},
						{Name: "confidence", Type: ladr.MetatagTypeNumber},
					},
					Files: []ladr.FileDecl{
						{Name: "core.md", Init: "blank"},
						{Name: "scratch.md", Init: "blank"},
					},
				},
			},
		},
	}
}

func TestMemoryStore_Load(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Load(testRelikDecls()); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !store.HasRelik("Test Memory") {
		t.Error("relik should exist after Load")
	}
	if store.HasRelik("Nonexistent") {
		t.Error("nonexistent relik should not exist")
	}
}

func TestMemoryStore_ReadWriteFile(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	// Read blank file
	content, err := store.ReadFile("Test Memory", "facts", "core.md")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if content != "" {
		t.Errorf("expected blank file, got %q", content)
	}

	// Write to file (append)
	if err := store.WriteFile("Test Memory", "facts", "core.md", "first fact", ladr.WriteAppend); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	content, _ = store.ReadFile("Test Memory", "facts", "core.md")
	if content != "first fact" {
		t.Errorf("expected 'first fact', got %q", content)
	}

	// Append again
	store.WriteFile("Test Memory", "facts", "core.md", "second fact", ladr.WriteAppend)
	content, _ = store.ReadFile("Test Memory", "facts", "core.md")
	if content != "first fact\n\nsecond fact" {
		t.Errorf("expected appended content, got %q", content)
	}

	// Overwrite
	store.WriteFile("Test Memory", "facts", "core.md", "replaced", ladr.WriteOverwrite)
	content, _ = store.ReadFile("Test Memory", "facts", "core.md")
	if content != "replaced" {
		t.Errorf("expected 'replaced', got %q", content)
	}
}

func TestMemoryStore_Metatags(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	// Write a metatag value
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "confidence", 0.95, ladr.WriteOverwrite); err != nil {
		t.Fatalf("WriteMetatag failed: %v", err)
	}

	// Read it back
	val, err := store.ReadMetatag("Test Memory", "facts", "core.md", "confidence")
	if err != nil {
		t.Fatalf("ReadMetatag failed: %v", err)
	}
	if val != 0.95 {
		t.Errorf("expected 0.95, got %v", val)
	}

	// Read across folder (should find it on core.md but not scratch.md)
	entries, err := store.ReadMetatagAcrossFolder("Test Memory", "facts", "confidence")
	if err != nil {
		t.Fatalf("ReadMetatagAcrossFolder failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].File != "core.md" {
		t.Errorf("expected core.md, got %s", entries[0].File)
	}
}

func TestMemoryStore_SystemMetatags(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	// Write some content
	store.WriteFile("Test Memory", "facts", "core.md", "hello world content here", ladr.WriteOverwrite)

	// System metatag: size
	size, err := store.ReadMetatag("Test Memory", "facts", "core.md", "size")
	if err != nil {
		t.Fatalf("ReadMetatag(size) failed: %v", err)
	}
	if size.(int) <= 0 {
		t.Errorf("expected positive size, got %v", size)
	}

	// System metatag: modified
	modified, err := store.ReadMetatag("Test Memory", "facts", "core.md", "modified")
	if err != nil {
		t.Fatalf("ReadMetatag(modified) failed: %v", err)
	}
	if modified == nil {
		t.Error("expected non-nil modified timestamp")
	}

	// System metatag: index
	idx, err := store.ReadFolderIndex("Test Memory", "facts")
	if err != nil {
		t.Fatalf("ReadFolderIndex failed: %v", err)
	}
	if len(idx.Files) != 2 {
		t.Errorf("expected 2 files in index, got %d", len(idx.Files))
	}
}

func TestMemoryStore_TokenLimitEviction(t *testing.T) {
	store := NewMemoryStore()
	// Custom relik with small token limit
	decls := []ladr.RelikDecl{{
		Title: "Small",
		Folders: []ladr.FolderDecl{{
			Name:       "data",
			TokenLimit: 5, // 5 tokens ≈ 20 chars
			Access:     ladr.FolderAccessReadWrite,
			Files:      []ladr.FileDecl{{Name: "log.md", Init: "blank"}},
		}},
	}}
	_ = store.Load(decls)

	// Write content that fits
	store.WriteFile("Small", "data", "log.md", "short", ladr.WriteOverwrite)
	content, _ := store.ReadFile("Small", "data", "log.md")
	if content != "short" {
		t.Errorf("expected 'short', got %q", content)
	}

	// Write content that exceeds limit — should truncate (FIFO)
	longContent := "this is a very long piece of content that definitely exceeds the token limit"
	store.WriteFile("Small", "data", "log.md", longContent, ladr.WriteOverwrite)
	content, _ = store.ReadFile("Small", "data", "log.md")
	// Content should be truncated to fit
	if len(content) > 25 { // 5 tokens * 4 chars/token + some slack
		t.Errorf("content should be truncated, got %d chars: %q", len(content), content)
	}
}

func TestMemoryStore_ReadOnlyFolder(t *testing.T) {
	store := NewMemoryStore()
	decls := []ladr.RelikDecl{{
		Title: "Protected",
		Folders: []ladr.FolderDecl{{
			Name:       "readonly",
			TokenLimit: 100,
			Access:     ladr.FolderAccessRead,
			Files:      []ladr.FileDecl{{Name: "data.md", Init: "initial content"}},
		}},
	}}
	_ = store.Load(decls)

	// Read should work
	content, err := store.ReadFile("Protected", "readonly", "data.md")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if content != "initial content" {
		t.Errorf("expected 'initial content', got %q", content)
	}

	// Write should fail
	err = store.WriteFile("Protected", "readonly", "data.md", "new content", ladr.WriteOverwrite)
	if err == nil {
		t.Error("WriteFile should fail on read-only folder")
	}
}

func TestMemoryStore_NewFileCreation(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	// Write to a file that wasn't declared — should create it
	err := store.WriteFile("Test Memory", "facts", "newfile.md", "new content", ladr.WriteOverwrite)
	if err != nil {
		t.Fatalf("WriteFile to new file failed: %v", err)
	}

	// Read it back
	content, _ := store.ReadFile("Test Memory", "facts", "newfile.md")
	if content != "new content" {
		t.Errorf("expected 'new content', got %q", content)
	}

	// Should appear in folder index
	idx, _ := store.ReadFolderIndex("Test Memory", "facts")
	found := false
	for _, f := range idx.Files {
		if f.Name == "newfile.md" {
			found = true
			break
		}
	}
	if !found {
		t.Error("new file should appear in folder index")
	}
}

func TestMemoryStore_NonexistentTargets(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	// Nonexistent relik
	_, err := store.ReadFile("Nonexistent", "facts", "core.md")
	if err == nil {
		t.Error("should error on nonexistent relik")
	}

	// Nonexistent folder
	_, err = store.ReadFile("Test Memory", "nonexistent", "core.md")
	if err == nil {
		t.Error("should error on nonexistent folder")
	}

	// Nonexistent file
	_, err = store.ReadFile("Test Memory", "facts", "nonexistent.md")
	if err == nil {
		t.Error("should error on nonexistent file")
	}
}

// TestMemoryStore_MetatagAppendPolicy verifies the append policy grows arrays
// and coerces scalars, so repeat writes accumulate rather than replace.
func TestMemoryStore_MetatagAppendPolicy(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	// Append onto nothing → single-element array.
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "tags", "alpha", ladr.WriteAppend); err != nil {
		t.Fatalf("WriteMetatag append #1 failed: %v", err)
	}
	// Append again → two elements.
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "tags", "beta", ladr.WriteAppend); err != nil {
		t.Fatalf("WriteMetatag append #2 failed: %v", err)
	}

	val, err := store.ReadMetatag("Test Memory", "facts", "core.md", "tags")
	if err != nil {
		t.Fatalf("ReadMetatag failed: %v", err)
	}
	arr, ok := val.([]any)
	if !ok {
		t.Fatalf("expected []any after append, got %T: %v", val, val)
	}
	if len(arr) != 2 || arr[0] != "alpha" || arr[1] != "beta" {
		t.Errorf("expected [alpha beta], got %v", arr)
	}

	// Append onto a scalar coerces to [scalar, value].
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "confidence", 0.5, ladr.WriteOverwrite); err != nil {
		t.Fatalf("seed confidence failed: %v", err)
	}
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "confidence", 0.9, ladr.WriteAppend); err != nil {
		t.Fatalf("append onto scalar failed: %v", err)
	}
	val, _ = store.ReadMetatag("Test Memory", "facts", "core.md", "confidence")
	arr, ok = val.([]any)
	if !ok || len(arr) != 2 {
		t.Errorf("expected coerced 2-element array, got %T: %v", val, val)
	}
}

// TestMemoryStore_MetatagMergePolicy verifies object metatags shallow-merge.
func TestMemoryStore_MetatagMergePolicy(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	mergeA := map[string]any{"k1": "v1", "k2": "old"}
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "meta", mergeA, ladr.WriteMergeByKey); err != nil {
		t.Fatalf("merge #1 failed: %v", err)
	}
	mergeB := map[string]any{"k2": "new", "k3": "v3"}
	if err := store.WriteMetatag("Test Memory", "facts", "core.md", "meta", mergeB, ladr.WriteMergeByKey); err != nil {
		t.Fatalf("merge #2 failed: %v", err)
	}

	val, _ := store.ReadMetatag("Test Memory", "facts", "core.md", "meta")
	obj, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T: %v", val, val)
	}
	if obj["k1"] != "v1" || obj["k2"] != "new" || obj["k3"] != "v3" {
		t.Errorf("merge result wrong: %v", obj)
	}
}

// TestMemoryStore_DeleteOperations covers DeleteMetatag, DeleteFile, DeleteRelik.
func TestMemoryStore_DeleteOperations(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	_ = store.WriteMetatag("Test Memory", "facts", "core.md", "tags", "x", ladr.WriteAppend)

	// DeleteMetatag
	if err := store.DeleteMetatag("Test Memory", "facts", "core.md", "tags"); err != nil {
		t.Fatalf("DeleteMetatag failed: %v", err)
	}
	if v, _ := store.ReadMetatag("Test Memory", "facts", "core.md", "tags"); v != nil {
		t.Errorf("metatag should be gone, got %v", v)
	}
	// System metatag delete is a no-op (not an error).
	if err := store.DeleteMetatag("Test Memory", "facts", "core.md", "created"); err != nil {
		t.Errorf("deleting system metatag should be no-op, got %v", err)
	}

	// DeleteFile
	if err := store.DeleteFile("Test Memory", "facts", "scratch.md"); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	if _, err := store.ReadFile("Test Memory", "facts", "scratch.md"); err == nil {
		t.Error("file should be gone after DeleteFile")
	}

	// DeleteRelik
	if err := store.DeleteRelik("Test Memory"); err != nil {
		t.Fatalf("DeleteRelik failed: %v", err)
	}
	if store.HasRelik("Test Memory") {
		t.Error("relik should be gone after DeleteRelik")
	}
}

// TestMemoryStore_ListReliks verifies relik enumeration.
func TestMemoryStore_ListReliks(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Load(testRelikDecls())

	titles, err := store.ListReliks()
	if err != nil {
		t.Fatalf("ListReliks failed: %v", err)
	}
	if len(titles) != 1 || titles[0] != "Test Memory" {
		t.Errorf("expected [Test Memory], got %v", titles)
	}
}

// TestMemoryStore_Load_IsIdempotent verifies the Load contract from the Store
// interface: "Idempotent: declared files are created only if absent — existing
// contents are never wiped, so memory persists across pipeline runs."
//
// Calling Load twice with the same declarations (the typical case when the same
// ladder runs twice on the same store) must NOT error, must NOT wipe existing
// contents, and must NOT duplicate files.
func TestMemoryStore_Load_IsIdempotent(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Load(testRelikDecls()); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	// Write some content into the relik — this represents "memory from a prior run".
	if err := store.WriteFile("Test Memory", "facts", "core.md", "persistent memory", ladr.WriteOverwrite); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	// Load again — must be a no-op (preserve memory, not error).
	if err := store.Load(testRelikDecls()); err != nil {
		t.Fatalf("second Load should be idempotent, got error: %v", err)
	}

	// Memory must persist.
	content, _ := store.ReadFile("Test Memory", "facts", "core.md")
	if content != "persistent memory" {
		t.Errorf("idempotent Load wiped memory: expected 'persistent memory', got %q", content)
	}

	// Index should still list exactly the declared files (no duplicates).
	idx, _ := store.ReadFolderIndex("Test Memory", "facts")
	names := map[string]bool{}
	for _, f := range idx.Files {
		if names[f.Name] {
			t.Errorf("idempotent Load created duplicate file %q", f.Name)
		}
		names[f.Name] = true
	}
	if !names["core.md"] || !names["scratch.md"] {
		t.Errorf("expected core.md + scratch.md after idempotent Load, got %v", names)
	}
}

// TestMemoryStore_EvictionReject verifies that EvictionReject preserves the
// pre-write content and returns an error when the new content would exceed the
// folder's token limit.
//
// Regression: previously, WriteFile mutated f.Contents BEFORE checking the
// eviction policy, so EvictionReject would silently keep the oversized write.
func TestMemoryStore_EvictionReject(t *testing.T) {
	store := NewMemoryStore()
	decls := []ladr.RelikDecl{{
		Title: "Reject",
		Folders: []ladr.FolderDecl{{
			Name:           "data",
			TokenLimit:     5, // 5 tokens ≈ 20 chars
			EvictionPolicy: ladr.EvictionReject,
			Files:          []ladr.FileDecl{{Name: "log.md", Init: "blank"}},
		}},
	}}
	if err := store.Load(decls); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Write content that fits — should succeed.
	if err := store.WriteFile("Reject", "data", "log.md", "short", ladr.WriteOverwrite); err != nil {
		t.Fatalf("small write should succeed: %v", err)
	}

	// Write content that exceeds limit — should error.
	longContent := "this is a very long piece of content that definitely exceeds the limit"
	err := store.WriteFile("Reject", "data", "log.md", longContent, ladr.WriteOverwrite)
	if err == nil {
		t.Fatal("expected error for oversized write with EvictionReject")
	}

	// Original content must be preserved.
	content, _ := store.ReadFile("Reject", "data", "log.md")
	if content != "short" {
		t.Errorf("EvictionReject should preserve old content, got %q", content)
	}

	// Append that exceeds limit — should also error and preserve.
	err = store.WriteFile("Reject", "data", "log.md", " and more appended content here", ladr.WriteAppend)
	if err == nil {
		t.Fatal("expected error for oversized append with EvictionReject")
	}
	content, _ = store.ReadFile("Reject", "data", "log.md")
	if content != "short" {
		t.Errorf("EvictionReject should preserve old content after append, got %q", content)
	}
}

// TestMemoryStore_EvictionFIFO verifies that EvictionFIFO (the default) truncates
// content to fit the token limit rather than rejecting the write.
func TestMemoryStore_EvictionFIFO(t *testing.T) {
	store := NewMemoryStore()
	decls := []ladr.RelikDecl{{
		Title: "FIFO",
		Folders: []ladr.FolderDecl{{
			Name:           "data",
			TokenLimit:     5, // 5 tokens ≈ 20 chars
			EvictionPolicy: ladr.EvictionFIFO,
			Files:          []ladr.FileDecl{{Name: "log.md", Init: "blank"}},
		}},
	}}
	if err := store.Load(decls); err != nil {
		t.Fatalf("Load: %v", err)
	}

	longContent := "this is a very long piece of content that definitely exceeds the limit"
	if err := store.WriteFile("FIFO", "data", "log.md", longContent, ladr.WriteOverwrite); err != nil {
		t.Fatalf("FIFO write should succeed (truncate), got error: %v", err)
	}

	content, _ := store.ReadFile("FIFO", "data", "log.md")
	if len(content) > 25 { // 5 tokens * 4 chars/token + small slack
		t.Errorf("FIFO should truncate content, got %d chars: %q", len(content), content)
	}
	if len(content) == 0 {
		t.Error("FIFO should keep some content after truncation")
	}
}
