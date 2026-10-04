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

// Package device implements the device channel: the transport over which a
// server-side ladder dispatches tools.codeExecution calls to the signed-in
// client running on the user's device (the federated execution path — Phase 3).
//
// The protocol + hub are transport-agnostic: the hard parts (connection
// registry keyed by user, request/response correlation by ID, timeout, multi-
// client) are independent of WebSocket specifics, so they are fully unit-testable
// in-memory. A real WebSocket adapter is a thin layer over the Connection /
// Transport interfaces defined here.
package device

// Envelope is the wire message exchanged over the device channel.
//
//	gateway → client : Type "exec_request"   (SessionID/Runtime/Code/TimeoutMs/Cwd)
//	gateway → client : Type "fs_read_request"   (Path)
//	gateway → client : Type "fs_write_request"  (Path/Content/Append)
//	gateway → client : Type "fs_list_request"   (Path/Glob)
//	client  → gateway: Type "exec_response"  (Stdout/Stderr/ExitCode/Error)
//	client  → gateway: Type "fs_read_response"  (Content/Truncated/Error)
//	client  → gateway: Type "fs_write_response" (Ok/Bytes/Error)
//	client  → gateway: Type "fs_list_response"  (Entries/Error)
//
// ID correlates a response with its request. LadderID rides on every request
// so the device can attribute ops to a ladder for its per-ladder grant checks;
// the engine treats it as opaque.
type Envelope struct {
	Type string `json:"type"` // one of the Type* constants below
	ID   string `json:"id"`   // correlation id, set by the hub on dispatch

	// exec_request fields (gateway → client).
	SessionID string `json:"sessionId,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
	Code      string `json:"code,omitempty"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
	Cwd       string `json:"cwd,omitempty"` // requested working dir (a granted root on the device; the device validates)

	// Terminal lifecycle fields (v2) — a terminal IS a persistent interpreter
	// session, named per ladder on the device. Terminal selects (or opens)
	// one; empty means the ladder's default terminal. Background starts a
	// long-running command and the device replies immediately (output
	// accumulates in the terminal's scrollback). Action addresses the
	// terminal itself: "peek" (new scrollback since the caller's last peek +
	// running state), "kill" (kill the terminal's process group), "status"
	// (the ladder's terminals). All ride exec_request/exec_response — no new
	// envelope types, old gateways forward them untouched.
	Terminal   string `json:"terminal,omitempty"`
	Background bool   `json:"background,omitempty"`
	Action     string `json:"action,omitempty"` // "peek" | "kill" | "status"

	// LadderID identifies the ladder whose run produced this request. The
	// device uses it for per-ladder grant checks; the engine treats it as
	// opaque attribution metadata.
	LadderID string `json:"ladderId,omitempty"`

	// fs request fields (gateway → client). Path is relative to a device-side
	// granted root; the device resolves + contains it (realpath check).
	Path    string `json:"path,omitempty"` // fs_read / fs_write target
	Glob    string `json:"glob,omitempty"` // fs_list pattern
	Content string `json:"content,omitempty"`
	Append  bool   `json:"append,omitempty"` // fs_write mode: append vs truncate

	// exec_response fields (client → gateway).
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`

	// Terminal lifecycle response fields. Running reports the terminal's
	// background command (peek; also true on a background spawn receipt);
	// peek output arrives as Stdout; status returns the ladder's terminals
	// in Terminals; Notice carries non-fatal warnings (e.g. an eviction
	// under a terminal cap).
	Running   bool             `json:"running,omitempty"`
	Terminals []TerminalStatus `json:"terminals,omitempty"`
	Notice    string           `json:"notice,omitempty"`

	// fs response fields (client → gateway).
	Truncated bool     `json:"truncated,omitempty"` // fs_read: content hit the size cap
	Entries   []string `json:"entries,omitempty"`   // fs_list result
	Ok        bool     `json:"ok,omitempty"`        // fs_write success
	Bytes     int      `json:"bytes,omitempty"`     // fs_write bytes written
}

// TerminalStatus is one terminal's live state on the device, as returned by
// action "status" (and embedded in the run manifest's beacon section).
type TerminalStatus struct {
	Name     string `json:"name"`
	Runtime  string `json:"runtime,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Running  bool   `json:"running,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"` // set once the background command exited
	IdleMs   int64  `json:"idleMs,omitempty"`
	AgeMs    int64  `json:"ageMs,omitempty"`
	Tail     string `json:"tail,omitempty"` // last ~200 chars of scrollback
}

const (
	TypeExecRequest = "exec_request"
	TypeExecResponse = "exec_response"

	TypeFsReadRequest  = "fs_read_request"
	TypeFsReadResponse = "fs_read_response"
	TypeFsWriteRequest = "fs_write_request"
	TypeFsWriteResponse = "fs_write_response"
	TypeFsListRequest  = "fs_list_request"
	TypeFsListResponse = "fs_list_response"
)

// ResponseTypeFor maps a request type to its response type. Unknown request
// types map to "" (the bridge drops or errors them).
func ResponseTypeFor(requestType string) string {
	switch requestType {
	case TypeExecRequest:
		return TypeExecResponse
	case TypeFsReadRequest:
		return TypeFsReadResponse
	case TypeFsWriteRequest:
		return TypeFsWriteResponse
	case TypeFsListRequest:
		return TypeFsListResponse
	default:
		return ""
	}
}

