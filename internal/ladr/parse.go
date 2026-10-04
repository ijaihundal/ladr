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

// Package ladr — config parsing.
// Ported from packages/ladr/parse.ts.
package ladr

import (
	"encoding/json"
	"fmt"
)

// ParseConfig deserialises raw JSON into a PipelineConfig, performing strict
// validation of the known fields.  Unknown top-level keys are rejected to match
// the `.strict()` Zod schema in the TS original.
func ParseConfig(raw json.RawMessage) (*PipelineConfig, error) {
	var cfg PipelineConfig
	if err := strictUnmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parseConfig: %w", err)
	}
	return &cfg, nil
}

// ParseStoredConfig first strips the "author" and "version" meta fields (left
// over from the stored template format) and then parses the remaining object
// as a PipelineConfig.  Mirrors parseStoredConfig from parse.ts.
func ParseStoredConfig(raw json.RawMessage) (*PipelineConfig, error) {
	stripped, err := StripMeta(raw)
	if err != nil {
		return nil, fmt.Errorf("parseStoredConfig: %w", err)
	}
	return ParseConfig(stripped)
}

// StripMeta removes the legacy "author" and "version" meta keys from a stored
// config JSON object.  These fields exist in the database JSONB column (left
// over from the old stored-template format) but are not part of the
// PipelineConfig schema.  If the input is not a JSON object it is returned
// unchanged.
func StripMeta(raw json.RawMessage) (json.RawMessage, error) {
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	obj, ok := generic.(map[string]any)
	if !ok {
		return raw, nil
	}
	delete(obj, "author")
	delete(obj, "version")
	return json.Marshal(obj)
}

// strictUnmarshal decodes src into dst and rejects unknown top-level keys.
func strictUnmarshal(src json.RawMessage, dst *PipelineConfig) error {
	// First decode into a map to detect unknown keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(src, &raw); err != nil {
		return err
	}
	allowed := map[string]bool{
		"id":              true,
		"name":            true,
		"description":     true,
		"ladrVersion":     true,
		"allowedTargets":  true,
		"exit":            true,
		"knobs":           true,
		"steps":           true,
		"trace":           true,
		"reliks":          true,
		"executionBudget": true,
		"defaultManifest": true,
		"indexer":         true,
		"mailbox":         true,
		"schedule":        true,
	}
	for k := range raw {
		if !allowed[k] {
			return fmt.Errorf("unknown top-level key %q (PipelineConfig is strict)", k)
		}
	}

	// Reject null / non-object input (raw is nil for JSON null) and enforce the
	// required top-level fields — matches the TS Zod schema where name, exit,
	// allowedTargets, knobs, and steps are all required.
	if raw == nil {
		return fmt.Errorf("config must be a JSON object")
	}
	for _, k := range []string{"name", "exit", "allowedTargets", "knobs", "steps"} {
		if _, ok := raw[k]; !ok {
			return fmt.Errorf("missing required top-level field %q", k)
		}
	}

	// Now decode each field individually.
	if v, ok := raw["id"]; ok {
		if err := json.Unmarshal(v, &dst.ID); err != nil {
			return err
		}
	}
	if v, ok := raw["name"]; ok {
		if err := json.Unmarshal(v, &dst.Name); err != nil {
			return err
		}
	}
	if v, ok := raw["description"]; ok {
		if err := json.Unmarshal(v, &dst.Description); err != nil {
			return err
		}
	}
	if v, ok := raw["ladrVersion"]; ok {
		if err := json.Unmarshal(v, &dst.LadrVersion); err != nil {
			return err
		}
	}
	if v, ok := raw["allowedTargets"]; ok {
		if err := json.Unmarshal(v, &dst.AllowedTargets); err != nil {
			return err
		}
	}
	if v, ok := raw["exit"]; ok {
		if err := json.Unmarshal(v, &dst.Exit); err != nil {
			return err
		}
	}
	if v, ok := raw["knobs"]; ok {
		var knobs map[string]Knob
		if err := json.Unmarshal(v, &knobs); err != nil {
			return err
		}
		dst.Knobs = knobs
	}
	if v, ok := raw["steps"]; ok {
		var steps []Step
		if err := json.Unmarshal(v, &steps); err != nil {
			return err
		}
		dst.Steps = steps
	}
	if v, ok := raw["trace"]; ok && string(v) != "null" {
		var tc TraceConfig
		if err := json.Unmarshal(v, &tc); err != nil {
			return err
		}
		dst.Trace = &tc
	}
	if v, ok := raw["reliks"]; ok && string(v) != "null" {
		var reliks []RelikDecl
		if err := json.Unmarshal(v, &reliks); err != nil {
			return err
		}
		dst.Reliks = reliks
	}
	if v, ok := raw["indexer"]; ok && string(v) != "null" {
		var ic IndexerConfig
		if err := json.Unmarshal(v, &ic); err != nil {
			return err
		}
		dst.Indexer = &ic
	}
	if v, ok := raw["mailbox"]; ok && string(v) != "null" {
		var mc MailboxConfig
		if err := json.Unmarshal(v, &mc); err != nil {
			return err
		}
		dst.Mailbox = &mc
	}
	if v, ok := raw["schedule"]; ok && string(v) != "null" {
		var sc ScheduleConfig
		if err := json.Unmarshal(v, &sc); err != nil {
			return err
		}
		dst.Schedule = &sc
	}
	if v, ok := raw["executionBudget"]; ok && string(v) != "null" {
		var eb ExecutionBudget
		if err := json.Unmarshal(v, &eb); err != nil {
			return err
		}
		dst.ExecutionBudget = &eb
	}
	if v, ok := raw["defaultManifest"]; ok && string(v) != "null" {
		var views []ManifestView
		if err := json.Unmarshal(v, &views); err != nil {
			return err
		}
		dst.DefaultManifest = views
	}
	return nil
}
