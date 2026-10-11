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

// Package ladr — relik declarations, targets, and write policies.
package ladr

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// Metatag
// ---------------------------------------------------------------------------

// MetatagType enumerates the value types a metatag can hold.
type MetatagType string

const (
	MetatagTypeString  MetatagType = "string"
	MetatagTypeStringA MetatagType = "string[]"
	MetatagTypeNumber  MetatagType = "number"
	MetatagTypeBoolean MetatagType = "boolean"
	MetatagTypeObject  MetatagType = "object"
)

// Metatag is a named, typed value attached to a file (or folder-level: all files).
type Metatag struct {
	Name   string          `json:"name"`
	Type   MetatagType     `json:"type"`
	Schema json.RawMessage `json:"schema,omitempty"` // inline JSON Schema, only when type: object
}

// ---------------------------------------------------------------------------
// Folder access + eviction
// ---------------------------------------------------------------------------

// FolderAccess controls read/write permissions on a folder.
type FolderAccess string

const (
	FolderAccessReadWrite FolderAccess = "readWrite"
	FolderAccessRead      FolderAccess = "read"
)

// EvictionPolicy controls what happens when a write would exceed the token limit.
type EvictionPolicy string

const (
	EvictionFIFO   EvictionPolicy = "FIFO"
	EvictionLRU    EvictionPolicy = "LRU"
	EvictionReject EvictionPolicy = "reject"
)

// ---------------------------------------------------------------------------
// File declaration (config-time)
// ---------------------------------------------------------------------------

// FileDecl declares a file inside a folder at config time.
type FileDecl struct {
	Name     string    `json:"name"`
	Init     string    `json:"init,omitempty"` // "blank" or literal content
	Metatags []Metatag `json:"metatags,omitempty"`
}

// ---------------------------------------------------------------------------
// Folder declaration (config-time)
// ---------------------------------------------------------------------------

// FolderDecl declares a folder inside a relik at config time.
type FolderDecl struct {
	Name           string         `json:"name"`
	TokenLimit     int            `json:"tokenLimit"`
	Access         FolderAccess   `json:"access,omitempty"`
	EvictionPolicy EvictionPolicy `json:"evictionPolicy,omitempty"`
	Metatags       []Metatag      `json:"metatags,omitempty"`
	Files          []FileDecl     `json:"files,omitempty"`
}

// ---------------------------------------------------------------------------
// Relik declaration (config-time)
// ---------------------------------------------------------------------------

// RelikDecl declares a persistent memory store at config time.
//
// Mode controls how the config binds the relik:
//   - "declare" (default): the config owns the structure — the engine seeds
//     folders/files on first run (idempotently; existing content is kept).
//   - "reference": the config USES an existing relik by title — it must
//     already exist in the relik instance at run time, or the run fails
//     fast before any LLM call. References declare no structure.
type RelikDecl struct {
	Title   string       `json:"title"`
	Mode    string       `json:"mode,omitempty"` // "" | "declare" | "reference"
	Folders []FolderDecl `json:"folders"`
}

const (
	RelikModeDeclare   = "declare"
	RelikModeReference = "reference"
)

// IsReference reports whether the relik is bound by reference (require-existing)
// rather than declared (auto-init).
func (s RelikDecl) IsReference() bool { return s.Mode == RelikModeReference }



// ---------------------------------------------------------------------------
// RelikTarget — where a write/read goes
// ---------------------------------------------------------------------------

// RelikTarget identifies a location inside a relik.
type RelikTarget struct {
	Relik   string `json:"relik"`
	Folder  string `json:"folder"`
	File    string `json:"file,omitempty"`
	Metatag string `json:"metatag,omitempty"`
}

// HasMetatag returns true if the target points at a metatag value (not file contents).
func (t RelikTarget) HasMetatag() bool { return t.Metatag != "" }

// HasFile returns true if the target points at a specific file.
func (t RelikTarget) HasFile() bool { return t.File != "" }

// ---------------------------------------------------------------------------
// WritePolicy
// ---------------------------------------------------------------------------

// WritePolicy controls how writes interact with existing content.
type WritePolicy string

const (
	WriteAppend     WritePolicy = "append"
	WriteOverwrite  WritePolicy = "overwrite"
	WriteMergeByKey WritePolicy = "mergeByKey"
)

// ---------------------------------------------------------------------------
// Effective schema computation
// ---------------------------------------------------------------------------

// EffectiveMetatags computes the union of folder-level and file-level metatags
// for a specific file. Folder-level metatags apply to all files; file-level
// metatags extend (not override) the folder schema. If a metatag with the same
// name exists at both levels, the file-level declaration wins.
func EffectiveMetatags(folder FolderDecl, file FileDecl) []Metatag {
	byName := make(map[string]Metatag, len(folder.Metatags)+len(file.Metatags))
	order := make([]string, 0, len(folder.Metatags)+len(file.Metatags))

	// Folder-level first (base schema)
	for _, m := range folder.Metatags {
		if _, exists := byName[m.Name]; !exists {
			byName[m.Name] = m
			order = append(order, m.Name)
		}
	}
	// File-level overrides/extends
	for _, m := range file.Metatags {
		if _, exists := byName[m.Name]; !exists {
			order = append(order, m.Name)
		}
		byName[m.Name] = m
	}

	result := make([]Metatag, 0, len(order))
	for _, name := range order {
		result = append(result, byName[name])
	}
	return result
}

// FindFolder returns the folder declaration by name, or nil if not found.
func FindFolder(relik RelikDecl, folderName string) *FolderDecl {
	for i := range relik.Folders {
		if relik.Folders[i].Name == folderName {
			return &relik.Folders[i]
		}
	}
	return nil
}

// FindFile returns the file declaration by name within a folder, or nil.
func FindFile(folder FolderDecl, fileName string) *FileDecl {
	for i := range folder.Files {
		if folder.Files[i].Name == fileName {
			return &folder.Files[i]
		}
	}
	return nil
}

// FindRelik returns the relik declaration by title from a list, or nil.
func FindRelik(reliks []RelikDecl, title string) *RelikDecl {
	for i := range reliks {
		if reliks[i].Title == title {
			return &reliks[i]
		}
	}
	return nil
}

// FindMetatag returns the metatag declaration by name from a list, or nil.
func FindMetatag(metatags []Metatag, name string) *Metatag {
	for i := range metatags {
		if metatags[i].Name == name {
			return &metatags[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// RelikReadField — field type for reading from reliks
// ---------------------------------------------------------------------------

// RelikReadField reads a value from a relik (file contents, metatag, or system metatag).
type RelikReadField struct {
	Name string      `json:"name"`
	Type string      `json:"type"` // always "relikRead"
	From RelikTarget `json:"from"`
	// Depth is only meaningful when From.Metatag == "tree"; controls recursion depth.
	Depth *int `json:"depth,omitempty"`
	// Fallback, if declared, is consulted when the primary From target misses
	// (file empty, metatag unset, etc.). Uses the unified FromSpec vocabulary.
	Fallback *FromSpec `json:"fallback,omitempty"`
}

// MarshalJSON ensures the type field is always set.
func (s RelikReadField) MarshalJSON() ([]byte, error) {
	type alias RelikReadField
	a := alias(s)
	a.Type = "relikRead"
	return json.Marshal(a)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validateMetatagType checks that a metatag type is valid.
func validateMetatagType(t MetatagType) error {
	switch t {
	case MetatagTypeString, MetatagTypeStringA, MetatagTypeNumber, MetatagTypeBoolean, MetatagTypeObject:
		return nil
	default:
		return fmt.Errorf("metatag: invalid type %q", t)
	}
}
