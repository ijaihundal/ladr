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

package ladr_test

import (
	"strings"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// --- LoopRef ---------------------------------------------------------------

func TestLoopRefUnmarshal(t *testing.T) {
	for _, kw := range []string{"accumulate", "previous", "current"} {
		t.Run("keyword/"+kw, func(t *testing.T) {
			var l ladr.LoopRef
			if err := json.Unmarshal([]byte(`"`+kw+`"`), &l); err != nil {
				t.Fatalf("err: %v", err)
			}
			if l.Kind != ladr.LoopRefString || l.Str != kw {
				t.Errorf("got %+v", l)
			}
		})
	}
	for _, c := range []struct{ name, raw string }{
		{"number0", `0`},
		{"number42", `42`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var l ladr.LoopRef
			if err := json.Unmarshal([]byte(c.raw), &l); err != nil {
				t.Fatalf("err: %v", err)
			}
			if l.Kind != ladr.LoopRefNumber {
				t.Errorf("kind = %v", l.Kind)
			}
		})
	}
	t.Run("reject invalid keyword", func(t *testing.T) {
		var l ladr.LoopRef
		if err := json.Unmarshal([]byte(`"next"`), &l); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("reject bool", func(t *testing.T) {
		var l ladr.LoopRef
		if err := json.Unmarshal([]byte(`true`), &l); err == nil {
			t.Error("expected error")
		}
	})
}

func TestLoopRefRoundTrip(t *testing.T) {
	cases := []ladr.LoopRef{
		{Kind: ladr.LoopRefString, Str: "current"},
		{Kind: ladr.LoopRefString, Str: "previous"},
		{Kind: ladr.LoopRefString, Str: "accumulate"},
		{Kind: ladr.LoopRefNumber, Num: 7},
	}
	for i, c := range cases {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		var back ladr.LoopRef
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}
		if back != c {
			t.Errorf("case %d: got %+v want %+v", i, back, c)
		}
	}
}

// --- KnobInput -------------------------------------------------------------

func TestKnobInputUnmarshal(t *testing.T) {
	t.Run("slider", func(t *testing.T) {
		var k ladr.KnobInput
		err := json.Unmarshal([]byte(`{"input":"slider","steps":[{"title":"A","value":1,"default":true},{"title":"B","value":2},{"title":"C","value":3}]}`), &k)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if k.Kind != ladr.KnobInputSlider || len(k.Slider.Steps) != 3 {
			t.Errorf("got %+v", k)
		}
	})
	t.Run("numerical", func(t *testing.T) {
		var k ladr.KnobInput
		err := json.Unmarshal([]byte(`{"input":"numerical","default":5,"min":1,"max":10}`), &k)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if k.Kind != ladr.KnobInputNumerical || k.Numerical.Min != 1 {
			t.Errorf("got %+v", k)
		}
	})
	t.Run("missing input rejected", func(t *testing.T) {
		var k ladr.KnobInput
		if err := json.Unmarshal([]byte(`{"default":5,"min":1,"max":10}`), &k); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("unknown input rejected", func(t *testing.T) {
		var k ladr.KnobInput
		if err := json.Unmarshal([]byte(`{"input":"dial"}`), &k); err == nil {
			t.Error("expected error")
		}
	})
}

// --- Knob (intersection round-trip) ----------------------------------------

func TestKnobRoundTrip(t *testing.T) {
	raw := `{"name":"Coverage","type":"nodes","input":"slider","steps":[{"title":"Low","value":3,"default":true},{"title":"Med","value":6},{"title":"High","value":12}]}`
	var k ladr.Knob
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if k.Name != "Coverage" || k.Type != ladr.KnobTypeNodes || k.Input.Kind != ladr.KnobInputSlider {
		t.Fatalf("parsed wrong: %+v", k)
	}
	b, err := json.Marshal(k)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var k2 ladr.Knob
	if err := json.Unmarshal(b, &k2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if k2.Name != k.Name || k2.Type != k.Type || k2.Input.Kind != k.Input.Kind || len(k2.Input.Slider.Steps) != 3 {
		t.Errorf("round-trip mismatch: %+v", k2)
	}
	if k2.Input.Slider.Steps[0].Default != true {
		t.Errorf("default flag lost: %+v", k2.Input.Slider.Steps[0])
	}
}

func TestKnobNumericalRoundTrip(t *testing.T) {
	raw := `{"name":"Rounds","type":"loops","input":"numerical","default":3,"min":1,"max":10}`
	var k ladr.Knob
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b, _ := json.Marshal(k)
	var k2 ladr.Knob
	if err := json.Unmarshal(b, &k2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if k2.Input.Kind != ladr.KnobInputNumerical {
		t.Fatalf("kind lost")
	}
	if k2.Input.Numerical.Default == nil || *k2.Input.Numerical.Default != 3 {
		t.Errorf("default lost: %+v", k2.Input.Numerical)
	}
	if k2.Input.Numerical.Min != 1 || k2.Input.Numerical.Max != 10 {
		t.Errorf("bounds lost: %+v", k2.Input.Numerical)
	}
}

// --- Field -----------------------------------------------------------------

func TestFieldUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		kind ladr.FieldType
	}{
		{"text", `{"name":"Q","type":"text","from":"input.context"}`, ladr.FieldText},
		{"ingest", `{"name":"A","type":"ingest","from":{"stepId":"s0","loopRef":"current"}}`, ladr.FieldIngest},
		{"multi_ingest", `{"name":"M","type":"multi_ingest","from":[{"stepId":"s0","loopRef":"accumulate"}]}`, ladr.FieldMultiIngest},
		{"nodeInfo", `{"name":"N","type":"nodeInfo"}`, ladr.FieldNodeInfo},
		{"knobInfo", `{"name":"K","type":"knobInfo","from":"coverage"}`, ladr.FieldKnobInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var f ladr.Field
			if err := json.Unmarshal([]byte(c.raw), &f); err != nil {
				t.Fatalf("err: %v", err)
			}
			if f.Kind != c.kind {
				t.Errorf("kind = %v want %v", f.Kind, c.kind)
			}
		})
	}
	t.Run("unknown type rejected", func(t *testing.T) {
		var f ladr.Field
		if err := json.Unmarshal([]byte(`{"name":"X","type":"bogus"}`), &f); err == nil {
			t.Error("expected error")
		}
	})
}

// --- Nodes -----------------------------------------------------------------

func TestNodesUnmarshal(t *testing.T) {
	t.Run("number", func(t *testing.T) {
		var n ladr.Nodes
		if err := json.Unmarshal([]byte(`5`), &n); err != nil {
			t.Fatal(err)
		}
		if n.Kind != ladr.NodesNumber || n.Num != 5 {
			t.Errorf("got %+v", n)
		}
	})
	t.Run("string", func(t *testing.T) {
		var n ladr.Nodes
		if err := json.Unmarshal([]byte(`"{{knobs.coverage}}"`), &n); err != nil {
			t.Fatal(err)
		}
		if n.Kind != ladr.NodesString {
			t.Errorf("got %+v", n)
		}
	})
	t.Run("from-object", func(t *testing.T) {
		var n ladr.Nodes
		if err := json.Unmarshal([]byte(`{"from":{"stepId":"s0","loopRef":"current","pruned":true}}`), &n); err != nil {
			t.Fatal(err)
		}
		if n.Kind != ladr.NodesFromObj || n.From.StepID != "s0" || !n.From.Pruned {
			t.Errorf("got %+v", n)
		}
	})
	t.Run("reject array", func(t *testing.T) {
		var n ladr.Nodes
		if err := json.Unmarshal([]byte(`[1,2]`), &n); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("reject bool", func(t *testing.T) {
		var n ladr.Nodes
		if err := json.Unmarshal([]byte(`true`), &n); err == nil {
			t.Error("expected error")
		}
	})
}

// --- MaxDepth --------------------------------------------------------------

func TestMaxDepthUnmarshal(t *testing.T) {
	t.Run("number", func(t *testing.T) {
		var m ladr.MaxDepth
		if err := json.Unmarshal([]byte(`3`), &m); err != nil {
			t.Fatal(err)
		}
		if m.IsStr || m.Num != 3 {
			t.Errorf("got %+v", m)
		}
	})
	t.Run("string", func(t *testing.T) {
		var m ladr.MaxDepth
		if err := json.Unmarshal([]byte(`"{{knobs.iterations}}"`), &m); err != nil {
			t.Fatal(err)
		}
		if !m.IsStr {
			t.Errorf("got %+v", m)
		}
	})
	t.Run("reject bool", func(t *testing.T) {
		var m ladr.MaxDepth
		if err := json.Unmarshal([]byte(`true`), &m); err == nil {
			t.Error("expected error")
		}
	})
}

// --- FieldsConfig ----------------------------------------------------------

func TestFieldsConfig(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		var fc ladr.FieldsConfig
		if err := json.Unmarshal([]byte(`"clone:s0"`), &fc); err != nil {
			t.Fatal(err)
		}
		if !fc.IsString || fc.Str != "clone:s0" || fc.IsSet() != true {
			t.Errorf("got %+v", fc)
		}
	})
	t.Run("array", func(t *testing.T) {
		var fc ladr.FieldsConfig
		if err := json.Unmarshal([]byte(`[{"name":"Q","type":"text","from":"input.context"}]`), &fc); err != nil {
			t.Fatal(err)
		}
		if fc.IsString || len(fc.Fields) != 1 {
			t.Errorf("got %+v", fc)
		}
	})
}

// --- Step ------------------------------------------------------------------

func TestStepUnmarshal(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		var s ladr.Step
		if err := json.Unmarshal([]byte(`{"id":"a","name":"A","type":"normal"}`), &s); err != nil {
			t.Fatal(err)
		}
		if s.Kind != ladr.StepTypeNormal || s.Normal == nil {
			t.Errorf("got %+v", s)
		}
	})
	t.Run("sequential", func(t *testing.T) {
		var s ladr.Step
		if err := json.Unmarshal([]byte(`{"id":"a","name":"A","type":"sequential","nodes":3}`), &s); err != nil {
			t.Fatal(err)
		}
		if s.Kind != ladr.StepTypeSequential || s.Sequential == nil {
			t.Errorf("got %+v", s)
		}
	})
	t.Run("group with children", func(t *testing.T) {
		var s ladr.Step
		if err := json.Unmarshal([]byte(`{"id":"g","name":"G","type":"group","steps":[{"id":"a","name":"A","type":"normal"},{"id":"b","name":"B","type":"normal"}]}`), &s); err != nil {
			t.Fatal(err)
		}
		if s.Kind != ladr.StepTypeGroup || len(s.Group.Steps) != 2 {
			t.Errorf("got %+v", s)
		}
	})
	t.Run("unknown type rejected", func(t *testing.T) {
		var s ladr.Step
		if err := json.Unmarshal([]byte(`{"id":"a","name":"A","type":"bogus"}`), &s); err == nil {
			t.Error("expected error")
		}
	})
}

func TestStepIDNameAccessors(t *testing.T) {
	var s ladr.Step
	if err := json.Unmarshal([]byte(`{"id":"x","name":"X","type":"normal"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.ID() != "x" || s.Name() != "X" {
		t.Errorf("ID/Name = %q/%q", s.ID(), s.Name())
	}
	if s.Base() == nil || s.Base().ID != "x" {
		t.Error("Base() wrong")
	}
}

// --- AllowedTargetsPolicy --------------------------------------------------

func TestAllowedTargetsUnmarshal(t *testing.T) {
	t.Run("universal", func(t *testing.T) {
		var a ladr.AllowedTargetsPolicy
		if err := json.Unmarshal([]byte(`{"strategy":"universal"}`), &a); err != nil {
			t.Fatal(err)
		}
		if a.Strategy != ladr.AllowedTargetsUniversal {
			t.Errorf("got %v", a.Strategy)
		}
	})
	t.Run("constrained", func(t *testing.T) {
		var a ladr.AllowedTargetsPolicy
		if err := json.Unmarshal([]byte(`{"strategy":"constrained","providers":["openrouter"],"models":["gpt-oss-20b"]}`), &a); err != nil {
			t.Fatal(err)
		}
		if a.Strategy != ladr.AllowedTargetsConstrained || len(a.Providers) != 1 {
			t.Errorf("got %+v", a)
		}
	})
	// Fix #2: invalid strategy rejected at unmarshal.
	t.Run("invalid strategy rejected", func(t *testing.T) {
		var a ladr.AllowedTargetsPolicy
		if err := json.Unmarshal([]byte(`{"strategy":"MIXED"}`), &a); err == nil {
			t.Error("expected error")
		}
	})
}

// --- PipelineConfig round-trip ---------------------------------------------

func TestPipelineConfigRoundTrip(t *testing.T) {
	cfg := mustParse(t, minimalConfig)
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cfg2, err := ladr.ParseConfig(b)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if cfg2.Name != cfg.Name || cfg2.Exit != cfg.Exit || len(cfg2.Steps) != len(cfg.Steps) {
		t.Errorf("round-trip mismatch: %+v vs %+v", cfg2, cfg)
	}
}

func TestLoopRefEmptyStringRoundTrip(t *testing.T) {
	// Regression: stored ingest fields can carry "loopRef": "" (old marshal
	// emitted it for unset). Unmarshal must tolerate it as unset, and
	// marshaling an unset LoopRef must omit the field entirely.
	var n ladr.IngestField
	if err := json.Unmarshal([]byte(`{"name":"F","type":"ingest","from":{"stepId":"s0","loopRef":""}}`), &n); err != nil {
		t.Fatalf("unmarshal with loopRef empty failed: %v", err)
	}
	if n.From.StepID != "s0" {
		t.Fatalf("from not parsed: %+v", n.From)
	}
	if !n.From.LoopRef.IsZero() {
		t.Fatalf("loopRef \"\" must parse as unset, got %+v", n.From.LoopRef)
	}

	out, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(out, []byte(`"loopRef"`)) {
		t.Fatalf("unset loopRef must be omitted from output, got %s", out)
	}
}

func TestStepModelRoundTrip(t *testing.T) {
	// model: on a step must survive the stored round-trip.
	var s ladr.PipelineConfig
	raw := []byte(`{
		"name": "m", "exit": "s1", "allowedTargets": {"strategy": "universal"},
		"steps": [{"id": "s1", "name": "a", "type": "normal", "model": "cheap-model", "systemPrompt": "x"}]
	}`)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Steps[0].Normal.BaseStep.Model != "cheap-model" {
		t.Fatalf("model not parsed: %+v", s.Steps[0].Normal.BaseStep.Model)
	}
	out, _ := json.Marshal(s)
	if !bytes.Contains(out, []byte(`"model":"cheap-model"`)) {
		t.Fatalf("model not marshaled: %s", out)
	}
}

func TestValidateStepModelsAgainstAllowedTargets(t *testing.T) {
	cfg := &ladr.PipelineConfig{
		Name:           "m",
		Exit:           "s1",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsConstrained, Models: []string{"allowed-a"}},
		Steps: []ladr.Step{
			{Normal: &ladr.NormalStep{BaseStep: ladr.BaseStep{ID: "s1", Name: "a", Model: "not-allowed"}}},
		},
	}
	res := ladr.ValidateConfig(cfg)
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, `model "not-allowed"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("constrained ladder must reject a step model outside allowedTargets.models; errors=%v", res.Errors)
	}

	cfg.Steps[0].Normal.Model = "allowed-a"
	res2 := ladr.ValidateConfig(cfg)
	for _, e := range res2.Errors {
		if strings.Contains(e, "model") {
			t.Fatalf("allowed step model must not error; errors=%v", res2.Errors)
		}
	}
}
