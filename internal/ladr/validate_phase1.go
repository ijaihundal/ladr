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

// Package ladr — Phase 1 validation rules for the extended grammar.
// Covers: reliks, relikRead references, relikWrite targets, if/then/else
// conditions, jumps, dynamic steps, retrieve blocks, execution budget.
package ladr

import (
	"time"

	"github.com/robfig/cron/v3"
	"encoding/json"
	"regexp"
)

// System metatag names — always valid in relikRead, auto-maintained by engine.
var systemMetatags = map[string]bool{
	"index":    true,
	"tree":     true,
	"created":  true,
	"modified": true,
	"size":     true,
}

// ---------------------------------------------------------------------------
// Wired into ValidateConfig
// ---------------------------------------------------------------------------

// validatePhase1 runs all Phase 1 validation rules.
func validatePhase1(config *PipelineConfig, stepMap map[string]Step, errors *[]string) {
	validateRelikDeclarations(config.Reliks, errors)
	validateRelikReferences(config.Steps, config.Reliks, errors)
	validateIndexer(config, errors)
	validateMailbox(config, errors)
	validateSchedule(config, errors)
	validateJumpsAndActions(config.Steps, stepMap, config.Reliks, errors)
	validateIfConditions(config.Steps, errors)
	validateDynamicSteps(config.Steps, errors)
	validateAdaptiveKeys(config.Steps, config.Reliks, errors)
	validateRelikWrites(config.Steps, config.Reliks, errors)
	validateExecutionBudget(config.ExecutionBudget, errors)
	validateNoContinueIfConflict(config.Steps, errors)
	validateFallbacks(config.Steps, config.Reliks, stepMap, errors)
	validateManifestViews(config, errors)
	validateFieldRules(config.Steps, errors)
	validateNodesFromReferences(config.Steps, stepMap, errors)
	validateRecursionMaxDepth(config.Steps, errors)
}

// validateFieldRules checks field-level constraints that cross-cut individual
// field types. Currently:
// - nodeInfo with a `from` is rejected (spec §5.7: nodeInfo takes no source).
func validateFieldRules(steps []Step, errors *[]string) {
	for _, base := range flattenNonGroupSteps(steps) {
		if base.Fields == nil || base.Fields.IsString {
			continue
		}
		for _, field := range base.Fields.Fields {
			if field.Kind == FieldNodeInfo && field.NodeInfo.From != "" {
				*errors = append(*errors,
					`Step "`+base.ID+`": field "`+field.NodeInfo.Name+
						`" (nodeInfo): "from" is not allowed on nodeInfo fields`)
			}
		}
	}
}

// validateNodesFromReferences checks that nodes.from.stepId references an
// existing step (§7.2). Previously a ghost stepId was silently accepted.
func validateNodesFromReferences(steps []Step, stepMap map[string]Step, errors *[]string) {
	for _, base := range flattenNonGroupSteps(steps) {
		if base.Nodes == nil || base.Nodes.Kind != NodesFromObj {
			continue
		}
		refID := base.Nodes.From.StepID
		if refID == "" {
			continue
		}
		if _, exists := stepMap[refID]; !exists {
			*errors = append(*errors,
				`Step "`+base.ID+`": nodes.from references non-existent step "`+refID+`"`)
		}
	}
}

// validateRecursionMaxDepth rejects maxDepth:0 (pointless per spec §6.7).
func validateRecursionMaxDepth(steps []Step, errors *[]string) {
	for _, base := range flattenNonGroupSteps(steps) {
		if base.Recursion == nil {
			continue
		}
		if !base.Recursion.MaxDepthNumOrStr.IsStr && base.Recursion.MaxDepthNumOrStr.Num == 0 {
			*errors = append(*errors,
				`Step "`+base.ID+`": recursion.maxDepth must be > 0 (0 means no recursion)`)
		}
	}
}

// ---------------------------------------------------------------------------
// 1. validateRelikDeclarations — structure of declared reliks
// ---------------------------------------------------------------------------

func validateRelikDeclarations(reliks []RelikDecl, errors *[]string) {
	seenTitles := make(map[string]bool)
	for _, relik := range reliks {
		if relik.Title == "" {
			*errors = append(*errors, `Relik: title must be non-empty`)
			continue
		}
		if seenTitles[relik.Title] {
			*errors = append(*errors, `Duplicate relik title: "`+relik.Title+`"`)
		}
		seenTitles[relik.Title] = true

		switch relik.Mode {
		case "", RelikModeDeclare, RelikModeReference:
		default:
			*errors = append(*errors, `Relik "`+relik.Title+`": invalid mode "`+relik.Mode+`" (want "declare" or "reference")`)
		}
		if relik.IsReference() {
			// A reference binds an existing relik by title; declaring structure
			// alongside it would be two owners for one shape.
			if len(relik.Folders) > 0 {
				*errors = append(*errors, `Relik "`+relik.Title+`": mode "reference" must not declare folders (structure belongs to the declaring ladder)`)
			}
			continue
		}

		if len(relik.Folders) == 0 {
			*errors = append(*errors, `Relik "`+relik.Title+`": must have at least 1 folder`)
		}

		seenFolders := make(map[string]bool)
		for _, folder := range relik.Folders {
			if folder.Name == "" {
				*errors = append(*errors, `Relik "`+relik.Title+`": folder name must be non-empty`)
				continue
			}
			if seenFolders[folder.Name] {
				*errors = append(*errors, `Relik "`+relik.Title+`": duplicate folder "`+folder.Name+`"`)
			}
			seenFolders[folder.Name] = true

			if folder.TokenLimit <= 0 {
				*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`": tokenLimit must be > 0`)
			}

			// Validate folder-level metatags
			seenMeta := make(map[string]bool)
			for _, m := range folder.Metatags {
				if err := validateMetatagType(m.Type); err != nil {
					*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`", metatag "`+m.Name+`": `+err.Error())
				}
				if m.Name == "" {
					*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`": metatag name must be non-empty`)
				}
				if seenMeta[m.Name] {
					*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`": duplicate metatag "`+m.Name+`"`)
				}
				seenMeta[m.Name] = true
				if m.Type == MetatagTypeObject && len(m.Schema) == 0 {
					*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`", metatag "`+m.Name+`": type "object" requires inline schema`)
				}
			}

			// Validate files
			seenFiles := make(map[string]bool)
			for _, file := range folder.Files {
				if file.Name == "" {
					*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`": file name must be non-empty`)
					continue
				}
				if seenFiles[file.Name] {
					*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`": duplicate file "`+file.Name+`"`)
				}
				seenFiles[file.Name] = true

				// Validate file-level metatags
				fileMetaSeen := make(map[string]bool)
				for _, m := range file.Metatags {
					if err := validateMetatagType(m.Type); err != nil {
						*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`", file "`+file.Name+`", metatag "`+m.Name+`": `+err.Error())
					}
					if m.Name == "" {
						*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`", file "`+file.Name+`": metatag name must be non-empty`)
					}
					if fileMetaSeen[m.Name] {
						*errors = append(*errors, `Relik "`+relik.Title+`", folder "`+folder.Name+`", file "`+file.Name+`": duplicate metatag "`+m.Name+`"`)
					}
					fileMetaSeen[m.Name] = true
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 1b. validateIndexer — indexer config + indexSearch fields
// ---------------------------------------------------------------------------

func validateIndexer(config *PipelineConfig, errors *[]string) {
	if config.Indexer != nil {
		if config.Indexer.ChunkTokens < 0 {
			*errors = append(*errors, `indexer: chunkTokens must be >= 0`)
		}
		if config.Indexer.Overlap < 0 {
			*errors = append(*errors, `indexer: overlap must be >= 0`)
		}
		switch config.Indexer.Scope {
		case "", "session", "run":
		default:
			*errors = append(*errors, `indexer: invalid scope "`+config.Indexer.Scope+`" (want "session" or "run")`)
		}
	}
	for _, step := range flattenNonGroupSteps(config.Steps) {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			if field.Kind != FieldIndexSearch {
				continue
			}
			if field.IndexSearch.Query == "" {
				*errors = append(*errors, `Step "`+step.ID+`": indexSearch field "`+field.IndexSearch.Name+`" requires a query`)
			}
			if field.IndexSearch.Limit < 0 || field.IndexSearch.Limit > 100 {
				*errors = append(*errors, `Step "`+step.ID+`": indexSearch field "`+field.IndexSearch.Name+`" limit must be 0-100`)
			}
		}
	}
}

// 1c. validateMailbox — mailbox config + mail post-step blocks
// ---------------------------------------------------------------------------

func validateMailbox(config *PipelineConfig, errors *[]string) {
	if config.Mailbox != nil && config.Mailbox.Drain != nil {
		if config.Mailbox.Drain.As == "" {
			*errors = append(*errors, `mailbox: drain requires an "as" injection name`)
		}
		if config.Mailbox.Drain.Limit < 0 || config.Mailbox.Drain.Limit > 500 {
			*errors = append(*errors, `mailbox: drain limit must be 0-500`)
		}
	}
	for _, base := range flattenNonGroupSteps(config.Steps) {
		if base == nil || base.Mail == nil {
			continue
		}
		if base.Mail.To == "" {
			*errors = append(*errors, `Step "`+base.ID+`": mail block requires "to"`)
		}
	}
}

// 1d. validateSchedule — schedule config
// ---------------------------------------------------------------------------

func validateSchedule(config *PipelineConfig, errors *[]string) {
	sc := config.Schedule
	if sc == nil {
		return
	}
	if len(sc.Entries) == 0 && sc.From == nil {
		*errors = append(*errors, `schedule: needs entries or the pointer form (from:)`)
	}
	seen := map[string]bool{}
	for _, e := range sc.Entries {
		if e.ID == "" {
			*errors = append(*errors, `schedule: entry id required`)
		}
		if seen[e.ID] {
			*errors = append(*errors, `schedule: duplicate entry id "`+e.ID+`"`)
		}
		seen[e.ID] = true
		if _, err := cronParse(e.Cron); err != nil {
			*errors = append(*errors, `schedule entry "`+e.ID+`": invalid cron "`+e.Cron+`"`)
		}
		if e.Tz != "" {
			if _, err := timeLoadLocation(e.Tz); err != nil {
				*errors = append(*errors, `schedule entry "`+e.ID+`": invalid timezone "`+e.Tz+`"`)
			}
		}
	}
}

// 2. validateRelikReferences — relikRead fields reference valid targets
// ---------------------------------------------------------------------------

func validateRelikReferences(steps []Step, reliks []RelikDecl, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			if field.Kind != FieldRelikRead {
				continue
			}
			sr := field.RelikRead
			target := sr.From

			relik := FindRelik(reliks, target.Relik)
			if relik == nil {
				*errors = append(*errors,
					`Step "`+step.ID+`": relikRead field "`+sr.Name+
						`" references non-existent relik "`+target.Relik+`"`)
				continue
			}

			folder := FindFolder(*relik, target.Folder)
			if folder == nil {
				*errors = append(*errors,
					`Step "`+step.ID+`": relikRead field "`+sr.Name+
						`" references non-existent folder "`+target.Folder+`" in relik "`+target.Relik+`"`)
				continue
			}

			// If file specified, check it exists
			if target.File != "" {
				file := FindFile(*folder, target.File)
				if file == nil {
					*errors = append(*errors,
						`Step "`+step.ID+`": relikRead field "`+sr.Name+
							`" references non-existent file "`+target.File+`" in folder "`+target.Folder+`"`)
					continue
				}

				// If metatag specified, check it's in the effective schema
				if target.Metatag != "" {
					if !systemMetatags[target.Metatag] {
						effMetas := EffectiveMetatags(*folder, *file)
						if FindMetatag(effMetas, target.Metatag) == nil {
							*errors = append(*errors,
								`Step "`+step.ID+`": relikRead field "`+sr.Name+
									`" references undeclared metatag "`+target.Metatag+
									`" on file "`+target.File+`"`)
						}
					}
				}
			} else if target.Metatag != "" {
				// Folder-level metatag read (no file specified) — metatag must exist
				// in at least one file's effective schema. System metatags (index, tree)
				// are always valid at folder level.
				if !systemMetatags[target.Metatag] {
					// Check if any file in the folder has this metatag in its effective schema
					found := false
					for _, f := range folder.Files {
						effMetas := EffectiveMetatags(*folder, f)
						if FindMetatag(effMetas, target.Metatag) != nil {
							found = true
							break
						}
					}
					if !found && len(folder.Files) > 0 {
						*errors = append(*errors,
							`Step "`+step.ID+`": relikRead field "`+sr.Name+
								`" references metatag "`+target.Metatag+
								`" across folder "`+target.Folder+`" but no file declares it`)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 3. validateJumpsAndActions — jump targets in if/then/else/onInvalid
// ---------------------------------------------------------------------------

func validateJumpsAndActions(steps []Step, stepMap map[string]Step, reliks []RelikDecl, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		// Check If block actions
		if step.If != nil {
			validateAction(step.ID, "then", step.If.Then, stepMap, reliks, errors)
			validateAction(step.ID, "else", step.If.Else, stepMap, reliks, errors)
		}
		// Check onInvalid action
		if step.OnInvalid != nil {
			validateAction(step.ID, "onInvalid", *step.OnInvalid, stepMap, reliks, errors)
		}
	}
}

func validateAction(stepID, branch string, action Action, stepMap map[string]Step, reliks []RelikDecl, errors *[]string) {
	if action.Kind != ActionJump {
		return
	}
	if action.Jump == nil {
		return
	}
	jt := *action.Jump

	// Validate passField.to references for INTRA-ladder jumps. For cross-ladder
	// jumps, the target step's field list isn't available at parse time —
	// validation is deferred to handoff-load-time (engine validates against
	// the fetched target config). For intra-ladder jumps, both caller and
	// target are in the same stepMap.
	if !jt.IsCrossLadder() && jt.StepID != "" && len(jt.PassFields) > 0 {
		targetStep, ok := stepMap[jt.StepID]
		if !ok {
			// Already reported by the existing stepId-exists check below.
			// Skip per-field validation; the missing-target error is enough.
		} else {
			validateIntraLadderPassFields(stepID, branch, jt, targetStep, stepMap, reliks, errors)
		}
	}
	// Validate passField.from references structurally (both intra and cross —
	// caller-side sources must resolve regardless of target type).
	for i, pf := range jt.PassFields {
		label := branch + " passFields[" + itoa(i) + "]"
		if pf.To == "" {
			*errors = append(*errors,
				`Step "`+stepID+`": `+label+` has empty "to" (target field name required)`)
		}
		validateFallbackFromSpec(stepID, label+".from", pf.From, stepMap, reliks, errors)
	}

	// Cross-ladder jumps: validate the ladderId is parseable
	if jt.IsCrossLadder() {
		if _, err := ParseLadderID(jt.LadderID); err != nil {
			*errors = append(*errors,
				`Step "`+stepID+`": `+branch+` jump has invalid ladderId "`+jt.LadderID+`": `+err.Error())
		}
		return
	}

	// Within-ladder jumps: validate stepId exists
	if jt.StepID != "" {
		if _, ok := stepMap[jt.StepID]; !ok {
			*errors = append(*errors,
				`Step "`+stepID+`": `+branch+` jump references non-existent step "`+jt.StepID+`"`)
		}
	}
}

// validateIntraLadderPassFields checks that every passField.to matches a declared
// field name on the target step. Static-time check; only possible because both
// caller and target are in the same stepMap.
func validateIntraLadderPassFields(stepID, branch string, jt JumpTarget, targetStep Step, stepMap map[string]Step, reliks []RelikDecl, errors *[]string) {
	base := targetStep.Base()
	if base == nil || base.Fields == nil || base.Fields.IsString {
		// Target has no field list (string fields config or nil). Every passField.to is unmatched.
		for i, pf := range jt.PassFields {
			*errors = append(*errors,
				`Step "`+stepID+`": `+branch+` passFields[`+itoa(i)+`].to "`+pf.To+
					`" targets step "`+jt.StepID+`" which has no declared field list`)
		}
		return
	}
	// Build set of declared field names on the target step.
	declared := make(map[string]bool, len(base.Fields.Fields))
	for _, f := range base.Fields.Fields {
		declared[fieldName(f)] = true
	}
	for i, pf := range jt.PassFields {
		if pf.To == "" {
			continue // already reported above
		}
		if !declared[pf.To] {
			*errors = append(*errors,
				`Step "`+stepID+`": `+branch+` passFields[`+itoa(i)+`].to "`+pf.To+
					`" does not match any field on step "`+jt.StepID+`"`)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. validateIfConditions — condition well-formedness
// ---------------------------------------------------------------------------

func validateIfConditions(steps []Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.If == nil {
			continue
		}
		validateCondition(step.ID, step.If.Condition, errors)
	}
}

// validateCondition recursively validates a condition (including compound
// and/or/not sub-conditions). Leaf checks range-ordering, set non-emptiness,
// JSON schema validity, and regex compilability.
func validateCondition(stepID string, c Condition, errors *[]string) {
	switch c.Kind {
	case CondIntegerRange:
		if c.IntegerRange[0] > c.IntegerRange[1] {
			*errors = append(*errors,
				`Step "`+stepID+`": if.integerRange has min (`+itoa(c.IntegerRange[0])+
					`) > max (`+itoa(c.IntegerRange[1])+`)`)
		}
	case CondNumberRange:
		if c.NumberRange[0] > c.NumberRange[1] {
			*errors = append(*errors,
				`Step "`+stepID+`": if.numberRange has min > max`)
		}
	case CondIn:
		if len(c.In) == 0 {
			*errors = append(*errors,
				`Step "`+stepID+`": if.in has empty array (must have at least 1 value)`)
		}
	case CondJSONMatches:
		if len(c.JSONMatches) == 0 {
			*errors = append(*errors,
				`Step "`+stepID+`": if.jsonMatches has empty schema`)
		}
		var probe any
		if err := json.Unmarshal(c.JSONMatches, &probe); err != nil {
			*errors = append(*errors,
				`Step "`+stepID+`": if.jsonMatches is not valid JSON: `+err.Error())
		}
	case CondMatches:
		if c.Matches == "" {
			*errors = append(*errors,
				`Step "`+stepID+`": if.matches has empty pattern`)
		}
		if _, err := regexp.Compile(c.Matches); err != nil {
			*errors = append(*errors,
				`Step "`+stepID+`": if.matches has invalid regex: `+err.Error())
		}
	case CondAnd:
		if len(c.And) == 0 {
			*errors = append(*errors,
				`Step "`+stepID+`": if.and has empty array (must have at least 1 condition)`)
		}
		for _, sub := range c.And {
			validateCondition(stepID, sub, errors)
		}
	case CondOr:
		if len(c.Or) == 0 {
			*errors = append(*errors,
				`Step "`+stepID+`": if.or has empty array (must have at least 1 condition)`)
		}
		for _, sub := range c.Or {
			validateCondition(stepID, sub, errors)
		}
	case CondNot:
		if c.Not == nil {
			*errors = append(*errors,
				`Step "`+stepID+`": if.not is missing its sub-condition`)
		} else {
			validateCondition(stepID, *c.Not, errors)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. validateDynamicSteps — dynamic: true constraints
// ---------------------------------------------------------------------------

func validateDynamicSteps(steps []Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if !step.Dynamic {
			continue
		}
		// Must have from: (DynamicFrom)
		if step.DynamicFrom == nil {
			*errors = append(*errors,
				`Step "`+step.ID+`": dynamic: true requires "from" to specify config source`)
		}
		// Should NOT declare its own systemPrompt/fields/nodes — those come from the loaded config.
		// We warn (not error) for now — these fields would be overwritten at runtime anyway.
		if step.SystemPrompt != "" {
			*errors = append(*errors,
				`Step "`+step.ID+`": dynamic step should not declare systemPrompt (overwritten by loaded config)`)
		}
		if step.Fields != nil && step.Fields.IsSet() {
			*errors = append(*errors,
				`Step "`+step.ID+`": dynamic step should not declare fields (overwritten by loaded config)`)
		}
		if step.Nodes != nil {
			*errors = append(*errors,
				`Step "`+step.ID+`": dynamic step should not declare nodes (overwritten by loaded config)`)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. validateAdaptiveKeys — codeExecution/relikRead/relikEdit/files validation
// ---------------------------------------------------------------------------

// validCodeRuntimes is the set of language runtimes the session executor can
// spin up.
var validCodeRuntimes = map[string]bool{
	"python": true,
	"node":   true,
	"bash":   true,
}

// validateAdaptiveKeys validates the four flat step-level adaptive keys
// (codeExecution:, relikRead:, relikEdit:, files:) and enforces the
// shared-loop field agreement when multiple keys coexist on one step.
func validateAdaptiveKeys(steps []Step, reliks []RelikDecl, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		id := step.ID

		// Track shared loop fields across declared keys to detect conflicts.
		var maxRoundsSeen map[string]int    // key name → maxRounds value
		maxRoundsSeen = make(map[string]int)
		var requestMatchSeen map[string]string // key name → raw requestMatch
		requestMatchSeen = make(map[string]string)

		// --- codeExecution ---
		if step.CodeExecution != nil {
			ce := step.CodeExecution
			if ce.MaxRounds <= 0 {
				*errors = append(*errors,
					`Step "`+id+`": codeExecution.maxRounds must be > 0 (got `+itoa(ce.MaxRounds)+`)`)
			}
			if ce.As == "" {
				*errors = append(*errors,
					`Step "`+id+`": codeExecution.as must be non-empty`)
			}
			if ce.Runtime != "" && !validCodeRuntimes[ce.Runtime] {
				*errors = append(*errors,
					`Step "`+id+`": codeExecution.runtime must be one of python|node|bash (got "`+ce.Runtime+`")`)
			}
			if len(ce.RequestMatch) == 0 {
				*errors = append(*errors,
					`Step "`+id+`": codeExecution.requestMatch must be a non-empty JSON Schema`)
			} else {
				var probe any
				if err := json.Unmarshal(ce.RequestMatch, &probe); err != nil {
					*errors = append(*errors,
						`Step "`+id+`": codeExecution.requestMatch is not valid JSON: `+err.Error())
				}
			}
			maxRoundsSeen["codeExecution"] = ce.MaxRounds
			requestMatchSeen["codeExecution"] = string(ce.RequestMatch)
		}

		// --- relikRead ---
		if step.RelikRead != nil {
			sr := step.RelikRead
			if sr.MaxRounds <= 0 {
				*errors = append(*errors,
					`Step "`+id+`": relikRead.maxRounds must be > 0 (got `+itoa(sr.MaxRounds)+`)`)
			}
			if sr.As == "" {
				*errors = append(*errors,
					`Step "`+id+`": relikRead.as must be non-empty`)
			}
			if len(sr.RequestMatch) == 0 {
				*errors = append(*errors,
					`Step "`+id+`": relikRead.requestMatch must be a non-empty JSON Schema`)
			} else {
				var probe any
				if err := json.Unmarshal(sr.RequestMatch, &probe); err != nil {
					*errors = append(*errors,
						`Step "`+id+`": relikRead.requestMatch is not valid JSON: `+err.Error())
				}
			}
			// Validate source relik/folder exists
			if sr.From.Relik == "" {
				*errors = append(*errors,
					`Step "`+id+`": relikRead.from.relik must be non-empty`)
			} else {
				sd := FindRelik(reliks, sr.From.Relik)
				if sd == nil {
					*errors = append(*errors,
						`Step "`+id+`": relikRead.from references non-existent relik "`+sr.From.Relik+`"`)
				} else if sr.From.Folder != "" {
					if FindFolder(*sd, sr.From.Folder) == nil {
						*errors = append(*errors,
							`Step "`+id+`": relikRead.from references non-existent folder "`+sr.From.Folder+`"`)
					}
				}
			}
			maxRoundsSeen["relikRead"] = sr.MaxRounds
			requestMatchSeen["relikRead"] = string(sr.RequestMatch)
		}

		// --- relikEdit ---
		if step.RelikEdit != nil {
			se := step.RelikEdit
			if se.MaxRounds <= 0 {
				*errors = append(*errors,
					`Step "`+id+`": relikEdit.maxRounds must be > 0 (got `+itoa(se.MaxRounds)+`)`)
			}
			if se.As == "" {
				*errors = append(*errors,
					`Step "`+id+`": relikEdit.as must be non-empty`)
			}
			if len(se.RequestMatch) == 0 {
				*errors = append(*errors,
					`Step "`+id+`": relikEdit.requestMatch must be a non-empty JSON Schema`)
			} else {
				var probe any
				if err := json.Unmarshal(se.RequestMatch, &probe); err != nil {
					*errors = append(*errors,
						`Step "`+id+`": relikEdit.requestMatch is not valid JSON: `+err.Error())
				}
			}
			// Validate target relik/folder exists and is writable
			if se.To.Relik == "" {
				*errors = append(*errors,
					`Step "`+id+`": relikEdit.to.relik must be non-empty`)
			} else {
				sd := FindRelik(reliks, se.To.Relik)
				if sd == nil {
					*errors = append(*errors,
						`Step "`+id+`": relikEdit.to references non-existent relik "`+se.To.Relik+`"`)
				} else if se.To.Folder != "" {
					fd := FindFolder(*sd, se.To.Folder)
					if fd == nil {
						*errors = append(*errors,
							`Step "`+id+`": relikEdit.to references non-existent folder "`+se.To.Folder+`"`)
					} else if fd.Access == FolderAccessRead {
						*errors = append(*errors,
							`Step "`+id+`": relikEdit.to folder "`+se.To.Folder+`" is read-only — cannot be edited`)
					}
				}
			}
			maxRoundsSeen["relikEdit"] = se.MaxRounds
			requestMatchSeen["relikEdit"] = string(se.RequestMatch)
		}

		// --- files ---
		if step.Files != nil {
			fc := step.Files
			if fc.MaxRounds <= 0 {
				*errors = append(*errors,
					`Step "`+id+`": files.maxRounds must be > 0 (got `+itoa(fc.MaxRounds)+`)`)
			}
			if fc.As == "" {
				*errors = append(*errors,
					`Step "`+id+`": files.as must be non-empty`)
			}
			if len(fc.RequestMatch) == 0 {
				*errors = append(*errors,
					`Step "`+id+`": files.requestMatch must be a non-empty JSON Schema`)
			} else {
				var probe any
				if err := json.Unmarshal(fc.RequestMatch, &probe); err != nil {
					*errors = append(*errors,
						`Step "`+id+`": files.requestMatch is not valid JSON: `+err.Error())
				}
			}
			maxRoundsSeen["files"] = fc.MaxRounds
			requestMatchSeen["files"] = string(fc.RequestMatch)
		}

		// --- configEdit ---
		if step.ConfigEdit != nil {
			ced := step.ConfigEdit
			if ced.MaxRounds <= 0 {
				*errors = append(*errors,
					`Step "`+id+`": configEdit.maxRounds must be > 0 (got `+itoa(ced.MaxRounds)+`)`)
			}
			if ced.As == "" {
				*errors = append(*errors,
					`Step "`+id+`": configEdit.as must be non-empty`)
			}
			if len(ced.RequestMatch) == 0 {
				*errors = append(*errors,
					`Step "`+id+`": configEdit.requestMatch must be a non-empty JSON Schema`)
			} else {
				var probe any
				if err := json.Unmarshal(ced.RequestMatch, &probe); err != nil {
					*errors = append(*errors,
						`Step "`+id+`": configEdit.requestMatch is not valid JSON: `+err.Error())
				}
			}
			maxRoundsSeen["configEdit"] = ced.MaxRounds
			requestMatchSeen["configEdit"] = string(ced.RequestMatch)
		}

		// --- shared-loop field agreement ---
		// When two or more adaptive keys coexist, maxRounds and requestMatch
		// must agree (same value) or be declared on only one key. Different
		// values across keys is a config error — the keys share ONE loop.
		if len(maxRoundsSeen) > 1 {
			var first string
			var firstVal int
			for k, v := range maxRoundsSeen {
				if first == "" {
					first = k
					firstVal = v
					continue
				}
				if v != firstVal {
					*errors = append(*errors,
						`Step "`+id+`": `+k+`.maxRounds (`+itoa(v)+`) conflicts with `+
							first+`.maxRounds (`+itoa(firstVal)+`) — adaptive keys share one loop; declare maxRounds on one key only`)
				}
			}
		}
		if len(requestMatchSeen) > 1 {
			var first string
			var firstVal string
			for k, v := range requestMatchSeen {
				if first == "" {
					first = k
					firstVal = v
					continue
				}
				if v != firstVal {
					*errors = append(*errors,
						`Step "`+id+`": `+k+`.requestMatch conflicts with `+
							first+`.requestMatch — adaptive keys share one loop; declare requestMatch on one key only`)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 7. validateRelikWrites — relikWrite config validation
// ---------------------------------------------------------------------------

func validateRelikWrites(steps []Step, reliks []RelikDecl, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.RelikWrite == nil {
			continue
		}
		sw := *step.RelikWrite

		// Target must reference a declared relik and folder
		if sw.To.Relik == "" {
			*errors = append(*errors,
				`Step "`+step.ID+`": relikWrite.to.relik must be non-empty`)
		} else {
			relik := FindRelik(reliks, sw.To.Relik)
			if relik == nil {
				*errors = append(*errors,
					`Step "`+step.ID+`": relikWrite.to references non-existent relik "`+sw.To.Relik+`"`)
			} else if sw.To.Folder != "" {
				if FindFolder(*relik, sw.To.Folder) == nil {
					*errors = append(*errors,
						`Step "`+step.ID+`": relikWrite.to references non-existent folder "`+sw.To.Folder+`"`)
				}
			}
		}

		// mergeByKey requires key
		if sw.On == WriteMergeByKey && sw.Key == "" {
			*errors = append(*errors,
				`Step "`+step.ID+`": relikWrite.on "mergeByKey" requires "key" field`)
		}

		// key should only be present with mergeByKey
		if sw.Key != "" && sw.On != WriteMergeByKey {
			*errors = append(*errors,
				`Step "`+step.ID+`": relikWrite.key is only valid with on: "mergeByKey"`)
		}

		// match must be non-empty JSON Schema
		if len(sw.Match) == 0 {
			*errors = append(*errors,
				`Step "`+step.ID+`": relikWrite.match must be a non-empty JSON Schema`)
		} else {
			var probe any
			if err := json.Unmarshal(sw.Match, &probe); err != nil {
				*errors = append(*errors,
					`Step "`+step.ID+`": relikWrite.match is not valid JSON: `+err.Error())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 8. validateExecutionBudget — Path B configurable ceilings
// ---------------------------------------------------------------------------

// Platform maximums — enforced at validation as a sanity check.
const (
	platformMaxSpend     = 50.0 // dollars
	platformMaxHops      = 10000
	platformMaxLlmCalls  = 5000
	platformMaxRecursion = 20
)

func validateExecutionBudget(eb *ExecutionBudget, errors *[]string) {
	if eb == nil {
		return
	}
	if eb.MaxSpend != nil {
		if *eb.MaxSpend <= 0 {
			*errors = append(*errors, `executionBudget.maxSpend must be > 0`)
		}
		if *eb.MaxSpend > platformMaxSpend {
			*errors = append(*errors,
				`executionBudget.maxSpend (`+ftoa(*eb.MaxSpend)+`) exceeds platform max (`+ftoa(platformMaxSpend)+`)`)
		}
	}
	if eb.MaxHops != nil {
		if *eb.MaxHops <= 0 {
			*errors = append(*errors, `executionBudget.maxHops must be > 0`)
		}
		if *eb.MaxHops > platformMaxHops {
			*errors = append(*errors,
				`executionBudget.maxHops (`+itoa(*eb.MaxHops)+`) exceeds platform max (`+itoa(platformMaxHops)+`)`)
		}
	}
	if eb.MaxLlmCalls != nil {
		if *eb.MaxLlmCalls <= 0 {
			*errors = append(*errors, `executionBudget.maxLlmCalls must be > 0`)
		}
		if *eb.MaxLlmCalls > platformMaxLlmCalls {
			*errors = append(*errors,
				`executionBudget.maxLlmCalls (`+itoa(*eb.MaxLlmCalls)+`) exceeds platform max (`+itoa(platformMaxLlmCalls)+`)`)
		}
	}
	if eb.MaxRecursion != nil {
		if *eb.MaxRecursion < 0 {
			*errors = append(*errors, `executionBudget.maxRecursion must be >= 0`)
		}
		if *eb.MaxRecursion > platformMaxRecursion {
			*errors = append(*errors,
				`executionBudget.maxRecursion (`+itoa(*eb.MaxRecursion)+`) exceeds platform max (`+itoa(platformMaxRecursion)+`)`)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. validateNoContinueIfConflict — can't have both continueIf and if
// ---------------------------------------------------------------------------

func validateNoContinueIfConflict(steps []Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.ContinueIf != "" && step.If != nil {
			*errors = append(*errors,
				`Step "`+step.ID+`": cannot declare both "continueIf" (deprecated) and "if" — use only "if"`)
		}
	}
}

// ---------------------------------------------------------------------------
// 11. validateManifestViews — manifest from: views reference valid reliks/folders
// ---------------------------------------------------------------------------

func validateManifestViews(config *PipelineConfig, errors *[]string) {
	validateViewList := func(stepID string, views []ManifestView) {
		for i, view := range views {
			label := "defaultManifest"
			if stepID != "" {
				label = `Step "` + stepID + `" manifest`
			}
			_ = i
			switch view.Kind {
			case ManifestViewIndex, ManifestViewTree:
				if view.Relik == "" {
					*errors = append(*errors, label+": relik is required for index/tree views")
					continue
				}
				relik := FindRelik(config.Reliks, view.Relik)
				if relik == nil {
					*errors = append(*errors,
						label+`: references non-existent relik "`+view.Relik+`"`)
					continue
				}
				if view.Folder != "" {
					if FindFolder(*relik, view.Folder) == nil {
						*errors = append(*errors,
							label+`: references non-existent folder "`+view.Folder+`" in relik "`+view.Relik+`"`)
					}
				}
			}
		}
	}

	validateViewList("", config.DefaultManifest)

	nonGroupSteps := flattenNonGroupSteps(config.Steps)
	for _, step := range nonGroupSteps {
		if step.Fields == nil || step.Fields.IsString {
			continue
		}
		for _, field := range step.Fields.Fields {
			if field.Kind != FieldManifest {
				continue
			}
			if field.Manifest.From != nil {
				validateViewList(step.ID, []ManifestView{*field.Manifest.From})
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 10. validateFallbacks — fallback: declarations reference valid sources
// ---------------------------------------------------------------------------

// validateFallbacks walks every scalar-source field/dynamic loader that
// supports `fallback:` and checks the declared FromSpec resolves structurally:
//   - { stepId: X }  → X exists in stepMap
//   - { relik: ... } → relik + folder declared
//   - "input.X"      → always structurally valid (runtime decides if X is set)
//   - "previous", "output", "literal" → always valid
func validateFallbacks(steps []Step, reliks []RelikDecl, stepMap map[string]Step, errors *[]string) {
	nonGroupSteps := flattenNonGroupSteps(steps)
	for _, step := range nonGroupSteps {
		if step.Fields != nil && !step.Fields.IsString {
			for _, field := range step.Fields.Fields {
				switch field.Kind {
				case FieldText:
					if field.Text.Fallback != nil {
						validateFallbackFromSpec(step.ID, fieldName(field), *field.Text.Fallback, stepMap, reliks, errors)
					}
				case FieldIngest:
					if field.Ingest.Fallback != nil {
						validateFallbackFromSpec(step.ID, fieldName(field), *field.Ingest.Fallback, stepMap, reliks, errors)
					}
				case FieldRelikRead:
					if field.RelikRead.Fallback != nil {
						validateFallbackFromSpec(step.ID, fieldName(field), *field.RelikRead.Fallback, stepMap, reliks, errors)
					}
				}
			}
		}
		// Dynamic step fallback (DynamicFromFallback on BaseStep).
		if step.DynamicFromFallback != nil {
			validateFallbackFromSpec(step.ID, "fromFallback", *step.DynamicFromFallback, stepMap, reliks, errors)
		}
	}
}

// validateFallbackFromSpec checks one fallback FromSpec for structural validity.
func validateFallbackFromSpec(stepID, fieldLabel string, spec FromSpec, stepMap map[string]Step, reliks []RelikDecl, errors *[]string) {
	switch spec.Kind {
	case FromKeywordPrevious, FromKeywordOutput, FromInputKey, FromLiteral:
		// Always structurally valid. Runtime decides if the source is populated.
		return
	case FromStepRef:
		if spec.Step.StepID == "" {
			*errors = append(*errors,
				`Step "`+stepID+`": `+fieldLabel+` has stepId reference with empty stepId`)
			return
		}
		if _, ok := stepMap[spec.Step.StepID]; !ok {
			*errors = append(*errors,
				`Step "`+stepID+`": `+fieldLabel+` references non-existent step "`+spec.Step.StepID+`"`)
		}
	case FromRelikRef:
		if spec.Relik.Relik == "" {
			*errors = append(*errors,
				`Step "`+stepID+`": `+fieldLabel+` has relik reference with empty relik title`)
			return
		}
		relik := FindRelik(reliks, spec.Relik.Relik)
		if relik == nil {
			*errors = append(*errors,
				`Step "`+stepID+`": `+fieldLabel+` references non-existent relik "`+spec.Relik.Relik+`"`)
			return
		}
		if spec.Relik.Folder != "" {
			if FindFolder(*relik, spec.Relik.Folder) == nil {
				*errors = append(*errors,
					`Step "`+stepID+`": `+fieldLabel+` references non-existent folder "`+spec.Relik.Folder+`" in relik "`+spec.Relik.Relik+`"`)
			}
		}
	}
}

// cronParse/timeLoadLocation isolate the cron + tz deps for validation.
func cronParse(spec string) (cron.Schedule, error) { return cron.ParseStandard(spec) }

func timeLoadLocation(tz string) (*time.Location, error) { return time.LoadLocation(tz) }
