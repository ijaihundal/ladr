// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package device

// channel_terminals_test.go — the terminal lifecycle fields ride the exec
// envelope end to end: ExecParams → Envelope on the wire → ExecResult fields
// parsed back. No new envelope types; old gateways forward these untouched.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// recordingDispatcher captures dispatched envelopes and replies with a
// scripted response.
type recordingDispatcher struct {
	reqs []Envelope
	resp Envelope
}

func (d *recordingDispatcher) Dispatch(_ context.Context, _ string, req Envelope) (Envelope, error) {
	d.reqs = append(d.reqs, req)
	resp := d.resp
	resp.ID = req.ID
	return resp, nil
}

func TestHubChannel_ExecThreadsTerminalFields(t *testing.T) {
	d := &recordingDispatcher{resp: Envelope{
		Type:     TypeExecResponse,
		Terminal: "dev",
		Running:  true,
	}}
	ch := &HubChannel{Dispatcher: d}

	res, err := ch.Exec(context.Background(), "u1", "s1", ExecParams{
		SessionID:  "s1",
		Runtime:    RuntimeBash,
		Code:       "npm run dev",
		Terminal:   "dev",
		Background: true,
		LadderID:   "lad1",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	req := d.reqs[0]
	if req.Type != TypeExecRequest {
		t.Errorf("type = %q, want exec_request (terminal lifecycle adds fields, not types)", req.Type)
	}
	if req.Terminal != "dev" || !req.Background || req.Action != "" {
		t.Errorf("envelope terminal fields = %q/%v/%q", req.Terminal, req.Background, req.Action)
	}
	if req.LadderID != "lad1" || req.Code != "npm run dev" {
		t.Errorf("envelope = %+v", req)
	}
	if res.Terminal != "dev" || !res.Running {
		t.Errorf("result terminal fields = %+v", res)
	}
}

func TestHubChannel_ExecTerminalActionRoundTrip(t *testing.T) {
	terms := []TerminalStatus{
		{Name: "dev", Runtime: "bash", Cwd: "/tmp/x", Running: true, IdleMs: 1200, AgeMs: 99000, Tail: "listening"},
		{Name: "default", Runtime: "python", ExitCode: 0},
	}
	d := &recordingDispatcher{resp: Envelope{Type: TypeExecResponse, Terminals: terms}}
	ch := &HubChannel{Dispatcher: d}

	res, err := ch.Exec(context.Background(), "u1", "", ExecParams{
		Action:   "status",
		LadderID: "lad1",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	req := d.reqs[0]
	if req.Action != "status" || req.Code != "" || req.Terminal != "" {
		t.Errorf("status envelope = %+v", req)
	}
	if len(res.Terminals) != 2 || res.Terminals[0].Name != "dev" || !res.Terminals[0].Running {
		t.Errorf("terminals = %+v", res.Terminals)
	}
}

func TestEnvelope_TerminalFieldsJSONRoundTrip(t *testing.T) {
	// Wire-shape check: the JSON keys match the beacon's protocol.ts exactly
	// (terminal/background/action in; running/terminals/notice out).
	in := Envelope{Type: TypeExecRequest, ID: "x", Terminal: "dev", Background: true, Action: "peek", LadderID: "l"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{`"terminal":"dev"`, `"background":true`, `"action":"peek"`, `"ladderId":"l"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("wire JSON missing %s: %s", key, b)
		}
	}

	out := Envelope{
		Type:      TypeExecResponse,
		Running:   true,
		Notice:    "evicted",
		Terminals: []TerminalStatus{{Name: "dev"}},
	}
	b2, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{`"running":true`, `"notice":"evicted"`, `"terminals":[{"name":"dev"}]`} {
		if !strings.Contains(string(b2), key) {
			t.Errorf("wire JSON missing %s: %s", key, b2)
		}
	}
}
