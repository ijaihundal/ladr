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

// Package engine — the flat adaptive-key bounded multi-turn loop.
//
// ExecuteAdaptiveLoop is the unified dispatch for the four flat step-level keys:
// codeExecution:, relikRead:, relikEdit:, and files:. Each turn the LLM emits
// output; if it matches the shared requestMatch schema the engine parses it as
// a tool call, dispatches to the matching tool, injects the result as a field,
// and continues. A non-matching output ends the loop (final answer). MaxRounds
// guarantees termination.
//
// When multiple adaptive keys are present on a step, they share ONE loop — the
// shared maxRounds and requestMatch are read from whichever key declares them
// (validation enforces they agree). Dispatch is by property name:
//
//	code     → codeExecution
//	request  → relikRead
//	edit     → relikEdit
//	read/write/list → files
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// adaptiveLoopConfig is the effective shared-loop config extracted from the
// declared flat keys. Built by buildAdaptiveLoopConfig.
type adaptiveLoopConfig struct {
	maxRounds    int
	requestMatch json.RawMessage
	autoPrompt   *bool // nil → true (inject defaults)
}

// ExecuteAdaptiveLoop runs the bounded adaptive loop for a step's flat keys.
//
// Returns the final (non-tool-call) output, which becomes the node's output.
// It performs its own LLM calls via the provided llmCall closure (so the caller
// controls budget accounting + provider wiring uniformly). When any adaptive key
// is declared, this loop IS the dispatch — the caller must not also call
// io.ExecuteLlmCall.
//
// Parameters:
//   - ce, sr, se, fs, ced: the five optional flat config pointers
//     (codeExecution, relikRead, relikEdit, files, configEdit)
//   - initialFields: the step's resolved fields
//   - systemPrompt: the step's system prompt
//   - io, store, runtime: engine I/O + relik store + runtime context
//   - stepID, groupID, nodeNumber: for event publishing + execution scoping
//   - llmCall: the counted, ceiling-checked LLM-call seam
func ExecuteAdaptiveLoop(
	ctx context.Context,
	ce *ladr.CodeExecutionConfig,
	sr *ladr.RelikReadConfig,
	se *ladr.RelikEditConfig,
	fs *ladr.FilesConfig,
	ced *ladr.ConfigEditConfig,
	initialFields []ResolvedField,
	systemPrompt string,
	io EngineIO,
	store relik.Store,
	runtime *ExecutionRuntimeContext,
	stepID string,
	groupID string,
	nodeNumber *int,
	llmCall func(ctx context.Context, userPrompt string) (string, error),
) (string, error) {
	loopCfg := buildAdaptiveLoopConfig(ce, sr, se, fs, ced)

	// Effective system prompt: author's prompt + auto-injected tool spec (unless
	// the author opted out via autoPrompt: false).
	effectiveSys := systemPrompt
	if loopCfg.autoPrompt == nil || *loopCfg.autoPrompt {
		effectiveSys = appendAdaptiveToolSpec(systemPrompt, ce, sr, se, fs, ced)
	}

	currentFields := initialFields
	var allRetrieved []RetrievedDoc // dedup across rounds for relikRead
	dedup := map[string]bool{}
	editApplied := false // set once a relikEdit call succeeds

	for round := 1; round <= loopCfg.maxRounds; round++ {
		userPrompt := AssemblePrompt(currentFields, effectiveSys)
		output, err := llmCall(ctx, userPrompt)
		if err != nil {
			return "", err
		}

		// Does the output match the shared requestMatch schema (→ tool call)?
		matched, call, err := parseToolCall(output, loopCfg.requestMatch)
		if err != nil || !matched {
			// A relikEdit-declared step that has not applied its edit may not
			// end here: a silent no-write step is how memory edits get lost.
			// Give the model shape feedback and another round; past maxRounds,
			// end the step loudly instead of passing for a final answer.
			requireEdit := se == nil || se.RequireEdit == nil || *se.RequireEdit
			if se != nil && requireEdit && !editApplied {
				if round < loopCfg.maxRounds {
					currentFields = append(currentFields, ResolvedField{Name: "ToolError",
						Value: "(no edit has been applied yet — this step must emit an edit call per the relikEdit tool spec before any final answer; re-emit {\"edit\": {...}} now)"})
					continue
				}
				_ = io.PublishEvent(ctx, PublishEventArgs{
					InstanceID: runtime.InstanceID,
					Type:       EventError,
					Data: map[string]any{
						"message":  "relikEdit: step ended without applying any edit (final output never matched the edit-call shape)",
						"nonFatal": true,
						"stepId":   stepID,
					},
				})
			}
			// Non-matching output = final answer. Done.
			return output, nil
		}

		// Dispatch to the matching tool and build the field to inject next round.
		var injected *ResolvedField
		switch {
		case ce != nil && (call.Code != "" || call.Action != ""):
			injected, err = runCodeToolAdaptive(ctx, ce, call, io, runtime, stepID, groupID, nodeNumber)
		case sr != nil && len(call.Request) > 0:
			injected, err = runRetrieveToolAdaptive(ctx, sr, call.Request, store, dedup, round, &allRetrieved)
		case se != nil && len(call.Edit) > 0:
			injected, err = runRelikEditToolAdaptive(ctx, se, call.Edit, store, runtime, stepID)
			if err == nil {
				editApplied = true
			}
		case fs != nil && (len(call.Read) > 0 || len(call.Write) > 0 || len(call.List) > 0):
			injected, err = runFilesToolAdaptive(ctx, fs, call, io, runtime, stepID, groupID, nodeNumber)
		case ced != nil && len(call.ConfigEdit) > 0:
			injected, err = runConfigEditToolAdaptive(ctx, ced, call.ConfigEdit, runtime, stepID)
		default:
			// Matched the schema but no tool could handle it (e.g. code present but
			// codeExecution not declared). Treat as a malformed call: inject a hint
			// and let the LLM correct itself rather than silently looping.
			injected = &ResolvedField{Name: "ToolError", Value: "(matched requestMatch but no enabled tool handles this shape)"}
		}
		if err != nil {
			// Tool infra failure (timeout, runtime missing, device denied).
			// Surface as feedback, don't kill the ladder — the LLM may recover
			// or produce a final answer.
			_ = io.PublishEvent(ctx, PublishEventArgs{
				InstanceID: runtime.InstanceID,
				Type:       EventError,
				Data:       map[string]any{"message": "tool call failed: " + err.Error(), "nonFatal": true, "stepId": stepID},
			})
			injected = &ResolvedField{Name: "ToolError", Value: "(tool call failed: " + err.Error() + ")"}
		}

		currentFields = append(currentFields, *injected)

		// maxRounds exhausted → force a final answer on the next call.
		if round == loopCfg.maxRounds {
			forced := AssemblePrompt(currentFields, effectiveSys) +
				"\n\nTool-call budget exhausted. Produce your final answer now."
			final, ferr := llmCall(ctx, forced)
			if ferr != nil {
				return "", ferr
			}
			return final, nil
		}
	}

	// Unreachable: the loop always returns on a non-match or at maxRounds.
	return "", fmt.Errorf("adaptive loop exited without producing output (step %s)", stepID)
}

// buildAdaptiveLoopConfig extracts the effective shared-loop config from the
// declared flat keys. The shared fields (maxRounds, requestMatch, autoPrompt)
// must agree across all declared keys — validation enforces this at config load
// time. Here we just read them from whichever key is present (preferring the
// first declared in order: codeExecution, relikRead, relikEdit, files).
func buildAdaptiveLoopConfig(ce *ladr.CodeExecutionConfig, sr *ladr.RelikReadConfig, se *ladr.RelikEditConfig, fs *ladr.FilesConfig, ced *ladr.ConfigEditConfig) adaptiveLoopConfig {
	var cfg adaptiveLoopConfig

	// Read shared fields from whichever key declares them. Since validation
	// enforces agreement, reading from any one is correct. Prefer the first
	// non-nil in a stable order.
	switch {
	case ce != nil:
		cfg.maxRounds = ce.MaxRounds
		cfg.requestMatch = ce.RequestMatch
		cfg.autoPrompt = ce.AutoPrompt
	case sr != nil:
		cfg.maxRounds = sr.MaxRounds
		cfg.requestMatch = sr.RequestMatch
		cfg.autoPrompt = sr.AutoPrompt
	case se != nil:
		cfg.maxRounds = se.MaxRounds
		cfg.requestMatch = se.RequestMatch
		cfg.autoPrompt = se.AutoPrompt
	case fs != nil:
		cfg.maxRounds = fs.MaxRounds
		cfg.requestMatch = fs.RequestMatch
		cfg.autoPrompt = fs.AutoPrompt
	case ced != nil:
		// configEdit-only steps read the shared config from configEdit —
		// without this case they silently fell through to zero-value config
		// (no rounds, no schema) and the loop never ran.
		cfg.maxRounds = ced.MaxRounds
		cfg.requestMatch = ced.RequestMatch
		cfg.autoPrompt = ced.AutoPrompt
	}

	return cfg
}

// runCodeToolAdaptive dispatches a code-execution call using the flat
// CodeExecutionConfig. codeExecution IS terminal access: a plain call runs in
// a terminal (call.Terminal names one; "" → the ladder's default), a call
// with Background starts a long-running command (immediate receipt), and a
// call with Action (peek/kill/status) addresses the terminal itself.
// Publishes tool-call/tool-result events, runs the call via io.ExecuteCode,
// and returns the result as an injected field.
func runCodeToolAdaptive(
	ctx context.Context,
	ce *ladr.CodeExecutionConfig,
	call toolCall,
	io EngineIO,
	runtime *ExecutionRuntimeContext,
	stepID, groupID string,
	nodeNumber *int,
) (*ResolvedField, error) {
	rt := ce.Runtime
	if call.Language != "" {
		rt = call.Language
	}
	_ = io.PublishEvent(ctx, PublishEventArgs{
		InstanceID: runtime.InstanceID,
		Type:       EventToolCall,
		Data: map[string]any{
			"stepId":    stepID,
			"tool":      "codeExecution",
			"runtime":   rt,
			"codeLen":   len(call.Code),
			"terminal":  call.Terminal,
			"background": call.Background,
			"action":    call.Action,
			"loopRef":   runtime.LoopIndex,
		},
	})
	res, err := io.ExecuteCode(ctx, ExecuteCodeParams{
		InstanceID: runtime.InstanceID,
		StepID:     stepID,
		GroupID:    groupID,
		LoopIndex:  runtime.LoopIndex,
		NodeNumber: nodeNumber,
		Depth:      runtime.Depth,
		Runtime:    rt,
		Code:       call.Code,
		LadderID:   ladderIdentityOf(runtime.Config),
		Terminal:   call.Terminal,
		Background: call.Background,
		Action:     call.Action,
	})
	if err != nil {
		return nil, err
	}
	_ = io.PublishEvent(ctx, PublishEventArgs{
		InstanceID: runtime.InstanceID,
		Type:       EventToolResult,
		Data: map[string]any{
			"stepId":   stepID,
			"tool":     "codeExecution",
			"exitCode": res.ExitCode,
			"outLen":   len(res.Output),
			"action":   call.Action,
			"terminal": res.Terminal,
		},
	})
	name := ce.As
	if name == "" {
		name = "ComputedResult"
	}

	// Terminal lifecycle calls render a receipt instead of raw stdout.
	value := res.Output
	switch {
	case call.Action == "status":
		if len(res.Terminals) == 0 {
			value = "(no terminals open)"
		} else {
			b, _ := json.MarshalIndent(res.Terminals, "", "  ")
			value = string(b)
		}
	case call.Action == "peek":
		state := "RUNNING"
		if !res.Running {
			state = fmt.Sprintf("EXITED (code %d)", res.ExitCode)
		}
		out := res.Stdout
		if out == "" {
			out = "(no new output)"
		}
		value = fmt.Sprintf("[terminal %s] %s\n%s", terminalName(call.Terminal), state, out)
		if res.Truncated {
			value += "\n(older scrollback was evicted — output shown starts mid-stream)"
		}
	case call.Action == "kill":
		value = fmt.Sprintf("[terminal %s] killed", terminalName(call.Terminal))
	case call.Background:
		value = fmt.Sprintf("[terminal %s] command started in background — running; peek with {\"action\":\"peek\",\"terminal\":\"%s\"}", terminalName(res.Terminal), terminalName(res.Terminal))
	default:
		// plain foreground code run → res.Output
	}
	if res.Notice != "" {
		value += "\n(notice: " + res.Notice + ")"
	}
	return &ResolvedField{Name: name, Value: value}, nil
}

// terminalName resolves a call's terminal reference for receipt text (empty
// means the ladder's default terminal).
func terminalName(t string) string {
	if t == "" {
		return "default"
	}
	return t
}

// runRetrieveToolAdaptive dispatches a relik-retrieval call using the flat
// RelikReadConfig. Reuses executeRetrievalRequest + dedup so the adaptive
// relikRead behaves identically to the old standalone retrieve path.
func runRetrieveToolAdaptive(
	ctx context.Context,
	sr *ladr.RelikReadConfig,
	requestRaw json.RawMessage,
	store relik.Store,
	dedup map[string]bool,
	round int,
	allRetrieved *[]RetrievedDoc,
) (*ResolvedField, error) {
	var req ladr.RetrievalRequest
	if err := json.Unmarshal(requestRaw, &req); err != nil {
		return nil, fmt.Errorf("relikRead: malformed request: %w", err)
	}
	docs := executeRetrievalRequest(&req, sr.From, store, dedup, round)
	*allRetrieved = append(*allRetrieved, docs...)
	name := sr.As
	if name == "" {
		name = "RetrievedDocuments"
	}
	return &ResolvedField{Name: name, Value: formatRetrievedDocs(docs)}, nil
}

// runRelikEditToolAdaptive dispatches a surgical-edit call using the flat
// RelikEditConfig. Emits edit ops against the declared folder and returns the
// EditReceipt as an injected field.
func runRelikEditToolAdaptive(
	ctx context.Context,
	se *ladr.RelikEditConfig,
	editRaw json.RawMessage,
	store relik.Store,
	runtime *ExecutionRuntimeContext,
	stepID string,
) (*ResolvedField, error) {
	var call editCall
	if err := json.Unmarshal(editRaw, &call); err != nil {
		return nil, fmt.Errorf("relikEdit: malformed edit: %w", err)
	}
	if call.File == "" {
		return nil, fmt.Errorf("relikEdit: \"file\" is required")
	}

	ops := call.Ops
	if len(ops) == 0 && call.Op != "" {
		ops = []relik.EditOp{{
			Op:      relik.EditOpKind(call.Op),
			Anchor:  call.Anchor,
			Content: call.Content,
			Start:   call.Start,
			End:     call.End,
		}}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("relikEdit: at least one op is required")
	}

	receipt, err := store.EditFile(se.To.Relik, se.To.Folder, relik.EditFileParams{
		File:            call.File,
		Ops:             ops,
		ExpectedVersion: call.ExpectedVersion,
	})
	if err != nil {
		return nil, err
	}

	receiptJSON, _ := json.MarshalIndent(receipt, "", "  ")

	name := se.As
	if name == "" {
		name = "EditReceipt"
	}
	return &ResolvedField{Name: name, Value: string(receiptJSON)}, nil
}

// filesCall is the parsed payload of a files: dispatch property.
type filesCall struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append"`
	Glob    string `json:"glob"`
}

// runFilesToolAdaptive dispatches a files: tool call (read/write/list). Publishes
// tool-call/tool-result events, runs the op via io.FilesOp, and returns the
// result as an injected field. Path scoping is the IO implementation's job —
// the engine core only knows relative paths.
func runFilesToolAdaptive(
	ctx context.Context,
	fc *ladr.FilesConfig,
	call toolCall,
	io EngineIO,
	runtime *ExecutionRuntimeContext,
	stepID, groupID string,
	nodeNumber *int,
) (*ResolvedField, error) {
	var op string
	var raw json.RawMessage
	var payload filesCall
	switch {
	case len(call.Read) > 0:
		op, raw = "read", call.Read
	case len(call.Write) > 0:
		op, raw = "write", call.Write
	default:
		op, raw = "list", call.List
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("files: malformed %s: %w", op, err)
	}
	if op != "list" && payload.Path == "" {
		return nil, fmt.Errorf("files: %s requires \"path\"", op)
	}

	_ = io.PublishEvent(ctx, PublishEventArgs{
		InstanceID: runtime.InstanceID,
		Type:       EventToolCall,
		Data: map[string]any{
			"stepId":  stepID,
			"tool":    "files",
			"op":      op,
			"path":    payload.Path,
			"loopRef": runtime.LoopIndex,
		},
	})
	res, err := io.FilesOp(ctx, FilesOpParams{
		InstanceID: runtime.InstanceID,
		StepID:     stepID,
		GroupID:    groupID,
		LoopIndex:  runtime.LoopIndex,
		NodeNumber: nodeNumber,
		Depth:      runtime.Depth,
		Op:         op,
		Path:       payload.Path,
		Content:    payload.Content,
		Glob:       payload.Glob,
		Append:     payload.Append,
		LadderID:   ladderIdentityOf(runtime.Config),
	})
	if err != nil {
		return nil, err
	}

	// Render the result as a compact, prompt-injectable string per op.
	var value string
	switch op {
	case "read":
		value = res.Content
		if res.Truncated {
			value += "\n…(truncated)"
		}
	case "write":
		value = fmt.Sprintf("(wrote %d bytes to %s)", res.Bytes, payload.Path)
	default: // list
		if len(res.Entries) == 0 {
			value = "(no matches)"
		} else {
			value = strings.Join(res.Entries, "\n")
		}
	}
	_ = io.PublishEvent(ctx, PublishEventArgs{
		InstanceID: runtime.InstanceID,
		Type:       EventToolResult,
		Data: map[string]any{
			"stepId": stepID,
			"tool":   "files",
			"op":     op,
			"outLen": len(value),
		},
	})
	name := fc.As
	if name == "" {
		name = "FileResult"
	}
	return &ResolvedField{Name: name, Value: value}, nil
}

// ladderIdentityOf returns the ladder's stable id when present, falling back to
// its name — the attribution key threaded through device-channel envelopes for
// per-ladder grant checks on the federated path (inert locally). Resolved in
// one place so the choice can't drift.
func ladderIdentityOf(cfg *ladr.PipelineConfig) string {
	if cfg == nil {
		return ""
	}
	if cfg.ID != "" {
		return cfg.ID
	}
	return cfg.Name
}

// appendAdaptiveToolSpec renders the auto-injected tool-usage block from the
// declared flat keys and appends it to the system prompt. This is the "sane
// default" half of the auto-prompt feature; the author can override per-key
// via `prompt:` or disable globally via `autoPrompt: false`.
func appendAdaptiveToolSpec(systemPrompt string, ce *ladr.CodeExecutionConfig, sr *ladr.RelikReadConfig, se *ladr.RelikEditConfig, fs *ladr.FilesConfig, ced *ladr.ConfigEditConfig) string {
	var b strings.Builder
	b.WriteString("\n\n[Available tools — emit JSON to call one; otherwise answer in prose]")
	if ce != nil {
		rt := ce.Runtime
		if rt == "" {
			rt = "python"
		}
		as := ce.As
		if as == "" {
			as = "ComputedResult"
		}
		if ce.Prompt != "" {
			b.WriteString("\n- codeExecution (" + rt + "): " + ce.Prompt)
		} else {
			b.WriteString("\n- codeExecution (" + rt + "): emit {\"code\": \"...\"} to run code → result injected as \"" + as + "\"")
			b.WriteString("\n  Terminals: code runs in a persistent terminal that survives across steps until killed. Add \"terminal\": \"name\" to choose/open one (omit → \"default\"); when several terminals are open, always name the one you mean. Add \"background\": true to start a long-running command (server, watcher) — you get an immediate receipt and output accumulates in the terminal's scrollback. Emit {\"action\": \"peek\", \"terminal\": \"name\"} for new scrollback (plus running/exited + exit code), {\"action\": \"kill\", \"terminal\": \"name\"} to kill a terminal's whole process group, {\"action\": \"status\"} to list your terminals. A busy terminal (background command running) rejects new code — peek or kill first, or use another terminal. These fields require your ladder's requestMatch schema to allow them.")
		}
	}
	if sr != nil {
		as := sr.As
		if as == "" {
			as = "RetrievedDocuments"
		}
		if sr.Prompt != "" {
			b.WriteString("\n- relikRead: " + sr.Prompt)
		} else {
			b.WriteString("\n- relikRead: emit {\"request\": {\"files\": [\"path\"]}} to read files → result injected as \"" + as + "\"")
		}
	}
	if se != nil {
		as := se.As
		if as == "" {
			as = "EditReceipt"
		}
		scope := se.To.Folder
		if scope == "" {
			scope = "<folder>"
		}
		if se.Prompt != "" {
			b.WriteString("\n- relikEdit: " + se.Prompt)
		} else {
			b.WriteString("\n- relikEdit (folder " + scope + "): emit {\"edit\": {\"file\": \"name.md\", \"ops\": [{\"op\": \"replace\", \"anchor\": \"exact existing text\", \"content\": \"new text\"}]}} to edit a file surgically → receipt (applied/rejected + numbered file view) injected as \"" + as + "\". Ops: replace|insertBefore|insertAfter|delete (anchor-based) or lineReplace|lineDelete|lineInsert (start/end, 1-indexed). Edits are atomic — all ops apply or none do. Optionally include \"expectedVersion\": N (from your last receipt) — if another writer changed the file first you get a version-conflict ToolError; re-read the file and retry.")
		}
	}
	if fs != nil {
		as := fs.As
		if as == "" {
			as = "FileResult"
		}
		if fs.Prompt != "" {
			b.WriteString("\n- files: " + fs.Prompt)
		} else {
			b.WriteString("\n- files: emit {\"read\": {\"path\": \"relative/path\"}} to read a file, {\"write\": {\"path\": \"relative/path\", \"content\": \"...\"}} to write one (add \"append\": true to append), or {\"list\": {\"glob\": \"**/*.py\"}} to list matches → result injected as \"" + as + "\". Paths are relative to the run's file scope.")
		}
	}
	if ced != nil {
		as := ced.As
		if as == "" {
			as = "ConfigEditReceipt"
		}
		if ced.Prompt != "" {
			b.WriteString("\n- configEdit: " + ced.Prompt)
		} else {
			b.WriteString("\n- configEdit: emit {\"configEdit\": {\"ops\": [{\"op\": \"replace\", \"anchor\": \"exact existing config text\", \"content\": \"new text\"}], \"message\": \"why\"}} to edit THIS ladder's stored config surgically → receipt (applied version + changed keys, or refusal reasons) injected as \"" + as + "\". Same op vocabulary as relikEdit. The edit is validated before commit — a broken config cannot land; on validation errors, fix and retry. Protected keys (executionBudget, allowedTargets) do not land: they queue as a pending user approval. Include a self-authored commit message.")
		}
	}
	b.WriteString("\nWhen you have your final answer, respond in prose (NOT JSON).")
	if systemPrompt == "" {
		return strings.TrimPrefix(b.String(), "\n\n")
	}
	return systemPrompt + b.String()
}
