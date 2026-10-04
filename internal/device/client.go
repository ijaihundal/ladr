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

package device

import (
	"context"
)

// Transport is the device-side (client) view of the gateway connection —
// bidirectional. The device Client reads request envelopes and writes
// response envelopes over it. A WebSocket adapter implements this over a
// live ws connection; tests use an in-memory loopback.
type Transport interface {
	// Recv blocks until the next envelope from the gateway (or returns an error
	// when the connection closes).
	Recv() (Envelope, error)
	// Send writes an envelope to the gateway.
	Send(env Envelope) error
	// Close terminates the connection.
	Close() error
}

// Client runs on the user's device: it reads request envelopes from the
// gateway, executes exec_request envelopes via the local Backend (direct
// persistent runtimes — a SessionRunner), and sends exec_response envelopes
// back. Serve runs until the transport closes or ctx is cancelled.
//
// This is the device-side half of federated execution: the compute that runs
// on the user's hardware, leaving the recipe (hidden-config ladder) on the
// server. This Go client is the reference implementation / test harness; the
// shipping client is beacon's TypeScript device client (same envelope
// protocol, same sentinel-session design).
type Client struct {
	Exec      Backend // typically NewSessionRunner("") (direct persistent runtimes)
	Transport Transport
}

// Serve runs the read→exec→respond loop until the transport closes or ctx ends.
func (c *Client) Serve(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		env, err := c.Transport.Recv()
		if err != nil {
			return err
		}
		if env.Type != TypeExecRequest {
			continue // ignore stray non-request messages
		}
		// Handle concurrently so one slow run doesn't block the next request.
		go c.handle(ctx, env)
	}
}

func (c *Client) handle(ctx context.Context, env Envelope) {
	res, err := c.Exec.Exec(ctx, ExecParams{
		SessionID: env.SessionID,
		Runtime:   Runtime(env.Runtime),
		Code:      env.Code,
		TimeoutMs: env.TimeoutMs,
		LadderID:  env.LadderID,
		Cwd:       env.Cwd,
	})
	resp := Envelope{Type: TypeExecResponse, ID: env.ID}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Stdout = res.Stdout
		resp.Stderr = res.Stderr
		resp.ExitCode = res.ExitCode
	}
	_ = c.Transport.Send(resp)
}
