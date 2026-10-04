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

// Package relik implements the persistent memory system for LADR ladders.
//
// A relik is composed of folders of plain-text files with structured metatags.
// The store provides read/write operations for file contents and metatags,
// plus system-maintained metatags (index, tree, timestamps, size).
//
// Phase 1 ships an in-process in-memory reference implementation.
// Production deployments use the Redis-backed implementation (hot tier)
// with optional filesystem cold tier (.md files, flushed on step boundary).
package relik

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ladr/ladr/internal/ladr"
)

// ---------------------------------------------------------------------------
// Runtime data model
// ---------------------------------------------------------------------------

// File is the runtime representation of a relik file.
type File struct {
	Name     string
	Contents string
	Metatags map[string]any // user metatags
	Created  time.Time
	Modified time.Time
	Reads    int64  // for LRU eviction
	Version  int    // monotonic, bumped on every content/metadata mutation (D8 provenance)
	Hash     string // content hash (sha256 of contents), recomputed on every write
}

// Folder is the runtime representation of a relik folder.
type Folder struct {
	Name  string
	Files map[string]*File
	Decl  ladr.FolderDecl // config-time declaration (schema, tokenLimit, access, eviction)
}

// Relik is the runtime representation of a relik.
type Relik struct {
	Title   string
	Folders map[string]*Folder
}

// MetatagEntry is a metatag value found across multiple files in a folder.
type MetatagEntry struct {
	File    string
	Metatag string
	Value   any
}

// FolderIndex is the system-maintained index metatag.
type FolderIndex struct {
	Files []FolderIndexEntry
}

// FolderIndexEntry is one entry in the folder index.
type FolderIndexEntry struct {
	Name     string    `json:"name"`
	Size     int       `json:"size"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
}

// FolderTree is the system-maintained tree metatag (recursive).
type FolderTreeNode struct {
	Name     string             `json:"name"`
	Files    []FolderIndexEntry `json:"files,omitempty"`
	Children []FolderTreeNode   `json:"children,omitempty"`
}

// ---------------------------------------------------------------------------
// Surgical editing (tools.relikEdit) — provenance + atomic edit primitives
// ---------------------------------------------------------------------------

// FileMeta is metadata about a file without its contents. Used for provenance
// reads (version/hash) and as the basis for content-free observable reads.
type FileMeta struct {
	Version   int       `json:"version"`
	Hash      string    `json:"hash"`
	LineCount int       `json:"lineCount"`
	Size      int       `json:"size"` // approximate token count
	Created   time.Time `json:"created"`
	Modified  time.Time `json:"modified"`
}

// EditOpKind enumerates the surgical edit operations available to a relikEdit
// tool loop. Anchor ops (replace/insertBefore/insertAfter/delete) locate their
// target by quoting exact existing text — robust to line drift because the LLM
// sees a current numbered view each turn. Line ops (lineReplace/lineDelete/
// lineInsert) address 1-indexed inclusive ranges for the cases where the model
// has a fresh numbered view.
type EditOpKind string

const (
	EditOpReplace      EditOpKind = "replace"      // replace anchor text with content
	EditOpInsertBefore EditOpKind = "insertBefore" // insert content immediately before the anchor
	EditOpInsertAfter  EditOpKind = "insertAfter"  // insert content immediately after the anchor
	EditOpDelete       EditOpKind = "delete"       // delete the anchor text
	EditOpLineReplace  EditOpKind = "lineReplace"  // replace lines [Start,End] (1-indexed, inclusive) with content
	EditOpLineDelete   EditOpKind = "lineDelete"   // delete lines [Start,End]
	EditOpLineInsert   EditOpKind = "lineInsert"   // insert content before line Start
)

// EditOp is one surgical edit against the target file.
type EditOp struct {
	Op      EditOpKind `json:"op"`
	Anchor  string     `json:"anchor,omitempty"`  // anchor ops: exact text to find (must be unique)
	Content string     `json:"content,omitempty"` // replacement / insertion text
	Start   int        `json:"start,omitempty"`   // line ops: start line, 1-indexed
	End     int        `json:"end,omitempty"`     // line ops: end line, inclusive
}

// EditFileParams scopes an atomic edit batch to a single file.
type EditFileParams struct {
	File string   `json:"file"`
	Ops  []EditOp `json:"ops"`
	// ExpectedVersion, when set, is the version the editor saw. Mismatch =
	// someone else wrote first → VersionConflict; re-read and retry. The
	// check is optimistic: deliberate on a snapshot, apply under the
	// row's own version guard — never a lock across LLM deliberation.
	ExpectedVersion *int `json:"expectedVersion,omitempty"`
}

// VersionConflictError is the informative edit failure: the file moved since
// the editor last saw it. The adaptive loop surfaces it as tool feedback;
// the model re-reads and retries.
type VersionConflictError struct {
	File      string
	Expected  int
	Actual    int
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("version conflict on %q: you expected v%d but the file is at v%d (another writer got there first) — re-read the file and retry your edit", e.File, e.Expected, e.Actual)
}

// EditOpStatus is the per-op outcome recorded in an EditReceipt.
type EditOpStatus struct {
	Op     EditOpKind `json:"op"`
	Status string     `json:"status"`           // "applied" | "rejected"
	Reason string     `json:"reason,omitempty"` // why rejected (anchor_not_found, anchor_not_unique, out_of_range, ...)
	Lines  []int      `json:"lines,omitempty"`  // line numbers changed (1-indexed)
}

// EditReceipt is the feedback returned after a surgical edit and injected back
// into the LLM's next turn. It carries a numbered view of the changed file so
// the model always edits against current state.
type EditReceipt struct {
	Applied   bool           `json:"applied"` // true iff every op applied (atomic)
	File      string         `json:"file"`
	Version   int            `json:"version"`   // new version after edit
	Hash      string         `json:"hash"`      // new content hash
	LineCount int            `json:"lineCount"` // lines in the file post-edit
	Ops       []EditOpStatus `json:"ops"`
	FileView  string         `json:"fileView"` // the file with line numbers, post-edit
}

// ---------------------------------------------------------------------------
// Store interface
// ---------------------------------------------------------------------------

// Store is the contract for reading and writing relik data.
type Store interface {
	// Load initializes the store from ladder declarations. Idempotent: declared
	// files are created only if absent — existing contents are never wiped, so
	// memory persists across pipeline runs.
	Load(decls []ladr.RelikDecl) error

	// File operations
	ReadFile(relikTitle, folderName, fileName string) (string, error)
	WriteFile(relikTitle, folderName, fileName string, content string, policy ladr.WritePolicy) error
	DeleteFile(relikTitle, folderName, fileName string) error

	// Metatag operations
	ReadMetatag(relikTitle, folderName, fileName, metatagName string) (any, error)
	ReadMetatagAcrossFolder(relikTitle, folderName, metatagName string) ([]MetatagEntry, error)
	// WriteMetatag sets a metatag value under the given policy:
	//   - overwrite:  replace the value (the historical default)
	//   - append:     array push; a missing or scalar existing value is coerced
	//                 into [existing..., value] so tags grow on repeat writes
	//   - mergeByKey: shallow object merge (existing object ∪ value object); for
	//                 non-object values behaves as overwrite
	WriteMetatag(relikTitle, folderName, fileName, metatagName string, value any, policy ladr.WritePolicy) error
	DeleteMetatag(relikTitle, folderName, fileName, metatagName string) error

	// Whole-relik operations
	DeleteRelik(relikTitle string) error
	// ListReliks returns the titles of every relik that has at least one file
	// in this store's scope (i.e. within this relik instance).
	ListReliks() ([]string, error)

	// System metatags
	ReadFolderIndex(relikTitle, folderName string) (*FolderIndex, error)
	ReadFolderTree(relikTitle, folderName string, depth int) (*FolderTreeNode, error)

	// Flush persists hot state to cold storage (no-op for in-memory).
	Flush() error

	// Provenance + surgical editing (tools.relikEdit)
	ReadFileMeta(relikTitle, folderName, fileName string) (*FileMeta, error)
	EditFile(relikTitle, folderName string, params EditFileParams) (*EditReceipt, error)

	// HasRelik returns true if a relik with the given title exists.
	HasRelik(relikTitle string) bool
}

// ---------------------------------------------------------------------------
// In-memory implementation
// ---------------------------------------------------------------------------

// MemoryStore is the in-process reference implementation of Store.
type MemoryStore struct {
	mu     sync.RWMutex
	reliks map[string]*Relik
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{reliks: make(map[string]*Relik)}
}

// Load initializes the store from ladder declarations.
// Creates the relik/folder structure and initializes declared files.
//
// Idempotent per the Store contract: if a relik/folder/file already exists
// from a prior Load, its contents are preserved (memory persists across
// pipeline runs). Only absent declared files are initialized.
func (m *MemoryStore) Load(decls []ladr.RelikDecl) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, decl := range decls {
		relik, exists := m.reliks[decl.Title]
		if !exists {
			relik = &Relik{
				Title:   decl.Title,
				Folders: make(map[string]*Folder),
			}
			m.reliks[decl.Title] = relik
		}
		for _, fd := range decl.Folders {
			folder, exists := relik.Folders[fd.Name]
			if !exists {
				folder = &Folder{
					Name:  fd.Name,
					Files: make(map[string]*File),
					Decl:  fd,
				}
				relik.Folders[fd.Name] = folder
			} else {
				// Preserve existing folder declaration only if the new one isn't
				// materially different. The Decl carries token/access/eviction
				// policy — keep the freshly-loaded version (config is the source
				// of truth), but never wipe file contents.
				folder.Decl = fd
			}
			now := time.Now().UTC()
			for _, fileDecl := range fd.Files {
				if _, fileExists := folder.Files[fileDecl.Name]; fileExists {
					// File already exists — preserve its contents (idempotent Load).
					continue
				}
				content := ""
				if fileDecl.Init != "" && fileDecl.Init != "blank" {
					content = fileDecl.Init
				}
				f := &File{
					Name:     fileDecl.Name,
					Contents: content,
					Metatags: make(map[string]any),
					Created:  now,
					Modified: now,
					Version:  1,
					Hash:     hashContents(content),
				}
				folder.Files[fileDecl.Name] = f
			}
		}
	}
	return nil
}

// HasRelik returns true if a relik exists.
func (m *MemoryStore) HasRelik(relikTitle string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.reliks[relikTitle]
	return ok
}

// ReadFile returns the contents of a file.
func (m *MemoryStore) ReadFile(relikTitle, folderName, fileName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.getFile(relikTitle, folderName, fileName)
	if err != nil {
		return "", err
	}
	f.Reads++ // track for LRU
	return f.Contents, nil
}

// WriteFile writes content to a file with the given policy.
func (m *MemoryStore) WriteFile(relikTitle, folderName, fileName, content string, policy ladr.WritePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	folder, err := m.getFolder(relikTitle, folderName)
	if err != nil {
		return err
	}

	// Check access policy
	if folder.Decl.Access == ladr.FolderAccessRead {
		return fmt.Errorf("folder %q is read-only", folderName)
	}

	f, exists := folder.Files[fileName]
	if !exists {
		// Create new file if not declared
		now := time.Now().UTC()
		f = &File{
			Name:     fileName,
			Metatags: make(map[string]any),
			Created:  now,
		}
		folder.Files[fileName] = f
	}

	// Save old contents so EvictionReject can restore them if the write
	// would exceed the token limit.
	oldContents := f.Contents

	switch policy {
	case ladr.WriteAppend:
		if f.Contents != "" {
			f.Contents = f.Contents + "\n\n" + content
		} else {
			f.Contents = content
		}
	case ladr.WriteOverwrite:
		f.Contents = content
	case ladr.WriteMergeByKey:
		// mergeByKey is handled at a higher level (needs JSON parsing + key field)
		// This should not be called directly with mergeByKey.
		return fmt.Errorf("mergeByKey must be handled by the write executor, not the store directly")
	default:
		f.Contents = content
	}

	// Enforce token limit + eviction. EvictionReject must restore the
	// pre-write content — without this, f.Contents was already mutated by
	// the switch above and "reject" would silently keep the oversized write.
	if folder.Decl.TokenLimit > 0 && countTokens(f.Contents) > folder.Decl.TokenLimit {
		switch folder.Decl.EvictionPolicy {
		case ladr.EvictionReject:
			f.Contents = oldContents
			return fmt.Errorf("write rejected: content (%d tokens) exceeds folder tokenLimit (%d)",
				countTokens(f.Contents), folder.Decl.TokenLimit)
		case ladr.EvictionFIFO, ladr.EvictionLRU:
			f.Contents = truncateToTokenLimit(f.Contents, folder.Decl.TokenLimit)
		default:
			f.Contents = truncateToTokenLimit(f.Contents, folder.Decl.TokenLimit)
		}
	}

	f.Modified = time.Now().UTC()
	f.bumpVersion()
	return nil
}

// ReadMetatag returns the value of a metatag on a specific file.
func (m *MemoryStore) ReadMetatag(relikTitle, folderName, fileName, metatagName string) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.getFile(relikTitle, folderName, fileName)
	if err != nil {
		return nil, err
	}
	f.Reads++

	// System metatags
	switch metatagName {
	case "created":
		return f.Created, nil
	case "modified":
		return f.Modified, nil
	case "size":
		return countTokens(f.Contents), nil
	}

	// User metatags
	val, ok := f.Metatags[metatagName]
	if !ok {
		return nil, nil
	}
	return val, nil
}

// ReadMetatagAcrossFolder returns all files in a folder that have the given metatag.
func (m *MemoryStore) ReadMetatagAcrossFolder(relikTitle, folderName, metatagName string) ([]MetatagEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	folder, err := m.getFolder(relikTitle, folderName)
	if err != nil {
		return nil, err
	}

	// Snapshot file names and sort so the result is deterministic across runs
	// (Go map iteration order is randomized). The manifest and downstream
	// consumers depend on stable ordering for reproducible prompts/traces.
	names := make([]string, 0, len(folder.Files))
	for name := range folder.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	var entries []MetatagEntry
	for _, name := range names {
		f := folder.Files[name]
		if val, ok := f.Metatags[metatagName]; ok {
			entries = append(entries, MetatagEntry{
				File:    f.Name,
				Metatag: metatagName,
				Value:   val,
			})
		}
	}
	return entries, nil
}

// WriteMetatag sets a metatag value on a file under the given policy.
func (m *MemoryStore) WriteMetatag(relikTitle, folderName, fileName, metatagName string, value any, policy ladr.WritePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.getFile(relikTitle, folderName, fileName)
	if err != nil {
		return err
	}

	f.Metatags[metatagName] = applyMetatagPolicy(f.Metatags[metatagName], value, policy)
	f.Modified = time.Now().UTC()
	f.bumpVersion()
	return nil
}

// DeleteMetatag removes a metatag from a file. System metatags (created/modified/size)
// cannot be deleted and are silently ignored.
func (m *MemoryStore) DeleteMetatag(relikTitle, folderName, fileName, metatagName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.getFile(relikTitle, folderName, fileName)
	if err != nil {
		return err
	}
	if isSystemMetatag(metatagName) {
		return nil
	}
	delete(f.Metatags, metatagName)
	f.Modified = time.Now().UTC()
	return nil
}

// DeleteFile removes a file from a folder.
func (m *MemoryStore) DeleteFile(relikTitle, folderName, fileName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	folder, err := m.getFolder(relikTitle, folderName)
	if err != nil {
		return err
	}
	if _, ok := folder.Files[fileName]; !ok {
		return fmt.Errorf("file %q not found in folder %q", fileName, folderName)
	}
	delete(folder.Files, fileName)
	return nil
}

// DeleteRelik removes an entire relik (all its folders and files).
func (m *MemoryStore) DeleteRelik(relikTitle string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.reliks[relikTitle]; !ok {
		return fmt.Errorf("relik %q not found", relikTitle)
	}
	delete(m.reliks, relikTitle)
	return nil
}

// ListReliks returns the titles of all reliks in this store.
func (m *MemoryStore) ListReliks() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	titles := make([]string, 0, len(m.reliks))
	for title := range m.reliks {
		titles = append(titles, title)
	}
	sort.Strings(titles)
	return titles, nil
}

// applyMetatagPolicy combines an existing metatag value with a new one per policy.
func applyMetatagPolicy(existing, incoming any, policy ladr.WritePolicy) any {
	switch policy {
	case ladr.WriteAppend:
		var arr []any
		switch e := existing.(type) {
		case nil:
			arr = []any{}
		case []any:
			arr = e
		case []string:
			arr = make([]any, 0, len(e)+1)
			for _, s := range e {
				arr = append(arr, s)
			}
		default:
			// Coerce a scalar into a single-element array so repeat appends grow.
			arr = []any{e}
		}
		// If incoming is itself an array, extend; otherwise push the single value.
		if inc, ok := incoming.([]any); ok {
			return append(arr, inc...)
		}
		return append(arr, incoming)

	case ladr.WriteMergeByKey:
		existingMap, ok1 := existing.(map[string]any)
		incMap, ok2 := incoming.(map[string]any)
		if !ok1 || !ok2 {
			// Non-object values can't merge — fall back to overwrite.
			return incoming
		}
		merged := make(map[string]any, len(existingMap)+len(incMap))
		for k, v := range existingMap {
			merged[k] = v
		}
		for k, v := range incMap {
			merged[k] = v
		}
		return merged

	case ladr.WriteOverwrite:
		fallthrough
	default:
		return incoming
	}
}

// isSystemMetatag reports whether a metatag name is engine-maintained (read-only).
func isSystemMetatag(name string) bool {
	switch name {
	case "index", "tree", "created", "modified", "size":
		return true
	}
	return false
}

// ReadFolderIndex returns the system-maintained folder index.
func (m *MemoryStore) ReadFolderIndex(relikTitle, folderName string) (*FolderIndex, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	folder, err := m.getFolder(relikTitle, folderName)
	if err != nil {
		return nil, err
	}

	// Snapshot file names and sort so the index is deterministic across runs
	// (Go map iteration order is randomized). Index/tree metatags surface to
	// LLM prompts, so stable ordering is required for reproducible traces.
	names := make([]string, 0, len(folder.Files))
	for name := range folder.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	idx := &FolderIndex{Files: make([]FolderIndexEntry, 0, len(names))}
	for _, name := range names {
		f := folder.Files[name]
		idx.Files = append(idx.Files, FolderIndexEntry{
			Name:     f.Name,
			Size:     countTokens(f.Contents),
			Created:  f.Created,
			Modified: f.Modified,
		})
	}
	return idx, nil
}

// ReadFolderTree returns the recursive folder tree (system metatag "tree").
func (m *MemoryStore) ReadFolderTree(relikTitle, folderName string, depth int) (*FolderTreeNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	folder, err := m.getFolder(relikTitle, folderName)
	if err != nil {
		return nil, err
	}

	// Snapshot file names and sort for determinism (see ReadFolderIndex).
	names := make([]string, 0, len(folder.Files))
	for name := range folder.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	node := &FolderTreeNode{Name: folder.Name, Files: make([]FolderIndexEntry, 0, len(names))}
	for _, name := range names {
		f := folder.Files[name]
		node.Files = append(node.Files, FolderIndexEntry{
			Name:     f.Name,
			Size:     countTokens(f.Contents),
			Created:  f.Created,
			Modified: f.Modified,
		})
	}

	// Note: subfolders are Phase 2 (the current folder model is flat).
	// depth > 1 is accepted but has no subfolders to recurse into yet.
	_ = depth

	return node, nil
}

// Flush is a no-op for in-memory store.
func (m *MemoryStore) Flush() error { return nil }

// ReadFileMeta returns a file's metadata (version/hash/lineCount/size) without
// its contents — the provenance read primitive (D8).
func (m *MemoryStore) ReadFileMeta(relikTitle, folderName, fileName string) (*FileMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	f, err := m.getFile(relikTitle, folderName, fileName)
	if err != nil {
		return nil, err
	}
	return &FileMeta{
		Version:   f.Version,
		Hash:      f.Hash,
		LineCount: lineCountOf(f.Contents),
		Size:      countTokens(f.Contents),
		Created:   f.Created,
		Modified:  f.Modified,
	}, nil
}

// EditFile applies a batch of edit ops atomically to one file. Ops are validated
// and applied against a working copy in sequence; the moment one fails the
// whole batch rolls back (file unchanged, Applied=false). On success the file's
// contents, version, and hash are updated and a numbered view is returned so the
// LLM's next edit targets current state.
func (m *MemoryStore) EditFile(relikTitle, folderName string, params EditFileParams) (*EditReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.getFile(relikTitle, folderName, params.File)
	if err != nil {
		return nil, err
	}
	if params.ExpectedVersion != nil && *params.ExpectedVersion != f.Version {
		return nil, &VersionConflictError{File: params.File, Expected: *params.ExpectedVersion, Actual: f.Version}
	}

	working, opStatuses, allApplied := ApplyEditOps(f.Contents, params.Ops)

	if allApplied && len(params.Ops) > 0 {
		f.Contents = working
		f.Modified = time.Now().UTC()
		f.bumpVersion()
	}

	return &EditReceipt{
		Applied:   allApplied && len(params.Ops) > 0,
		File:      params.File,
		Version:   f.Version,
		Hash:      f.Hash,
		LineCount: lineCountOf(f.Contents),
		Ops:       opStatuses,
		FileView:  numberedView(f.Contents),
	}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (m *MemoryStore) getRelik(title string) (*Relik, error) {
	s, ok := m.reliks[title]
	if !ok {
		return nil, fmt.Errorf("relik %q not found", title)
	}
	return s, nil
}

func (m *MemoryStore) getFolder(relikTitle, folderName string) (*Folder, error) {
	s, err := m.getRelik(relikTitle)
	if err != nil {
		return nil, err
	}
	f, ok := s.Folders[folderName]
	if !ok {
		return nil, fmt.Errorf("folder %q not found in relik %q", folderName, relikTitle)
	}
	return f, nil
}

func (m *MemoryStore) getFile(relikTitle, folderName, fileName string) (*File, error) {
	folder, err := m.getFolder(relikTitle, folderName)
	if err != nil {
		return nil, err
	}
	f, ok := folder.Files[fileName]
	if !ok {
		return nil, fmt.Errorf("file %q not found in folder %q", fileName, folderName)
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// Token counting (rough approximation)
// ---------------------------------------------------------------------------

// countTokens returns an approximate token count for a string.
// Phase 1 uses chars/4 as a rough proxy. Phase 2 will use a real tokenizer.
func countTokens(s string) int {
	// Rough approximation: 1 token ≈ 4 characters of English text.
	// This is intentionally simple — production should use tiktoken or similar.
	return len(s) / 4
}

// truncateToTokenLimit keeps the tail of the content to fit within the token limit.
func truncateToTokenLimit(content string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}
	// Keep the tail (most recent content).
	start := len(content) - maxChars
	// Try to start at a newline boundary for clean cuts.
	if idx := strings.IndexByte(content[start:], '\n'); idx >= 0 {
		start = start + idx + 1
	}
	return content[start:]
}

// ---------------------------------------------------------------------------
// Surgical-edit helpers (tools.relikEdit)
// ---------------------------------------------------------------------------

// hashContents returns the sha256 hex digest of a file's contents. Used for the
// Version/Hash provenance pair (D8) — observability, not locking.
func hashContents(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// bumpVersion recomputes the file's hash from its (already-mutated) contents
// and increments the monotonic version. Called on every content/metadata write.
func (f *File) bumpVersion() {
	f.Version++
	f.Hash = hashContents(f.Contents)
}

// lineCountOf returns the number of lines in s (split on "\n"). Empty string = 0.
func lineCountOf(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// numberedView renders file contents as "<n>\t<line>" per line — the cat -n /
// grep -n format models see constantly — so the LLM always edits against a
// current numbered view.
func numberedView(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d\t%s", i+1, ln)
	}
	return b.String()
}

// applyEditOps applies a batch of ops to content sequentially (each op sees the
// result of the previous). Atomic: the first failure stops evaluation and the
// returned content is the working copy up to (but not including) the failing
// op. Returns the resulting content, per-op statuses, and whether every op
// applied. Used by both MemoryStore and PgStore so edit semantics never drift.
// ApplyEditOps applies anchor/line ops to text; shared by relikEdit and
// configEdit (the same surgical-edit vocabulary, two targets).
func ApplyEditOps(content string, ops []EditOp) (string, []EditOpStatus, bool) {
	working := content
	statuses := make([]EditOpStatus, len(ops))
	allApplied := true
	for i, op := range ops {
		newContent, lines, rerr := applyEditOp(working, op)
		if rerr != nil {
			statuses[i] = EditOpStatus{Op: op.Op, Status: "rejected", Reason: rerr.Error()}
			allApplied = false
			for j := i + 1; j < len(ops); j++ {
				statuses[j] = EditOpStatus{Op: ops[j].Op, Status: "rejected", Reason: "not_reached"}
			}
			break
		}
		statuses[i] = EditOpStatus{Op: op.Op, Status: "applied", Lines: lines}
		working = newContent
	}
	return working, statuses, allApplied
}

// applyEditOp applies one edit op to content, returning the new content and the
// 1-indexed line numbers it changed. Returns a reason string as an error on
// failure (anchor_not_found, anchor_not_unique, anchor_required, out_of_range,
// unknown_op).
func applyEditOp(content string, op EditOp) (string, []int, error) {
	switch op.Op {
	case EditOpReplace, EditOpInsertBefore, EditOpInsertAfter, EditOpDelete:
		if op.Anchor == "" {
			return "", nil, fmt.Errorf("anchor_required")
		}
		occurrences := strings.Count(content, op.Anchor)
		if occurrences == 0 {
			return "", nil, fmt.Errorf("anchor_not_found")
		}
		if occurrences > 1 {
			return "", nil, fmt.Errorf("anchor_not_unique")
		}
		idx := strings.Index(content, op.Anchor)
		startLine := 1 + strings.Count(content[:idx], "\n")

		var newContent string
		var endLine int
		switch op.Op {
		case EditOpReplace:
			newContent = content[:idx] + op.Content + content[idx+len(op.Anchor):]
			endLine = startLine + lineCountOf(op.Content) - 1
		case EditOpInsertBefore:
			newContent = content[:idx] + op.Content + content[idx:]
			endLine = startLine + lineCountOf(op.Content) - 1
		case EditOpInsertAfter:
			after := idx + len(op.Anchor)
			newContent = content[:after] + op.Content + content[after:]
			endLine = startLine + lineCountOf(op.Content)
		case EditOpDelete:
			newContent = content[:idx] + content[idx+len(op.Anchor):]
			endLine = startLine
		}
		if endLine < startLine {
			endLine = startLine
		}
		lines := make([]int, 0, endLine-startLine+1)
		for ln := startLine; ln <= endLine; ln++ {
			lines = append(lines, ln)
		}
		return newContent, lines, nil

	case EditOpLineReplace, EditOpLineDelete, EditOpLineInsert:
		lines := strings.Split(content, "\n")
		n := len(lines)
		var newLines []string
		var changed []int
		switch op.Op {
		case EditOpLineInsert:
			if op.Start < 1 || op.Start > n+1 {
				return "", nil, fmt.Errorf("out_of_range")
			}
			inserted := strings.Split(op.Content, "\n")
			newLines = append(newLines, lines[:op.Start-1]...)
			newLines = append(newLines, inserted...)
			newLines = append(newLines, lines[op.Start-1:]...)
			for i := 0; i < len(inserted); i++ {
				changed = append(changed, op.Start+i)
			}
		case EditOpLineReplace:
			if op.Start < 1 || op.End < op.Start || op.End > n {
				return "", nil, fmt.Errorf("out_of_range")
			}
			replacement := strings.Split(op.Content, "\n")
			newLines = append(newLines, lines[:op.Start-1]...)
			newLines = append(newLines, replacement...)
			newLines = append(newLines, lines[op.End:]...)
			endL := op.Start + len(replacement) - 1
			if endL < op.Start {
				endL = op.Start
			}
			for ln := op.Start; ln <= endL; ln++ {
				changed = append(changed, ln)
			}
		case EditOpLineDelete:
			if op.Start < 1 || op.End < op.Start || op.End > n {
				return "", nil, fmt.Errorf("out_of_range")
			}
			newLines = append(newLines, lines[:op.Start-1]...)
			newLines = append(newLines, lines[op.End:]...)
			for ln := op.Start; ln <= op.End; ln++ {
				changed = append(changed, ln)
			}
		}
		return strings.Join(newLines, "\n"), changed, nil
	}
	return "", nil, fmt.Errorf("unknown_op")
}
