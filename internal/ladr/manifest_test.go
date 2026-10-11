package ladr

import (
	"encoding/json"
	"testing"
)

func TestManifestView_Time_RoundTrip(t *testing.T) {
	original := ManifestView{Kind: ManifestViewTime}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"time"` {
		t.Errorf("expected \"time\", got %s", string(b))
	}
	var decoded ManifestView
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ManifestViewTime {
		t.Errorf("expected ManifestViewTime, got %d", decoded.Kind)
	}
}

func TestManifestView_Structure_RoundTrip(t *testing.T) {
	original := ManifestView{Kind: ManifestViewStructure}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"structure"` {
		t.Errorf("expected \"structure\", got %s", string(b))
	}
	var decoded ManifestView
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ManifestViewStructure {
		t.Errorf("expected ManifestViewStructure, got %d", decoded.Kind)
	}
}

func TestManifestView_Index_Folder_RoundTrip(t *testing.T) {
	original := ManifestView{Kind: ManifestViewIndex, Relik: "Memory", Folder: "facts"}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(b, &raw)
	idx, ok := raw["index"].(map[string]any)
	if !ok {
		t.Fatalf("expected index object, got %v", raw)
	}
	if idx["relik"] != "Memory" {
		t.Errorf("expected relik=Memory, got %v", idx["relik"])
	}
	if idx["folder"] != "facts" {
		t.Errorf("expected folder=facts, got %v", idx["folder"])
	}

	var decoded ManifestView
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ManifestViewIndex {
		t.Errorf("expected ManifestViewIndex, got %d", decoded.Kind)
	}
	if decoded.Relik != "Memory" || decoded.Folder != "facts" {
		t.Errorf("expected Relik=Memory Folder=facts, got Relik=%q Folder=%q", decoded.Relik, decoded.Folder)
	}
}

func TestManifestView_Index_Depth_RoundTrip(t *testing.T) {
	depth := 5
	original := ManifestView{Kind: ManifestViewIndex, Relik: "Memory", Depth: &depth}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded ManifestView
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ManifestViewIndex {
		t.Errorf("expected ManifestViewIndex, got %d", decoded.Kind)
	}
	if decoded.Relik != "Memory" {
		t.Errorf("expected Relik=Memory, got %q", decoded.Relik)
	}
	if decoded.Depth == nil || *decoded.Depth != 5 {
		t.Errorf("expected Depth=5, got %v", decoded.Depth)
	}
}

func TestManifestView_Tree_Full_RoundTrip(t *testing.T) {
	original := ManifestView{Kind: ManifestViewTree, Relik: "Memory", DepthFull: true}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(b, &raw)
	tree, ok := raw["tree"].(map[string]any)
	if !ok {
		t.Fatalf("expected tree object, got %v", raw)
	}
	if tree["depth"] != "full" {
		t.Errorf("expected depth=full, got %v", tree["depth"])
	}

	var decoded ManifestView
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ManifestViewTree || !decoded.DepthFull {
		t.Errorf("expected ManifestViewTree DepthFull=true, got Kind=%d DepthFull=%v", decoded.Kind, decoded.DepthFull)
	}
}

func TestManifestView_Tree_NumericDepth_RoundTrip(t *testing.T) {
	depth := 3
	original := ManifestView{Kind: ManifestViewTree, Relik: "Memory", Depth: &depth}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded ManifestView
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != ManifestViewTree {
		t.Errorf("expected ManifestViewTree, got %d", decoded.Kind)
	}
	if decoded.Depth == nil || *decoded.Depth != 3 {
		t.Errorf("expected Depth=3, got %v", decoded.Depth)
	}
}

func TestManifestView_InvalidKeyword(t *testing.T) {
	var v ManifestView
	err := json.Unmarshal([]byte(`"invalid"`), &v)
	if err == nil {
		t.Fatal("expected error for invalid keyword")
	}
}

func TestManifestView_Index_MissingRelik(t *testing.T) {
	var v ManifestView
	err := json.Unmarshal([]byte(`{"index":{"folder":"facts"}}`), &v)
	if err == nil {
		t.Fatal("expected error for missing relik")
	}
}

func TestManifestField_BareMarshal(t *testing.T) {
	field := ManifestField{Name: "Available", Type: "manifest"}
	b, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	json.Unmarshal(b, &decoded)
	if decoded["type"] != "manifest" {
		t.Errorf("expected type=manifest, got %v", decoded["type"])
	}
	if _, hasFrom := decoded["from"]; hasFrom {
		t.Errorf("bare manifest should not have from, got %v", decoded["from"])
	}
}

func TestManifestField_WithFromMarshal(t *testing.T) {
	field := ManifestField{
		Name: "Now",
		Type: "manifest",
		From: &ManifestView{Kind: ManifestViewTime},
	}
	b, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	json.Unmarshal(b, &decoded)
	if decoded["type"] != "manifest" {
		t.Errorf("expected type=manifest, got %v", decoded["type"])
	}
	if decoded["from"] != "time" {
		t.Errorf("expected from=time, got %v", decoded["from"])
	}
}

func TestDefaultManifest_Parse(t *testing.T) {
	raw := `{
		"name": "Test",
		"allowedTargets": {"strategy": "universal"},
		"exit": "answer",
		"knobs": {},
		"steps": [],
		"defaultManifest": ["time", "structure", {"index": {"relik": "Memory", "folder": "facts"}}]
	}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if len(cfg.DefaultManifest) != 3 {
		t.Fatalf("expected 3 default manifest views, got %d", len(cfg.DefaultManifest))
	}
	if cfg.DefaultManifest[0].Kind != ManifestViewTime {
		t.Errorf("expected first view = time, got %d", cfg.DefaultManifest[0].Kind)
	}
	if cfg.DefaultManifest[1].Kind != ManifestViewStructure {
		t.Errorf("expected second view = structure, got %d", cfg.DefaultManifest[1].Kind)
	}
	if cfg.DefaultManifest[2].Kind != ManifestViewIndex {
		t.Errorf("expected third view = index, got %d", cfg.DefaultManifest[2].Kind)
	}
	if cfg.DefaultManifest[2].Relik != "Memory" || cfg.DefaultManifest[2].Folder != "facts" {
		t.Errorf("expected Relik=Memory Folder=facts, got Relik=%q Folder=%q",
			cfg.DefaultManifest[2].Relik, cfg.DefaultManifest[2].Folder)
	}
}
