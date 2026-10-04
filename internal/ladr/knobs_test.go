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
	"math"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
)

// configWithKnobs builds a minimal valid PipelineConfig carrying the given
// knobs JSON object body.
func configWithKnobs(t *testing.T, knobsBody string) *ladr.PipelineConfig {
	t.Helper()
	if knobsBody == "" {
		knobsBody = ``
	}
	raw := `{"name":"T","allowedTargets":{"strategy":"universal"},"exit":"s","knobs":{` + knobsBody + `},"steps":[{"id":"s","name":"S","type":"normal"}]}`
	cfg, err := ladr.ParseStoredConfig([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}

const sliderConfig = `"coverage":{"name":"Coverage","type":"nodes","input":"slider","steps":[{"title":"Compact","value":3,"default":true},{"title":"Balanced","value":6},{"title":"Wide","value":12}]}`
const numericalConfig = `"rounds":{"name":"Rounds","type":"loops","input":"numerical","default":3,"min":1,"max":10}`
const mixedConfig = `"coverage":{"name":"Coverage","type":"nodes","input":"slider","steps":[{"title":"Low","value":3,"default":true},{"title":"Med","value":6},{"title":"High","value":12}]},` +
	`"rounds":{"name":"Rounds","type":"loops","input":"numerical","default":3,"min":1,"max":10},` +
	`"depth":{"name":"Depth","type":"recursion","input":"numerical","default":2,"min":1,"max":5}`

func floatPtr(v float64) *float64 { return &v }

var _ = floatPtr

// --- ResolveKnobDefaults ---------------------------------------------------

func TestResolveKnobDefaults(t *testing.T) {
	t.Run("slider picks default step value", func(t *testing.T) {
		r := ladr.ResolveKnobDefaults(configWithKnobs(t, sliderConfig))
		if r["coverage"] != 3 {
			t.Errorf("coverage = %v", r["coverage"])
		}
	})
	t.Run("slider falls back to first when no default", func(t *testing.T) {
		cfg := configWithKnobs(t, `"x":{"name":"X","type":"generic","input":"slider","steps":[{"title":"A","value":7},{"title":"B","value":14},{"title":"C","value":21}]}`)
		if ladr.ResolveKnobDefaults(cfg)["x"] != 7 {
			t.Error("expected 7")
		}
	})
	t.Run("slider skips non-finite default step value", func(t *testing.T) {
		// Build in-memory: JSON cannot represent NaN. The default step carries
		// a NaN value, so the non-finite guard must omit the knob entirely.
		cfg := configWithKnobs(t, numericalConfig)
		k := ladr.Knob{
			Name: "X",
			Type: ladr.KnobTypeGeneric,
			Input: ladr.KnobInput{
				Kind: ladr.KnobInputSlider,
				Slider: ladr.SliderInput{Input: "slider", Steps: []ladr.SliderStep{
					{Title: "A", Value: math.NaN(), Default: true},
					{Title: "B", Value: 5},
					{Title: "C", Value: 10},
				}},
			},
		}
		cfg.Knobs["x"] = k
		r := ladr.ResolveKnobDefaults(cfg)
		if _, ok := r["x"]; ok {
			t.Errorf("expected NaN default step to be skipped, got x=%v", r["x"])
		}
	})
	t.Run("numerical uses default", func(t *testing.T) {
		if ladr.ResolveKnobDefaults(configWithKnobs(t, numericalConfig))["rounds"] != 3 {
			t.Error("expected 3")
		}
	})
	t.Run("numerical falls back to min when no default", func(t *testing.T) {
		cfg := configWithKnobs(t, `"x":{"name":"X","type":"generic","input":"numerical","min":2,"max":10}`)
		if ladr.ResolveKnobDefaults(cfg)["x"] != 2 {
			t.Error("expected 2")
		}
	})
	t.Run("numerical clamps above max", func(t *testing.T) {
		cfg := configWithKnobs(t, `"x":{"name":"X","type":"generic","input":"numerical","default":50,"min":1,"max":10}`)
		if ladr.ResolveKnobDefaults(cfg)["x"] != 10 {
			t.Error("expected 10")
		}
	})
	t.Run("numerical clamps below min", func(t *testing.T) {
		cfg := configWithKnobs(t, `"x":{"name":"X","type":"generic","input":"numerical","default":-5,"min":1,"max":10}`)
		if ladr.ResolveKnobDefaults(cfg)["x"] != 1 {
			t.Error("expected 1")
		}
	})
	t.Run("empty knobs returns empty", func(t *testing.T) {
		r := ladr.ResolveKnobDefaults(configWithKnobs(t, ""))
		if len(r) != 0 {
			t.Errorf("expected empty, got %v", r)
		}
	})
	t.Run("nil config returns empty", func(t *testing.T) {
		if r := ladr.ResolveKnobDefaults(nil); len(r) != 0 {
			t.Errorf("expected empty, got %v", r)
		}
	})
	t.Run("mixed knobs resolve all", func(t *testing.T) {
		r := ladr.ResolveKnobDefaults(configWithKnobs(t, mixedConfig))
		if r["coverage"] != 3 || r["rounds"] != 3 || r["depth"] != 2 {
			t.Errorf("got %v", r)
		}
	})
	t.Run("skips knob with neither slider nor numerical input", func(t *testing.T) {
		// A knob missing input entirely cannot be built via parse (rejected),
		// so construct one in-memory with no input variant.
		cfg := configWithKnobs(t, numericalConfig)
		cfg.Knobs["bad"] = ladr.Knob{Name: "Bad", Type: ladr.KnobTypeGeneric} // Input zero-value
		r := ladr.ResolveKnobDefaults(cfg)
		if _, ok := r["bad"]; ok {
			t.Error("bad knob should be skipped")
		}
	})
}

// --- SanitizeKnobs ---------------------------------------------------------

func TestSanitizeKnobs(t *testing.T) {
	t.Run("slider value matching step accepted", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"coverage": 6}, configWithKnobs(t, sliderConfig))
		if r["coverage"] != 6 {
			t.Errorf("got %v", r["coverage"])
		}
	})
	t.Run("slider value not matching any step ignored", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"coverage": 99}, configWithKnobs(t, sliderConfig))
		if r["coverage"] != 3 {
			t.Errorf("got %v", r["coverage"])
		}
	})
	t.Run("numerical within range accepted", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"rounds": 7}, configWithKnobs(t, numericalConfig))
		if r["rounds"] != 7 {
			t.Errorf("got %v", r["rounds"])
		}
	})
	t.Run("numerical below min clamped", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"rounds": -1}, configWithKnobs(t, numericalConfig))
		if r["rounds"] != 1 {
			t.Errorf("got %v", r["rounds"])
		}
	})
	t.Run("numerical above max clamped", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"rounds": 50}, configWithKnobs(t, numericalConfig))
		if r["rounds"] != 10 {
			t.Errorf("got %v", r["rounds"])
		}
	})
	t.Run("NaN caller value ignored", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"rounds": math.NaN()}, configWithKnobs(t, numericalConfig))
		if r["rounds"] != 3 {
			t.Errorf("got %v", r["rounds"])
		}
	})
	t.Run("Inf caller value ignored", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"rounds": math.Inf(1)}, configWithKnobs(t, numericalConfig))
		if r["rounds"] != 3 {
			t.Errorf("got %v", r["rounds"])
		}
	})
	t.Run("missing caller value uses default", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{}, configWithKnobs(t, numericalConfig))
		if r["rounds"] != 3 {
			t.Errorf("got %v", r["rounds"])
		}
	})
	t.Run("extra keys in caller ignored", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"nonexistent": 42}, configWithKnobs(t, numericalConfig))
		if _, ok := r["nonexistent"]; ok {
			t.Error("extra key leaked")
		}
		if r["rounds"] != 3 {
			t.Errorf("rounds = %v", r["rounds"])
		}
	})
	t.Run("nil rawKnobs returns all defaults", func(t *testing.T) {
		r := ladr.SanitizeKnobs(nil, configWithKnobs(t, mixedConfig))
		if r["coverage"] != 3 || r["rounds"] != 3 || r["depth"] != 2 {
			t.Errorf("got %v", r)
		}
	})
	t.Run("partial override leaves others at default", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"coverage": 12}, configWithKnobs(t, mixedConfig))
		if r["coverage"] != 12 || r["rounds"] != 3 || r["depth"] != 2 {
			t.Errorf("got %v", r)
		}
	})
	t.Run("nil config returns empty", func(t *testing.T) {
		r := ladr.SanitizeKnobs(map[string]float64{"x": 1}, nil)
		if len(r) != 0 {
			t.Errorf("expected empty, got %v", r)
		}
	})
}
