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

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// ConfigEditResult is the configEdit receipt: what changed, which version
// landed (or which approval is waiting), and — on invalid edits — exactly
// why the commit was refused.
type ConfigEditResult struct {
	Applied          bool     `json:"applied"`
	QueuedApproval   bool     `json:"queuedApproval,omitempty"`
	ApprovalID       string   `json:"approvalId,omitempty"`
	Version          int      `json:"version,omitempty"`
	Fingerprint      string   `json:"fingerprint,omitempty"`
	ChangedKeys      []string `json:"changedKeys,omitempty"`
	ProtectedTouched []string `json:"protectedKeysTouched,omitempty"`
	ValidationErrors []string `json:"validationErrors,omitempty"`
	OpStatuses       []relik.EditOpStatus `json:"ops"`
}

// ConfigEditor applies config edits durably. Implemented by the wiring layer
// (pg + version history); faked in tests. The engine core never touches the
// database — this is the same seam pattern as RelikStore and Mailbox.
type ConfigEditor interface {
	EditConfig(ctx context.Context, configID string, currentConfig []byte, ops []relik.EditOp, message string) (*ConfigEditResult, error)
}

// configEditCall is the LLM-emitted configEdit request: surgical ops (the
// relikEdit vocabulary) against the CURRENT stored config plus the
// self-authored commit message (the changelog is the autobiography).
type configEditCall struct {
	Ops     []relik.EditOp `json:"ops,omitempty"`
	Op      string         `json:"op,omitempty"`
	Anchor  string         `json:"anchor,omitempty"`
	Content string         `json:"content,omitempty"`
	Start   int            `json:"start,omitempty"`
	End     int            `json:"end,omitempty"`
	Message string         `json:"message,omitempty"`
}

// runConfigEditToolAdaptive dispatches a configEdit call. Receipts (applied,
// queued-for-approval, or refused-with-reasons) inject as feedback — the
// model sees the consequence of its edit and continues.
func runConfigEditToolAdaptive(
	ctx context.Context,
	ce *ladr.ConfigEditConfig,
	editRaw json.RawMessage,
	runtime *ExecutionRuntimeContext,
	stepID string,
) (*ResolvedField, error) {
	var call configEditCall
	if err := json.Unmarshal(editRaw, &call); err != nil {
		return nil, fmt.Errorf("configEdit: malformed call: %w", err)
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
		return nil, fmt.Errorf("configEdit: at least one op is required")
	}
	if runtime.ConfigEditor == nil || runtime.ConfigID == "" {
		return nil, fmt.Errorf("configEdit: no config editor wired (bare local run?)")
	}

	// The CURRENT stored config is the edit target — not the in-memory copy
	// this run started with (a concurrent edit may have landed; edit latest).
	res, err := runtime.ConfigEditor.EditConfig(ctx, runtime.ConfigID, mustJSONBytes(runtime.Config), ops, call.Message)
	if err != nil {
		return nil, err
	}

	receiptJSON, _ := json.MarshalIndent(res, "", "  ")
	name := ce.As
	if name == "" {
		name = "ConfigEditReceipt"
	}
	return &ResolvedField{Name: name, Value: string(receiptJSON)}, nil
}

func mustJSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// SummarizeKeysForPrompt renders the current config as a compact numbered
// view so the model can aim its anchors without reading the whole file.
func SummarizeKeysForPrompt(cfg *ladr.PipelineConfig) string {
	var b strings.Builder
	b.WriteString("config top-level keys: name, description, allowedTargets, exit, knobs, steps, reliks?, executionBudget?, indexer?, mailbox?\n")
	b.WriteString(fmt.Sprintf("steps: %s (edit step fields by anchoring on their exact current text)\n", stepIDList(cfg)))
	return b.String()
}

func stepIDList(cfg *ladr.PipelineConfig) string {
	ids := make([]string, 0, len(cfg.Steps))
	for _, s := range cfg.Steps {
		ids = append(ids, s.ID())
	}
	return strings.Join(ids, ", ")
}
