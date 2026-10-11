package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

func testManifestConfig() *ladr.PipelineConfig {
	return &ladr.PipelineConfig{
		Name:           "test",
		Exit:           "answer",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{},
		Reliks: []ladr.RelikDecl{
			{
				Title: "Memory",
				Folders: []ladr.FolderDecl{
					{Name: "facts", TokenLimit: 1000, Files: []ladr.FileDecl{
						{Name: "core.md", Init: "core content"},
						{Name: "glossary.md", Init: "glossary content"},
					}},
					{Name: "trace", TokenLimit: 1000},
				},
			},
		},
	}
}

func TestBuildManifestView_Time(t *testing.T) {
	config := testManifestConfig()
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			"input.systemTime": "2026-07-26T12:00:00Z",
			"input.localTime":  "2026-07-26T08:00:00-04:00",
			"input.timezone":   "America/New_York",
		},
		Config: config,
	}

	result, err := BuildManifestView(context.Background(), ladr.ManifestView{Kind: ladr.ManifestViewTime}, config, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(*ManifestTime)
	if m.Now != "2026-07-26T12:00:00Z" {
		t.Errorf("expected Now=2026-07-26T12:00:00Z, got %q", m.Now)
	}
	if m.LocalNow != "2026-07-26T08:00:00-04:00" {
		t.Errorf("expected LocalNow, got %q", m.LocalNow)
	}
	if m.Timezone != "America/New_York" {
		t.Errorf("expected Timezone=America/New_York, got %q", m.Timezone)
	}
}

func TestBuildManifestView_Structure(t *testing.T) {
	config := testManifestConfig()
	runtime := &ExecutionRuntimeContext{Config: config}

	result, err := BuildManifestView(context.Background(), ladr.ManifestView{Kind: ladr.ManifestViewStructure}, config, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	reliks := result.([]map[string]any)
	if len(reliks) != 1 {
		t.Fatalf("expected 1 relik, got %d", len(reliks))
	}
	if reliks[0]["title"] != "Memory" {
		t.Errorf("expected title=Memory, got %v", reliks[0]["title"])
	}
	folders := reliks[0]["folders"].([]string)
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders, got %d", len(folders))
	}
	if folders[0] != "facts" || folders[1] != "trace" {
		t.Errorf("expected [facts, trace], got %v", folders)
	}
}

func TestBuildManifestView_Index_Folder(t *testing.T) {
	config := testManifestConfig()
	store := relik.NewMemoryStore()
	store.Load(config.Reliks)
	runtime := &ExecutionRuntimeContext{Config: config, RelikStore: store}

	view := ladr.ManifestView{Kind: ladr.ManifestViewIndex, Relik: "Memory", Folder: "facts"}
	result, err := BuildManifestView(context.Background(), view, config, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	if m["relik"] != "Memory" {
		t.Errorf("expected relik=Memory, got %v", m["relik"])
	}
	if m["folder"] != "facts" {
		t.Errorf("expected folder=facts, got %v", m["folder"])
	}
	files := m["files"].([]relik.FolderIndexEntry)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Name != "core.md" {
		t.Errorf("expected first file=core.md, got %q", files[0].Name)
	}
}

func TestBuildManifestView_Index_AllFolders(t *testing.T) {
	config := testManifestConfig()
	store := relik.NewMemoryStore()
	store.Load(config.Reliks)
	runtime := &ExecutionRuntimeContext{Config: config, RelikStore: store}

	view := ladr.ManifestView{Kind: ladr.ManifestViewIndex, Relik: "Memory"}
	result, err := BuildManifestView(context.Background(), view, config, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	folders := m["folders"].([]map[string]any)
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders, got %d", len(folders))
	}
}

func TestBuildManifestView_Tree(t *testing.T) {
	config := testManifestConfig()
	store := relik.NewMemoryStore()
	store.Load(config.Reliks)
	runtime := &ExecutionRuntimeContext{Config: config, RelikStore: store}

	view := ladr.ManifestView{Kind: ladr.ManifestViewTree, Relik: "Memory", DepthFull: true}
	result, err := BuildManifestView(context.Background(), view, config, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	m := result.(map[string]any)
	if m["relik"] != "Memory" {
		t.Errorf("expected relik=Memory, got %v", m["relik"])
	}
	folders := m["folders"].([]map[string]any)
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders in tree, got %d", len(folders))
	}
}

func TestResolveField_Manifest_FromTime(t *testing.T) {
	config := testManifestConfig()
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{
			"input.systemTime": "2026-07-26T12:00:00Z",
		},
		Config: config,
	}

	field := ladr.Field{
		Kind: ladr.FieldManifest,
		Manifest: ladr.ManifestField{
			Name: "Now",
			Type: "manifest",
			From: &ladr.ManifestView{Kind: ladr.ManifestViewTime},
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	s, ok := val.(string)
	if !ok {
		t.Fatalf("expected string, got %T", val)
	}

	var decoded map[string]any
	json.Unmarshal([]byte(s), &decoded)
	if decoded["now"] != "2026-07-26T12:00:00Z" {
		t.Errorf("expected now=2026-07-26T12:00:00Z, got %v", decoded["now"])
	}
}

func TestResolveField_Manifest_DefaultBundle(t *testing.T) {
	config := testManifestConfig()
	config.DefaultManifest = []ladr.ManifestView{
		{Kind: ladr.ManifestViewTime},
		{Kind: ladr.ManifestViewStructure},
	}

	store := relik.NewMemoryStore()
	store.Load(config.Reliks)
	runtime := &ExecutionRuntimeContext{
		Inputs:     map[string]any{"input.systemTime": "2026-07-26T12:00:00Z"},
		Config:     config,
		RelikStore: store,
	}

	field := ladr.Field{
		Kind: ladr.FieldManifest,
		Manifest: ladr.ManifestField{
			Name: "Available",
			Type: "manifest",
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	s, ok := val.(string)
	if !ok {
		t.Fatalf("expected string, got %T", val)
	}

	var bundle []any
	json.Unmarshal([]byte(s), &bundle)
	if len(bundle) != 2 {
		t.Fatalf("expected 2 entries in bundle, got %d", len(bundle))
	}

	first := bundle[0].(map[string]any)
	if first["type"] != "time" {
		t.Errorf("expected first entry type=time, got %v", first["type"])
	}

	second := bundle[1].(map[string]any)
	if second["type"] != "structure" {
		t.Errorf("expected second entry type=structure, got %v", second["type"])
	}
}

func TestResolveField_Manifest_FullDump(t *testing.T) {
	config := testManifestConfig()
	runtime := &ExecutionRuntimeContext{
		Inputs: map[string]any{"input.context": "hello"},
		Knobs:  map[string]float64{"branches": 3},
		Config: config,
	}

	field := ladr.Field{
		Kind: ladr.FieldManifest,
		Manifest: ladr.ManifestField{
			Name: "Available",
			Type: "manifest",
		},
	}

	val, err := ResolveField(context.Background(), field, runtime, &mockEngineIO{})
	if err != nil {
		t.Fatal(err)
	}

	s, ok := val.(string)
	if !ok {
		t.Fatalf("expected string, got %T", val)
	}

	var decoded map[string]any
	json.Unmarshal([]byte(s), &decoded)
	if _, ok := decoded["inputs"]; !ok {
		t.Error("expected inputs key in full dump manifest")
	}
	if _, ok := decoded["knobs"]; !ok {
		t.Error("expected knobs key in full dump manifest")
	}
}
