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

// Package ladr — dynamic steps and manifest.
package ladr

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// ManifestView — what a manifest field injects
// ---------------------------------------------------------------------------

// ManifestViewKind discriminates the ManifestView union.
type ManifestViewKind int

const (
	// ManifestViewTime injects the current timestamp(s).
	ManifestViewTime ManifestViewKind = iota
	// ManifestViewStructure injects relik titles + folder names (no files).
	ManifestViewStructure
	// ManifestViewIndex injects a flat file listing for a relik/folder.
	ManifestViewIndex
	// ManifestViewTree injects a recursive folder tree for a relik.
	ManifestViewTree
)

// ManifestView specifies what data a manifest field injects.
//
// JSON forms:
//
//	from: time                                → current timestamp(s)
//	from: structure                           → relik titles + folder names
//	from: { index: { relik, folder } }        → file list for one folder
//	from: { index: { relik, depth: N } }      → file lists across folders, capped
//	from: { tree: { relik, depth: full } }    → full recursive tree
//	from: { tree: { relik, depth: N } }       → recursive tree, depth-limited
type ManifestView struct {
	Kind      ManifestViewKind
	Relik     string // index, tree
	Folder    string // index: specific folder
	Depth     *int   // index: cap files per folder; tree: recursion depth
	DepthFull bool   // tree: depth: "full"
}

// MarshalJSON encodes ManifestView back to its JSON form.
func (v ManifestView) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case ManifestViewTime:
		return json.Marshal("time")
	case ManifestViewStructure:
		return json.Marshal("structure")
	case ManifestViewIndex:
		inner := map[string]any{"relik": v.Relik}
		if v.Folder != "" {
			inner["folder"] = v.Folder
		} else if v.Depth != nil {
			inner["depth"] = *v.Depth
		}
		return json.Marshal(map[string]any{"index": inner})
	case ManifestViewTree:
		inner := map[string]any{"relik": v.Relik}
		if v.DepthFull {
			inner["depth"] = "full"
		} else if v.Depth != nil {
			inner["depth"] = *v.Depth
		}
		return json.Marshal(map[string]any{"tree": inner})
	default:
		return json.Marshal(nil)
	}
}

// UnmarshalJSON dispatches by JSON type and key presence.
func (v *ManifestView) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch s {
		case "time":
			v.Kind = ManifestViewTime
			return nil
		case "structure":
			v.Kind = ManifestViewStructure
			return nil
		default:
			return fmt.Errorf("manifest from: invalid keyword %q", s)
		}
	}

	// Try object.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("manifest from: expected string or object")
	}

	if indexRaw, ok := obj["index"]; ok {
		v.Kind = ManifestViewIndex
		return v.parseIndexTarget(indexRaw)
	}

	if treeRaw, ok := obj["tree"]; ok {
		v.Kind = ManifestViewTree
		return v.parseTreeTarget(treeRaw)
	}

	return fmt.Errorf("manifest from: object must have \"index\" or \"tree\" key")
}

func (v *ManifestView) parseIndexTarget(data []byte) error {
	var target struct {
		Relik  string `json:"relik"`
		Folder string `json:"folder,omitempty"`
		Depth  *int   `json:"depth,omitempty"`
	}
	if err := json.Unmarshal(data, &target); err != nil {
		return err
	}
	if target.Relik == "" {
		return fmt.Errorf("manifest index: \"relik\" is required")
	}
	v.Relik = target.Relik
	v.Folder = target.Folder
	v.Depth = target.Depth
	return nil
}

func (v *ManifestView) parseTreeTarget(data []byte) error {
	var raw struct {
		Relik string          `json:"relik"`
		Depth json.RawMessage `json:"depth,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Relik == "" {
		return fmt.Errorf("manifest tree: \"relik\" is required")
	}
	v.Relik = raw.Relik

	if len(raw.Depth) > 0 {
		var depthStr string
		if json.Unmarshal(raw.Depth, &depthStr) == nil {
			if depthStr == "full" {
				v.DepthFull = true
				return nil
			}
			return fmt.Errorf("manifest tree: depth string must be \"full\", got %q", depthStr)
		}
		var depthNum int
		if json.Unmarshal(raw.Depth, &depthNum) == nil {
			v.Depth = &depthNum
			return nil
		}
		return fmt.Errorf("manifest tree: depth must be \"full\" or a number")
	}
	v.DepthFull = true
	return nil
}

// ---------------------------------------------------------------------------
// ManifestField
// ---------------------------------------------------------------------------

// ManifestField injects a runtime snapshot into the prompt.
//
// If From is set, only the requested view is injected.
// If From is nil and the config declares defaultManifest, the bundle is injected.
// If From is nil and no defaultManifest, the full dump is injected (backward compat).
//
// YAML:
//
//	fields:
//	  - name: Available
//	    type: manifest
//	  - name: Now
//	    type: manifest
//	    from: time
//	  - name: Facts
//	    type: manifest
//	    from: { index: { relik: Memory, folder: facts } }
type ManifestField struct {
	Name string        `json:"name"`
	Type string        `json:"type"` // always "manifest"
	From *ManifestView `json:"from,omitempty"`
}

// MarshalJSON ensures the type field is set.
func (m ManifestField) MarshalJSON() ([]byte, error) {
	type alias ManifestField
	a := alias(m)
	a.Type = "manifest"
	return json.Marshal(a)
}

// DynamicStepConfig is the shape of config loaded at runtime by a dynamic: true step.
// Under Path B, this CAN include control flow (if, jump, recursion) and adaptive keys.
// The only restriction: no nested dynamic: true.
//
// The planner step emits this as JSON output; the dynamic step loads and validates it.
type DynamicStepConfig struct {
	SystemPrompt string         `json:"systemPrompt"`
	Fields       []Field        `json:"fields"`
	Nodes        *int           `json:"nodes,omitempty"`
	If           *If            `json:"if,omitempty"`
	RelikWrite   *RelikWrite    `json:"relikWrite,omitempty"`
	Recursion    *RecursionStep `json:"recursion,omitempty"`

	// Flat adaptive keys (the redesign). Dynamic steps can use them just like
	// static steps. See adaptive.go.
	CodeExecution *CodeExecutionConfig `json:"codeExecution,omitempty"`
	RelikRead     *RelikReadConfig     `json:"relikRead,omitempty"`
	RelikEdit     *RelikEditConfig     `json:"relikEdit,omitempty"`
	Files         *FilesConfig         `json:"files,omitempty"`

	// Model overrides the run's model for this dynamic step.
	Model string `json:"model,omitempty"`
}

// IsEmpty returns true if the config has no meaningful content.
func (d DynamicStepConfig) IsEmpty() bool {
	return d.SystemPrompt == "" && len(d.Fields) == 0 && d.Nodes == nil &&
		d.If == nil && d.RelikWrite == nil && d.Recursion == nil &&
		d.CodeExecution == nil && d.RelikRead == nil && d.RelikEdit == nil &&
		d.Files == nil && d.Model == ""
}
