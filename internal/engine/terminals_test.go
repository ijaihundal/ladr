// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// You should obtain a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package engine

// terminals_test.go — codeExecution IS terminal access. Covers the terminal
// lifecycle threading through the adaptive loop (terminal/background/action
// fields on the tool call → ExecuteCodeParams → receipt formatting injected
// into the next turn) and the manifest's beacon section
// (TerminalStatusProvider → Beacon.Terminals, graceful absence).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ladr/ladr/internal/device"
	"github.com/ladr/ladr/internal/ladr"
)

// terminalsLadder is a 1-step ladder with the codeExecution key and a
// requestMatch that admits the terminal lifecycle fields.
const terminalsLadder = `{
	"name": "tools-terminals",
	"allowedTargets": {"strategy": "universal"},
	"exit": "work",
	"knobs": {},
	"steps": [
		{
			"id": "work", "name": "Work", "type": "normal",
			"nodes": 1,
			"codeExecution": {
				"maxRounds": 5,
				"as": "ComputedResult",
				"runtime": "bash",
				"requestMatch": {
					"type": "object",
					"properties": {
						"code":       {"type": "string"},
						"terminal":   {"type": "string"},
						"background": {"type": "boolean"},
						"action":     {"type": "string"}
					},
					"additionalProperties": true
				}
			},
			"systemPrompt": "Work with terminals."
		}
	]
}`

// withCodeFn scripts per-call results (a spawn and a peek in one loop).
func (l *ladderIO) withCodeFn(fn func(a ExecuteCodeParams) (ExecuteCodeResult, error)) *ladderIO {
	l.codeFn = fn
	return l
}

func TestLadder_Terminals_PlainCodeUnchanged(t *testing.T) {
	io := newLadderIO([]string{
		`{"code": "echo hi"}`,
		"done",
	}).withCodeResult(ExecuteCodeResult{Output: "hi"})

	result, err, _ := runLadder(t, terminalsLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %q", result)
	}
	call := io.codeCalls[0]
	if call.Terminal != "" || call.Background || call.Action != "" {
		t.Errorf("plain call should carry no terminal fields: %+v", call)
	}
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], "hi") {
		t.Errorf("round-2 prompt should contain the output: %q", io.prompts)
	}
}

func TestLadder_Terminals_BackgroundSpawnThreadsThrough(t *testing.T) {
	io := newLadderIO([]string{
		`{"code": "npm run dev", "terminal": "dev", "background": true}`,
		"Dev server starting.",
	}).withCodeResult(ExecuteCodeResult{Terminal: "dev", Running: true})

	result, err, _ := runLadder(t, terminalsLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "Dev server starting." {
		t.Errorf("result = %q", result)
	}
	call := io.codeCalls[0]
	if call.Terminal != "dev" || !call.Background || call.Action != "" {
		t.Errorf("call = %+v, want terminal dev + background", call)
	}
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], `[terminal dev] command started in background`) {
		t.Errorf("round-2 prompt should carry the spawn receipt: %q", io.prompts)
	}
}

func TestLadder_Terminals_PeekReceipts(t *testing.T) {
	cases := []struct {
		name       string
		result     ExecuteCodeResult
		wantSubstr []string
	}{
		{
			name:   "running",
			result: ExecuteCodeResult{Terminal: "dev", Running: true, Stdout: "listening on :3000\n"},
			wantSubstr: []string{`[terminal dev] RUNNING`, "listening on :3000"},
		},
		{
			name:   "exited with code",
			result: ExecuteCodeResult{Terminal: "dev", Running: false, ExitCode: 3, Stdout: "boom"},
			wantSubstr: []string{`[terminal dev] EXITED (code 3)`, "boom"},
		},
		{
			name:   "no new output",
			result: ExecuteCodeResult{Terminal: "dev", Running: false},
			wantSubstr: []string{`(no new output)`},
		},
		{
			name:   "scrollback evicted",
			result: ExecuteCodeResult{Terminal: "dev", Running: true, Stdout: "x", Truncated: true},
			wantSubstr: []string{`evicted`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			io := newLadderIO([]string{
				`{"action": "peek", "terminal": "dev"}`,
				"ok",
			}).withCodeResult(tc.result)
			_, err, _ := runLadder(t, terminalsLadder, io, 1)
			if err != nil {
				t.Fatalf("ExecutePipeline: %v", err)
			}
			call := io.codeCalls[0]
			if call.Action != "peek" || call.Terminal != "dev" || call.Code != "" {
				t.Errorf("call = %+v, want action peek on terminal dev", call)
			}
			for _, want := range tc.wantSubstr {
				if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], want) {
					t.Errorf("round-2 prompt should contain %q: %q", want, io.prompts)
				}
			}
		})
	}
}

func TestLadder_Terminals_KillAndStatusReceipts(t *testing.T) {
	io := newLadderIO([]string{
		`{"action": "kill", "terminal": "dev"}`,
		`{"action": "status"}`,
		"all done",
	}).withCodeFn(func(a ExecuteCodeParams) (ExecuteCodeResult, error) {
		if a.Action == "kill" {
			return ExecuteCodeResult{Terminal: "dev"}, nil
		}
		return ExecuteCodeResult{Terminals: []device.TerminalStatus{
			{Name: "default", Runtime: "bash", Running: true, Tail: "listening on 3000"},
		}}, nil
	})

	result, err, _ := runLadder(t, terminalsLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if result != "all done" {
		t.Errorf("result = %q", result)
	}
	if io.codeCalls[0].Action != "kill" {
		t.Errorf("first call = %+v", io.codeCalls[0])
	}
	if !strings.Contains(io.prompts[1], `[terminal dev] killed`) {
		t.Errorf("kill receipt missing: %q", io.prompts[1])
	}
	if !strings.Contains(io.prompts[2], `"name": "default"`) || !strings.Contains(io.prompts[2], "listening on 3000") {
		t.Errorf("status should render terminal JSON with tail: %q", io.prompts[2])
	}
}

func TestLadder_Terminals_StatusEmptyAndNotice(t *testing.T) {
	io := newLadderIO([]string{
		`{"action": "status"}`,
		"nothing running",
	}).withCodeResult(ExecuteCodeResult{Terminals: nil, Notice: `evicted terminal "old" (terminal cap (8 per ladder))`})

	_, err, _ := runLadder(t, terminalsLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}
	if !strings.Contains(io.prompts[1], "(no terminals open)") {
		t.Errorf("empty status should say so: %q", io.prompts[1])
	}
	if !strings.Contains(io.prompts[1], `(notice: evicted terminal "old"`) {
		t.Errorf("notice should append: %q", io.prompts[1])
	}
}

func TestLadder_Terminals_SpecTextDocumentsTerminals(t *testing.T) {
	io := newLadderIO([]string{
		`{"code": "true"}`,
		"done",
	}).withCodeResult(ExecuteCodeResult{Output: ""})

	_, _, _ = runLadder(t, terminalsLadder, io, 1)

	if len(io.prompts) < 1 {
		t.Fatal("no prompts captured")
	}
	for _, want := range []string{"Terminals:", `"background": true`, `"action": "peek"`, `"action": "kill"`, `"action": "status"`} {
		if !strings.Contains(io.prompts[0], want) {
			t.Errorf("auto spec should document %q: %q", want, io.prompts[0])
		}
	}
}

func TestLadder_Terminals_BusyErrorIsFeedback(t *testing.T) {
	io := newLadderIO([]string{
		`{"code": "echo x", "terminal": "dev"}`,
		"will peek instead.",
	}).withCodeErr(errors.New(`terminal "dev" is busy — a background command is still running`))

	result, err, _ := runLadder(t, terminalsLadder, io, 1)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v (busy must be non-fatal)", err)
	}
	if result != "will peek instead." {
		t.Errorf("result = %q", result)
	}
	if len(io.prompts) < 2 || !strings.Contains(io.prompts[1], "ToolError") || !strings.Contains(io.prompts[1], "busy") {
		t.Errorf("round-2 prompt should carry the busy ToolError: %q", io.prompts)
	}
}

// --- configEdit adaptive-loop wiring (the omission fix) ---

func TestHasAdaptiveKeys_IncludesConfigEdit(t *testing.T) {
	b := &ladr.BaseStep{ConfigEdit: &ladr.ConfigEditConfig{}}
	if !b.HasAdaptiveKeys() {
		t.Error("configEdit-only step must count as adaptive")
	}
}

func TestBuildAdaptiveLoopConfig_ReadsConfigEdit(t *testing.T) {
	ced := &ladr.ConfigEditConfig{MaxRounds: 7}
	cfg := buildAdaptiveLoopConfig(nil, nil, nil, nil, ced)
	if cfg.maxRounds != 7 {
		t.Errorf("maxRounds = %d, want 7 (configEdit-only steps fell through to zero-value before the fix)", cfg.maxRounds)
	}
}

// --- manifest beacon section ---

// beaconIO wraps ladderIO with the TerminalStatusProvider capability.
type beaconIO struct {
	*ladderIO
	terms  []device.TerminalStatus
	err    error
	called bool
}

func (b *beaconIO) TerminalStatus(_ context.Context, _ TerminalStatusArgs) ([]device.TerminalStatus, error) {
	b.called = true
	return b.terms, b.err
}

func manifestTestConfig(t *testing.T) *ladr.PipelineConfig {
	t.Helper()
	cfg, err := ladr.ParseStoredConfig([]byte(terminalsLadder))
	if err != nil {
		t.Fatalf("ParseStoredConfig: %v", err)
	}
	return cfg
}

func TestBuildManifest_BeaconSectionPresent(t *testing.T) {
	bio := &beaconIO{
		ladderIO: newLadderIO(nil),
		terms: []device.TerminalStatus{
			{Name: "dev", Runtime: "bash", Running: true, Tail: "listening"},
		},
	}
	m, err := BuildManifest(context.Background(), manifestTestConfig(t), &ExecutionRuntimeContext{
		InstanceID: "m1",
		Inputs:     map[string]any{},
	}, bio, nil, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if !bio.called {
		t.Error("TerminalStatusProvider was not consulted")
	}
	if m.Beacon == nil || !m.Beacon.Connected || len(m.Beacon.Terminals) != 1 || m.Beacon.Terminals[0].Name != "dev" {
		t.Errorf("beacon section = %+v", m.Beacon)
	}
}

func TestBuildManifest_BeaconAbsentWithoutCapability(t *testing.T) {
	m, err := BuildManifest(context.Background(), manifestTestConfig(t), &ExecutionRuntimeContext{
		InstanceID: "m1",
		Inputs:     map[string]any{},
	}, newLadderIO(nil), nil, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if m.Beacon != nil {
		t.Errorf("beacon should be nil when the IO doesn't provide terminal status: %+v", m.Beacon)
	}
}

func TestBuildManifest_BeaconAbsentOnError(t *testing.T) {
	bio := &beaconIO{ladderIO: newLadderIO(nil), err: errors.New("no device")}
	m, err := BuildManifest(context.Background(), manifestTestConfig(t), &ExecutionRuntimeContext{
		InstanceID: "m1",
		Inputs:     map[string]any{},
	}, bio, nil, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v (device errors must degrade, not fail the manifest)", err)
	}
	if m.Beacon != nil {
		t.Errorf("beacon should be nil on provider error: %+v", m.Beacon)
	}
}

func TestBuildManifest_BeaconBudgetCapsSlowDevice(t *testing.T) {
	orig := manifestBeaconBudget
	manifestBeaconBudget = 40 * time.Millisecond
	defer func() { manifestBeaconBudget = orig }()

	bio := &beaconIO{ladderIO: newLadderIO(nil)}
	bio.err = nil
	slow := &slowTerminalStatus{bio}
	m, err := BuildManifest(context.Background(), manifestTestConfig(t), &ExecutionRuntimeContext{
		InstanceID: "m1",
		Inputs:     map[string]any{},
	}, slow, nil, nil)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if m.Beacon != nil {
		t.Errorf("beacon should be nil when the device exceeds the budget: %+v", m.Beacon)
	}
}

// slowTerminalStatus embeds beaconIO (so it satisfies EngineIO) but answers
// TerminalStatus slower than the manifest budget.
type slowTerminalStatus struct {
	*beaconIO
}

func (s *slowTerminalStatus) TerminalStatus(ctx context.Context, args TerminalStatusArgs) ([]device.TerminalStatus, error) {
	select {
	case <-time.After(2 * time.Second):
		return s.beaconIO.TerminalStatus(ctx, args)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
