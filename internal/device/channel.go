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

// Package device — the engine→device seam types (formerly internal/executor).
//
// Code execution is direct-runtime (persistent local processes — see
// sessions.go) with no containerization anywhere. When the engine and the
// device are different machines, the HubChannel routes calls over the device
// channel (Dispatcher below); when they are the same machine (ladr local
// runs), the engine uses a SessionRunner directly.
package device

import (
	"context"
	"errors"
	"fmt"
)

// Runtime identifies a language backend.
type Runtime string

const (
	RuntimePython Runtime = "python"
	RuntimeNode   Runtime = "node"
	RuntimeBash   Runtime = "bash"
)

// ExecParams is a single code-execution request.
type ExecParams struct {
	SessionID string // stable key (step/loop/node coordinate) → one persistent process
	Runtime   Runtime
	Code      string // the code block for this call
	TimeoutMs int    // per-call wall-clock cap; 0 → DefaultTimeout

	// Terminal lifecycle (v2) — meaningful on the federated path, where
	// codeExecution lands in named persistent terminals on the device.
	// Terminal names one (empty → the ladder's default terminal); Background
	// starts a long-running command (device replies immediately, output
	// accumulates in scrollback); Action is "peek" | "kill" | "status" on the
	// terminal itself (Code empty). Local runners approximate Terminal by
	// using it as the session key and reject Background/Action.
	Terminal   string
	Background bool
	Action     string

	// OwnerID is the user whose device should run this code in the federated
	// path (the routing key for the device channel). Local runners ignore it.
	OwnerID string

	// LadderID attributes the call to a ladder (device-side grant checks).
	LadderID string

	// Cwd is the requested working directory (a granted root on the device).
	// The device validates it against its grants; runners that don't support
	// cwd scoping ignore it.
	Cwd string
}

// ExecResult is the outcome of one code-execution call.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int

	// Terminal lifecycle fields (v2) — populated for background spawns and
	// terminal actions; zero values on plain foreground runs.
	Terminal   string
	Running    bool
	Truncated  bool
	Terminals  []TerminalStatus
	Notice     string
}

// Output returns the trimmed stdout, falling back to stderr if stdout is empty,
// suitable for injection into the prompt as the tool's `as:` field.
func (r ExecResult) Output() string {
	if s := trimOutput(r.Stdout); s != "" {
		return s
	}
	if s := trimOutput(r.Stderr); s != "" {
		return s
	}
	return fmt.Sprintf("(exit code %d, no output)", r.ExitCode)
}

// Backend is the code-execution seam. Local persistent sessions
// (SessionRunner) and the federated path (engine-side HubChannel routing)
// both satisfy it.
type Backend interface {
	// Exec runs p.Code and returns its result. A non-zero exit code is a result,
	// not an error; only timeout/infra/dispatch failures error out.
	Exec(ctx context.Context, p ExecParams) (ExecResult, error)
}

// Dispatcher is the engine-side view of "route this request envelope to the
// user's device and wait for the response." It decouples HubChannel from
// WHERE the device lives:
//   - *Hub satisfies Dispatcher when the engine and gateway run in one process
//     (the all-in-one binary / co-located self-hosted) — dispatch is an in-process
//     channel write to the live WebSocket.
//   - *RedisDispatcher satisfies it when the engine and gateway are separate
//     processes (the default Docker Compose topology) — dispatch publishes to a
//     per-user Redis channel and a gateway-side RedisBridge delivers it to the
//     device.
//
// Both reuse the same Envelope protocol + ID correlation, and both are
// type-agnostic (exec, fs_read, fs_write, fs_list requests).
type Dispatcher interface {
	// Dispatch sends req to the device connected for userID and returns the
	// matching response. Dispatch assigns a correlation ID. Returns
	// ErrNoDevice when no client is connected; ctx bounds the wait.
	Dispatch(ctx context.Context, userID string, req Envelope) (Envelope, error)
}

// HubChannel adapts a Dispatcher to the federated Backend + fs seams — the
// glue that lets a remote engine route codeExecution/files tool calls to a
// connected device. The userID (the instance owner) is threaded in from the
// engine host, which resolves it directly from the pipeline instance — no DB
// lookup indirection here.
type HubChannel struct {
	Dispatcher Dispatcher
}

// Exec routes a code-execution call (including terminal lifecycle calls) to
// the connected device.
func (c *HubChannel) Exec(ctx context.Context, userID, sessionID string, p ExecParams) (ExecResult, error) {
	if c == nil || c.Dispatcher == nil {
		return ExecResult{}, errors.New("device channel: dispatcher not wired")
	}
	resp, err := c.Dispatcher.Dispatch(ctx, userID, Envelope{
		Type:       TypeExecRequest,
		SessionID:  sessionID,
		Runtime:    string(p.Runtime),
		Code:       p.Code,
		TimeoutMs:  p.TimeoutMs,
		Terminal:   p.Terminal,
		Background: p.Background,
		Action:     p.Action,
		LadderID:   p.LadderID,
		Cwd:        p.Cwd,
	})
	if err != nil {
		return ExecResult{}, err
	}
	if resp.Error != "" {
		return ExecResult{}, errors.New(resp.Error)
	}
	return ExecResult{
		Stdout:     resp.Stdout,
		Stderr:     resp.Stderr,
		ExitCode:   resp.ExitCode,
		Terminal:   resp.Terminal,
		Running:    resp.Running,
		Truncated:  resp.Truncated,
		Terminals:  resp.Terminals,
		Notice:     resp.Notice,
	}, nil
}

// FsRead routes a file-read call to the connected device. path is relative to
// the device's granted root for this ladder.
func (c *HubChannel) FsRead(ctx context.Context, userID, ladderID, path string) (Envelope, error) {
	return c.send(ctx, userID, Envelope{Type: TypeFsReadRequest, LadderID: ladderID, Path: path})
}

// FsWrite routes a file-write call to the connected device.
func (c *HubChannel) FsWrite(ctx context.Context, userID, ladderID, path, content string, append bool) (Envelope, error) {
	return c.send(ctx, userID, Envelope{Type: TypeFsWriteRequest, LadderID: ladderID, Path: path, Content: content, Append: append})
}

// FsList routes a file-list call to the connected device. glob is optional;
// empty glob lists the path directory.
func (c *HubChannel) FsList(ctx context.Context, userID, ladderID, path, glob string) (Envelope, error) {
	return c.send(ctx, userID, Envelope{Type: TypeFsListRequest, LadderID: ladderID, Path: path, Glob: glob})
}

// send is the typed-envelope core: dispatch and check for transport/protocol
// errors. Response-payload errors (denied, missing) are carried in the
// returned envelope's Error field — the caller decides how to surface them.
func (c *HubChannel) send(ctx context.Context, userID string, req Envelope) (Envelope, error) {
	if c == nil || c.Dispatcher == nil {
		return Envelope{}, errors.New("device channel: dispatcher not wired")
	}
	resp, err := c.Dispatcher.Dispatch(ctx, userID, req)
	if err != nil {
		return Envelope{}, err
	}
	if resp.Type == "" {
		return Envelope{}, fmt.Errorf("device channel: empty response type for %s", req.Type)
	}
	return resp, nil
}

// trimOutput strips trailing newlines/whitespace from captured output.
func trimOutput(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
