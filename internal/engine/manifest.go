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

// Package engine — manifest builder.
//
// The manifest is a runtime snapshot of everything readable at the current step.
// It's the input to dynamic step generation — the planner LLM reads the manifest
// to know what sources it can reference in the generated config.
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ladr/ladr/internal/device"
	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// ManifestStep describes a prior step's state at manifest build time.
type ManifestStep struct {
	ID        string `json:"id"`
	Loop      int    `json:"loop"`
	Nodes     int    `json:"nodes"`
	Survivors int    `json:"survivors"`
}

// ManifestRelikFile describes a file in a relik at manifest build time.
type ManifestRelikFile struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

// ManifestRelikFolder describes a folder in a relik.
type ManifestRelikFolder struct {
	Name  string              `json:"name"`
	Files []ManifestRelikFile `json:"files"`
}

// ManifestRelik describes a relik at manifest build time.
type ManifestRelik struct {
	Title   string                `json:"title"`
	Folders []ManifestRelikFolder `json:"folders"`
}

// ManifestTime is the runtime snapshot of time-related inputs.
// Now is always present (UTC ISO 8601). LocalNow and Timezone are present
// only when the caller supplied a timezone at API ingress.
type ManifestTime struct {
	Now      string `json:"now"`
	LocalNow string `json:"localNow,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// ManifestBeacon is the live view of the run owner's device terminals (the
// federated codeExecution surface). Present only when a device is connected
// and answers within the manifest's short budget; nil otherwise (local runs,
// disconnected beacons) — absence means "no terminal visibility", never an
// error. The planner reads it to know what's already running on the device.
type ManifestBeacon struct {
	Connected bool                    `json:"connected"`
	Terminals []device.TerminalStatus `json:"terminals,omitempty"`
}

// Manifest is the runtime snapshot of everything readable at a step.
type Manifest struct {
	Inputs []string           `json:"inputs"`
	Knobs  map[string]float64 `json:"knobs"`
	Steps  []ManifestStep     `json:"steps"`
	Reliks []ManifestRelik    `json:"reliks"`
	Time   *ManifestTime      `json:"time,omitempty"`
	Beacon *ManifestBeacon    `json:"beacon,omitempty"`
}

// TerminalStatusArgs identifies whose terminals are being asked about: the
// run owner's device is resolved by the host from the instance; LadderID
// scopes the question to that ladder's own terminals (device-side ownership).
type TerminalStatusArgs struct {
	InstanceID string
	LadderID   string
}

// TerminalStatusProvider is the optional capability EngineIO implementations
// expose to surface the run owner's live device terminals for the manifest's
// beacon section. Implementations dispatch a terminal "status" action over
// the device channel; local runs simply don't implement it (the manifest then
// omits the section).
type TerminalStatusProvider interface {
	TerminalStatus(ctx context.Context, args TerminalStatusArgs) ([]device.TerminalStatus, error)
}

// manifestBeaconBudget caps the device round-trip for the beacon section —
// the manifest builds on the step's critical path and must never stall on a
// slow or half-open device connection. A var so tests can shrink it.
var manifestBeaconBudget = 2500 * time.Millisecond

// BuildManifest constructs a Manifest from the current runtime state.
//
// Parameters:
//   - config: the ladder config (for step declarations + relik declarations)
//   - runtime: the current execution context (inputs, knobs, loop index)
//   - io: the engine I/O layer (for reading node counts)
//   - store: the relik store (for reading file sizes)
//   - prunedMap: the pruned node map (for survivor counts)
func BuildManifest(
	ctx context.Context,
	config *ladr.PipelineConfig,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
	store relik.Store,
	prunedMap *PrunedNodeMap,
) (*Manifest, error) {
	m := &Manifest{
		Inputs: collectInputKeys(runtime.Inputs),
		Knobs:  runtime.Knobs,
	}

	if config == nil {
		return m, nil // no config → empty manifest (field validation skipped)
	}

	// Build step entries.
	for _, step := range config.Steps {
		if step.Kind == ladr.StepTypeGroup {
			continue // groups aren't directly readable
		}
		base := step.Base()
		if base == nil {
			continue
		}

		ms := ManifestStep{
			ID:   step.ID(),
			Loop: runtime.LoopIndex,
		}

		// Get node count for this step in the current loop.
		count, err := io.GetNodeCount(ctx, GetNodeCountArgs{
			InstanceID: runtime.InstanceID,
			LoopIndex:  runtime.LoopIndex,
			StepID:     step.ID(),
		})
		if err == nil {
			ms.Nodes = count
		}

		// Get survivor count from pruned map.
		key := GetPrunedNodeMapKey(runtime.LoopIndex, step.ID())
		if prunedMap != nil {
			if survivors, ok := prunedMap.Get(key); ok {
				ms.Survivors = len(survivors)
			} else {
				ms.Survivors = ms.Nodes // no pruning = all survived
			}
		} else {
			ms.Survivors = ms.Nodes
		}

		m.Steps = append(m.Steps, ms)
	}

	// Build relik entries.
	for _, relikDecl := range config.Reliks {
		ms := ManifestRelik{Title: relikDecl.Title}
		for _, folderDecl := range relikDecl.Folders {
			mf := ManifestRelikFolder{Name: folderDecl.Name}
			if store != nil {
				idx, err := store.ReadFolderIndex(relikDecl.Title, folderDecl.Name)
				if err == nil && idx != nil {
					for _, f := range idx.Files {
						mf.Files = append(mf.Files, ManifestRelikFile{
							Name: f.Name,
							Size: f.Size,
						})
					}
				}
			}
			ms.Folders = append(ms.Folders, mf)
		}
		m.Reliks = append(m.Reliks, ms)
	}

	// Build time entry from auto-injected input keys.
	m.Time = buildManifestTime(runtime.Inputs)

	// Build the beacon section: the run owner's live device terminals,
	// best-effort with a short budget — the manifest must never stall on a
	// slow device round-trip, and a device that isn't there (local runs,
	// disconnected beacon) just omits the section.
	if sp, ok := io.(TerminalStatusProvider); ok {
		bctx, cancel := context.WithTimeout(ctx, manifestBeaconBudget)
		terms, err := sp.TerminalStatus(bctx, TerminalStatusArgs{
			InstanceID: runtime.InstanceID,
			LadderID:   ladderIdentityOf(config),
		})
		cancel()
		if err == nil {
			m.Beacon = &ManifestBeacon{Connected: true, Terminals: terms}
		}
	}

	return m, nil
}

// buildManifestTime extracts time-related fields from the runtime inputs map.
// Returns nil if no time-related inputs are present (defensive — gateway always
// injects at least input.systemTime).
func buildManifestTime(inputs map[string]any) *ManifestTime {
	now, _ := inputs["input.systemTime"].(string)
	if now == "" {
		return nil
	}
	t := &ManifestTime{Now: now}
	if v, ok := inputs["input.localTime"].(string); ok && v != "" {
		t.LocalNow = v
	}
	if v, ok := inputs["input.timezone"].(string); ok && v != "" {
		t.Timezone = v
	}
	return t
}

// collectInputKeys extracts the keys from the inputs map, EXCLUDING reserved
// internal keys that shouldn't surface to the planner LLM:
//   - "__*" namespace (engine-internal stowage, e.g. __passfields override map)
//   - "input.handoff*" (handoff lineage metadata — useful for trace, not for planner)
//   - "messages" (raw API payload, not a planner-relevant source)
//   - "model", "author", "template", "debugMode" (gateway request metadata)
//
// The keys that remain are the ones the planner can legitimately reference via
// `input.X` in generated configs: input.context, input.systemTime,
// input.localTime, input.timezone, plus any author-declared inputs.
func collectInputKeys(inputs map[string]any) []string {
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		if isInternalInputKey(k) {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

// isInternalInputKey returns true for keys reserved for engine/gateway internal
// state. Excluded from the manifest Inputs list so planner LLMs don't see them.
func isInternalInputKey(k string) bool {
	if strings.HasPrefix(k, "__") {
		return true
	}
	switch k {
	case "messages", "model", "author", "template", "debugMode":
		return true
	case "input.handoffFrom", "input.handoffFromStep", "input.handoffTarget", "input.handoffTargetStep":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Per-view manifest builders
// ---------------------------------------------------------------------------

// BuildManifestView builds a single manifest view and returns a JSON-compatible value.
func BuildManifestView(
	ctx context.Context,
	view ladr.ManifestView,
	config *ladr.PipelineConfig,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (any, error) {
	switch view.Kind {
	case ladr.ManifestViewTime:
		return buildTimeView(runtime), nil
	case ladr.ManifestViewStructure:
		return buildStructureView(config), nil
	case ladr.ManifestViewIndex:
		return buildIndexView(ctx, view, config, runtime)
	case ladr.ManifestViewTree:
		return buildTreeView(ctx, view, config, runtime)
	default:
		return nil, fmt.Errorf("manifest: unknown view kind")
	}
}

// BuildManifestBundle builds multiple views and returns them as an array of
// typed entries: [{ type, data, ... }, ...].
func BuildManifestBundle(
	ctx context.Context,
	views []ladr.ManifestView,
	config *ladr.PipelineConfig,
	runtime *ExecutionRuntimeContext,
	io EngineIO,
) (any, error) {
	var results []any
	for _, view := range views {
		val, err := BuildManifestView(ctx, view, config, runtime, io)
		if err != nil {
			return nil, err
		}
		entry := map[string]any{"data": val}
		switch view.Kind {
		case ladr.ManifestViewTime:
			entry["type"] = "time"
		case ladr.ManifestViewStructure:
			entry["type"] = "structure"
		case ladr.ManifestViewIndex:
			entry["type"] = "index"
			entry["relik"] = view.Relik
			if view.Folder != "" {
				entry["folder"] = view.Folder
			}
		case ladr.ManifestViewTree:
			entry["type"] = "tree"
			entry["relik"] = view.Relik
		}
		results = append(results, entry)
	}
	return results, nil
}

// buildTimeView extracts time-related fields from the runtime inputs map.
func buildTimeView(runtime *ExecutionRuntimeContext) *ManifestTime {
	return buildManifestTime(runtime.Inputs)
}

// buildStructureView returns relik titles + folder names (no files).
func buildStructureView(config *ladr.PipelineConfig) []map[string]any {
	var reliks []map[string]any
	for _, relikDecl := range config.Reliks {
		var folders []string
		for _, folderDecl := range relikDecl.Folders {
			folders = append(folders, folderDecl.Name)
		}
		reliks = append(reliks, map[string]any{
			"title":   relikDecl.Title,
			"folders": folders,
		})
	}
	return reliks
}

// buildIndexView returns a flat file listing for a relik/folder or relik/depth.
func buildIndexView(
	ctx context.Context,
	view ladr.ManifestView,
	config *ladr.PipelineConfig,
	runtime *ExecutionRuntimeContext,
) (any, error) {
	store := runtime.RelikStore
	if store == nil {
		return nil, fmt.Errorf("manifest index: no relik store available")
	}

	if view.Folder != "" {
		idx, err := store.ReadFolderIndex(view.Relik, view.Folder)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"relik":  view.Relik,
			"folder": view.Folder,
			"files":  idx.Files,
		}, nil
	}

	relikDecl := ladr.FindRelik(config.Reliks, view.Relik)
	if relikDecl == nil {
		return nil, fmt.Errorf("manifest index: relik %q not found in config", view.Relik)
	}

	var folders []map[string]any
	for _, folderDecl := range relikDecl.Folders {
		idx, err := store.ReadFolderIndex(view.Relik, folderDecl.Name)
		if err != nil {
			continue
		}
		files := idx.Files
		if view.Depth != nil && len(files) > *view.Depth {
			files = files[:*view.Depth]
		}
		folders = append(folders, map[string]any{
			"folder": folderDecl.Name,
			"files":  files,
		})
	}
	return map[string]any{
		"relik":   view.Relik,
		"folders": folders,
	}, nil
}

// buildTreeView returns a recursive folder tree for a relik.
func buildTreeView(
	ctx context.Context,
	view ladr.ManifestView,
	config *ladr.PipelineConfig,
	runtime *ExecutionRuntimeContext,
) (any, error) {
	store := runtime.RelikStore
	if store == nil {
		return nil, fmt.Errorf("manifest tree: no relik store available")
	}

	relikDecl := ladr.FindRelik(config.Reliks, view.Relik)
	if relikDecl == nil {
		return nil, fmt.Errorf("manifest tree: relik %q not found in config", view.Relik)
	}

	depth := 1
	if view.DepthFull {
		depth = 999
	} else if view.Depth != nil {
		depth = *view.Depth
	}

	var folders []map[string]any
	for _, folderDecl := range relikDecl.Folders {
		tree, err := store.ReadFolderTree(view.Relik, folderDecl.Name, depth)
		if err != nil {
			continue
		}
		folders = append(folders, map[string]any{
			"name":  tree.Name,
			"files": tree.Files,
		})
	}
	return map[string]any{
		"relik":   view.Relik,
		"folders": folders,
	}, nil
}
