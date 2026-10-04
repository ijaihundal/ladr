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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrNoDevice is returned when Dispatch has no connected client for the user.
var ErrNoDevice = errors.New("device: no client connected for user")

// Connection is the gateway-side, send-only view of a connected client device
// (gateway → client). The receive path (client → gateway) is handled by the
// transport's read loop, which calls Hub.Deliver for each response envelope.
//
// Implementations must make Send safe for concurrent use (the hub may dispatch
// from many goroutines to one user).
type Connection interface {
	Send(ctx context.Context, env Envelope) error
	Close() error
	UserID() string
}

// Hub is the gateway-side registry of connected client devices, plus the
// request/response correlator. It is the core of the device channel: dispatch
// sends an exec request to the user's device and blocks until the response
// arrives (or the context times out).
//
// A Hub is safe for concurrent use. One connection per user (latest register
// wins); multi-device affinity/round-robin is a planned refinement.
type Hub struct {
	mu      sync.Mutex
	byUser  map[string]Connection
	pending map[string]chan Envelope // requestID → response waiter
	idSeq   uint64

	// Presence, when set, is notified as devices connect/disconnect. The
	// hosted engine uses it for run pre-flight (rejecting code/files ladders
	// with no connected device); nil disables tracking (local deployments).
	Presence PresenceStore
}

// NewHub returns an empty device hub.
func NewHub() *Hub {
	return &Hub{byUser: map[string]Connection{}, pending: map[string]chan Envelope{}}
}

// Register associates a connection with its user (replacing any prior
// connection for that user).
func (h *Hub) Register(c Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byUser[c.UserID()] = c
}

// Unregister removes a connection if it is still the one registered for its
// user (avoids removing a newer connection under a stale handle) and reports
// whether it did. Transports use the result to scope cleanup (e.g. clearing
// the user's presence key only when this connection owned the slot).
func (h *Hub) Unregister(c Connection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.byUser[c.UserID()]; ok && cur == c {
		delete(h.byUser, c.UserID())
		return true
	}
	return false
}

// Deliver routes a response envelope (read from a client by the transport's
// read loop) to its pending Dispatch call. Unknown/stale IDs are dropped.
func (h *Hub) Deliver(env Envelope) {
	h.mu.Lock()
	ch, ok := h.pending[env.ID]
	if ok {
		delete(h.pending, env.ID)
	}
	h.mu.Unlock()
	if ok {
		select {
		case ch <- env:
		default:
		}
	}
}

// Dispatch sends a request envelope (any Type: exec_request, fs_read_request,
// ...) to the user's connected device and waits for the matching response.
// Returns ErrNoDevice if no client is connected. The provided ctx bounds the
// wait; cancellation/timeout unblocks the caller (the late response, if any,
// is dropped on arrival). Dispatch assigns a fresh correlation ID; the
// RedisBridge (which must preserve the engine-assigned ID) calls
// dispatchWithID directly.
func (h *Hub) Dispatch(ctx context.Context, userID string, req Envelope) (Envelope, error) {
	req.ID = h.newRequestID()
	return h.dispatchWithID(ctx, userID, req)
}

// dispatchWithID is the correlation core: it sends req (with its already-set ID
// and Type) to the user's device and waits for the response keyed by req.ID.
// Shared by Dispatch (in-process) and the RedisBridge (cross-process,
// engine-set ID). The request type passes through untouched — dispatch is
// type-agnostic (exec, fs_read, fs_write, fs_list).
func (h *Hub) dispatchWithID(ctx context.Context, userID string, req Envelope) (Envelope, error) {
	if req.Type == "" {
		req.Type = TypeExecRequest // backward compat for callers that only set fields
	}
	h.mu.Lock()
	conn, ok := h.byUser[userID]
	if !ok {
		h.mu.Unlock()
		return Envelope{}, ErrNoDevice
	}
	ch := make(chan Envelope, 1)
	h.pending[req.ID] = ch
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.pending, req.ID)
		h.mu.Unlock()
	}()

	if err := conn.Send(ctx, req); err != nil {
		return Envelope{}, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	}
}

// Has reports whether a connection is registered for the user (useful for
// deciding local vs federated routing at the ExecuteCode seam).
func (h *Hub) Has(userID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.byUser[userID]
	return ok
}

func (h *Hub) newRequestID() string {
	n := atomic.AddUint64(&h.idSeq, 1)
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) + "-" + itoaU(n)
}

func itoaU(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
