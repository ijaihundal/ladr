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

// Package ladr — knob default resolution and sanitisation.
// Ported from packages/ladr/knobs.ts.
package ladr

import "math"

// ResolveKnobDefaults computes the default value for every knob in the config.
// Returns a map keyed by knob name.
//
// For slider knobs the preferred step is the first one with default==true,
// falling back to the first step.  For numerical knobs the value is clamped to
// [min, max], defaulting to min when no explicit default is set.
func ResolveKnobDefaults(config *PipelineConfig) map[string]float64 {
	result := make(map[string]float64)
	if config == nil {
		return result
	}
	for key, knob := range config.Knobs {
		switch knob.Input.Kind {
		case KnobInputSlider:
			if len(knob.Input.Slider.Steps) == 0 {
				continue
			}
			var preferred *SliderStep
			for i := range knob.Input.Slider.Steps {
				if knob.Input.Slider.Steps[i].Default {
					preferred = &knob.Input.Slider.Steps[i]
					break
				}
			}
			if preferred == nil {
				preferred = &knob.Input.Slider.Steps[0]
			}
			if !math.IsNaN(preferred.Value) && !math.IsInf(preferred.Value, 0) {
				result[key] = preferred.Value
			}

		case KnobInputNumerical:
			min := knob.Input.Numerical.Min
			max := knob.Input.Numerical.Max
			value := min
			if knob.Input.Numerical.Default != nil {
				def := *knob.Input.Numerical.Default
				if !math.IsNaN(def) && !math.IsInf(def, 0) {
					value = def
				}
			}
			if !math.IsNaN(min) && !math.IsInf(min, 0) &&
				!math.IsNaN(max) && !math.IsInf(max, 0) {
				result[key] = math.Min(max, math.Max(min, value))
			}
		}
	}
	return result
}

// SanitizeKnobs takes user-supplied raw knob values and the pipeline config and
// returns a clean map containing every knob (defaults first, overridden by valid
// user values).
//
// For sliders only exact matches against a step value are accepted.  For
// numericals the value is clamped to [min, max].
func SanitizeKnobs(rawKnobs map[string]float64, config *PipelineConfig) map[string]float64 {
	result := ResolveKnobDefaults(config)
	if config == nil {
		return result
	}
	for key, knob := range config.Knobs {
		userVal, ok := rawKnobs[key]
		if !ok {
			continue
		}
		if math.IsNaN(userVal) || math.IsInf(userVal, 0) {
			continue
		}

		switch knob.Input.Kind {
		case KnobInputSlider:
			if len(knob.Input.Slider.Steps) == 0 {
				continue
			}
			for _, step := range knob.Input.Slider.Steps {
				if step.Value == userVal {
					result[key] = userVal
					break
				}
			}

		case KnobInputNumerical:
			min := knob.Input.Numerical.Min
			max := knob.Input.Numerical.Max
			if !math.IsNaN(min) && !math.IsInf(min, 0) &&
				!math.IsNaN(max) && !math.IsInf(max, 0) {
				result[key] = math.Min(max, math.Max(min, userVal))
			}
		}
	}
	return result
}
