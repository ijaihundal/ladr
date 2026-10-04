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
	"strings"
	"testing"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/mailbox"
)

// The mailbox acceptance: two ladders coordinate while never alive at the
// same time. Ladder A posts a notice in its post-step mail block and exits.
// Ladder B, a LATER run, drains the message at resolution time and its model
// sees the signal. In between, the message just sits in the store — durable
// by construction.

func mailTestConfig(name string, mail *ladr.MailBlock) *ladr.PipelineConfig {
	step := ladr.Step{
		Kind: ladr.StepTypeNormal,
		Normal: &ladr.NormalStep{
			BaseStep: ladr.BaseStep{
				ID: "go", Name: "go", Type: ladr.StepTypeNormal, SystemPrompt: "x",
				Mail: mail,
			},
		},
	}
	return &ladr.PipelineConfig{
		Name:           name,
		Exit:           "go",
		AllowedTargets: ladr.AllowedTargetsPolicy{Strategy: ladr.AllowedTargetsUniversal},
		Knobs:          map[string]ladr.Knob{},
		Steps:          []ladr.Step{step},
	}
}

func TestMailbox_PostStepSend(t *testing.T) {
	mb := mailbox.NewMemStore()
	cfg := mailTestConfig("sender-ladder", &ladr.MailBlock{
		To:   "receiver-ladder",
		Type: "notice",
	})
	runtime := ExecutionRuntimeContext{
		InstanceID: "send-1", Inputs: map[string]any{}, Knobs: map[string]float64{},
		OwnerID: "owner-1", LadderName: "sender-ladder", Mailbox: mb,
	}
	io := &mockEngineIO{llmFn: func(a ExecuteLlmParams) string { return `{}` }}
	if _, err := ExecutePipeline(context.Background(), ExecutePipelineOptions{
		Config: cfg, Context: runtime, IO: io, MaxLoops: 1,
	}); err != nil {
		t.Fatalf("sender run: %v", err)
	}
	// The message waits durably — nobody drains it here.
	env, err := mb.Drain(context.Background(), "owner-1", "receiver-ladder", 0)
	if err != nil || len(env) != 1 {
		t.Fatalf("expected 1 durable message, got %d (err %v)", len(env), err)
	}
	if env[0].Sender != "sender-ladder" || env[0].Type != "notice" {
		t.Errorf("envelope wrong: %+v", env[0])
	}
}

func TestMailbox_FromSourceConsumes(t *testing.T) {
	mb := mailbox.NewMemStore()
	if err := mb.Post(context.Background(), "owner-1", "receiver-ladder", mailbox.Envelope{
		Sender: "watcher", Type: "alert", CorrelationID: "corr-7",
	}); err != nil {
		t.Fatal(err)
	}
	// Explicit consume via the FromSpec vocabulary: from: { mailbox: true }.
	from := ladr.FromSpec{Kind: ladr.FromMailboxRef, Mailbox: true}
	runtime := &ExecutionRuntimeContext{
		InstanceID: "recv-1", OwnerID: "owner-1", LadderName: "receiver-ladder", Mailbox: mb,
	}
	got, err := ResolveFromSpec(context.Background(), from, runtime, &mockEngineIO{}, nil, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(got, "from watcher") || !strings.Contains(got, "alert") || !strings.Contains(got, "corr-7") {
		t.Errorf("injection missing envelope detail: %q", got)
	}
	// Drain = consume: the message is gone.
	again, _ := mb.Drain(context.Background(), "owner-1", "receiver-ladder", 0)
	if len(again) != 0 {
		t.Errorf("drain must consume, %d left", len(again))
	}
}

func TestMailbox_RelikRefPointerRoundTrip(t *testing.T) {
	// Payloads live in reliks; envelopes carry the pointer. The relik-ref
	// must survive the post → format path so the receiver knows where to read.
	mb := mailbox.NewMemStore()
	ref := &ladr.RelikTarget{Relik: "Self", Folder: "outbox", File: "note.md"}
	if err := mb.Post(context.Background(), "o", "l", mailbox.Envelope{
		Sender: "s", Type: "handoff", RelikRef: ref,
	}); err != nil {
		t.Fatal(err)
	}
	env, _ := mb.Drain(context.Background(), "o", "l", 0)
	if len(env) != 1 || env[0].RelikRef == nil || env[0].RelikRef.File != "note.md" {
		t.Fatalf("relik-ref lost: %+v", env)
	}
	if !strings.Contains(mailbox.Format(env), "Self/outbox/note.md") {
		t.Errorf("format must show the payload pointer: %q", mailbox.Format(env))
	}
}
