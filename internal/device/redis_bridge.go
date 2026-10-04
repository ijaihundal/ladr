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
	"encoding/json"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Redis pub/sub channel layout for the device channel:
//
//	ladr:dev:req:<userID>      engine → gateway: a request Envelope (exec_,
//	                          fs_read_, fs_write_, fs_list_request)
//	                          (engine publishes; the gateway holding the WS consumes)
//	ladr:dev:resp:<requestID>  gateway → engine: the matching response Envelope
//	                          (gateway publishes; the engine waiting on that ID consumes)
//
// The request carries the engine-assigned ID; the response is routed back by
// that same ID. This is the cross-process analogue of the in-memory Hub's
// pending map: the engine correlates responses by ID, the gateway correlates
// device replies by ID via the Hub.
const defaultPrefix = "ladr:dev:"

// --- engine side: RedisDispatcher ---------------------------------------

// RedisDispatcher is the engine-side Dispatcher: it routes tools.codeExecution
// calls to a device connected to a (possibly separate) gateway process, over
// Redis pub/sub. Use it when the engine and gateway run as separate processes
// (the default Docker Compose topology). When they share a process, a *Hub
// dispatches directly to the in-memory WebSocket connection.
//
// Call Run once at startup to start the response receiver; Dispatch blocks
// until Run is draining responses.
type RedisDispatcher struct {
	rdb    *goredis.Client
	prefix string

	mu      sync.Mutex
	pending map[string]chan Envelope // requestID → response waiter
}

var _ Dispatcher = (*RedisDispatcher)(nil)

// NewRedisDispatcher returns an engine-side dispatcher that reaches devices via
// Redis. The caller must Run it once.
func NewRedisDispatcher(rdb *goredis.Client) *RedisDispatcher {
	return &RedisDispatcher{rdb: rdb, prefix: defaultPrefix, pending: map[string]chan Envelope{}}
}

// Run starts the response receiver (PSubscribe resp:*) and blocks until ctx is
// cancelled. It must be running for Dispatch to receive responses. Safe to call
// in a background goroutine:  go rd.Run(ctx).
func (d *RedisDispatcher) Run(ctx context.Context) error {
	pubsub := d.rdb.PSubscribe(ctx, d.prefix+"resp:*")
	defer pubsub.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return nil
			}
			var env Envelope
			if json.Unmarshal([]byte(msg.Payload), &env) != nil || env.ID == "" {
				continue
			}
			d.deliver(env)
		}
	}
}

// Dispatch publishes the request envelope (any Type: exec_request,
// fs_read_request, ...) to the user's request channel and blocks for the
// matching response (correlated by a fresh ID). Returns ErrNoDevice if the
// gateway reports no connected client; ctx bounds the wait.
func (d *RedisDispatcher) Dispatch(ctx context.Context, userID string, req Envelope) (Envelope, error) {
	if userID == "" {
		return Envelope{}, ErrNoDevice
	}
	req.ID = newRequestIDHex()
	if req.Type == "" {
		req.Type = TypeExecRequest
	}
	ch := make(chan Envelope, 1)
	d.mu.Lock()
	d.pending[req.ID] = ch
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.pending, req.ID)
		d.mu.Unlock()
	}()

	payload, err := json.Marshal(req)
	if err != nil {
		return Envelope{}, err
	}
	if err := d.rdb.Publish(ctx, d.prefix+"req:"+userID, payload).Err(); err != nil {
		return Envelope{}, err
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	}
}

func (d *RedisDispatcher) deliver(env Envelope) {
	d.mu.Lock()
	ch, ok := d.pending[env.ID]
	if ok {
		delete(d.pending, env.ID)
	}
	d.mu.Unlock()
	if ok {
		select {
		case ch <- env:
		default:
		}
	}
}

// --- gateway side: RedisBridge ------------------------------------------

// RedisBridge runs in the gateway process: it subscribes to exec-request
// channels on Redis and dispatches each to the local Hub (which owns the live
// WebSocket connections to devices), then publishes the response back. This is
// what lets a separate engine process reach devices connected to the gateway —
// the bridge is the Redis↔Hub adapter.
//
// One bridge per gateway process; start with  go bridge.Run(ctx)  alongside the
// HTTP server. Requests for users with no connected device get an immediate
// error response so the engine fails fast instead of waiting out its timeout.
type RedisBridge struct {
	Hub    *Hub
	rdb    *goredis.Client
	prefix string
}

// NewRedisBridge returns a gateway-side bridge that ties Redis exec requests to
// the in-process device Hub.
func NewRedisBridge(hub *Hub, rdb *goredis.Client) *RedisBridge {
	return &RedisBridge{Hub: hub, rdb: rdb, prefix: defaultPrefix}
}

// Run subscribes to exec requests and serves them until ctx is cancelled.
func (b *RedisBridge) Run(ctx context.Context) error {
	pubsub := b.rdb.PSubscribe(ctx, b.prefix+"req:*")
	defer pubsub.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return nil
			}
			var req Envelope
			if json.Unmarshal([]byte(msg.Payload), &req) != nil || req.ID == "" {
				continue
			}
			userID := strings.TrimPrefix(msg.Channel, b.prefix+"req:")
			go b.serve(ctx, userID, req)
		}
	}
}

// serve dispatches one request to the local Hub and publishes the response.
// No connected device → immediate error response so the engine fails fast.
func (b *RedisBridge) serve(parent context.Context, userID string, req Envelope) {
	respType := ResponseTypeFor(req.Type)
	if respType == "" {
		b.publishResp(parent, Envelope{ID: req.ID, Type: TypeExecResponse, Error: "device bridge: unknown request type " + req.Type})
		return
	}
	if !b.Hub.Has(userID) {
		b.publishResp(parent, Envelope{ID: req.ID, Type: respType, Error: ErrNoDevice.Error()})
		return
	}
	timeout := bridgeTimeout(req.TimeoutMs)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	resp, err := b.Hub.dispatchWithID(ctx, userID, req)
	if err != nil {
		// ErrNoDevice (device disconnected mid-flight) and timeout both surface as
		// an error response so the engine gets a definitive answer.
		resp = Envelope{ID: req.ID, Type: respType, Error: err.Error()}
	}
	resp.ID = req.ID
	resp.Type = respType
	b.publishResp(parent, resp)
}

func (b *RedisBridge) publishResp(ctx context.Context, resp Envelope) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = b.rdb.Publish(ctx, b.prefix+"resp:"+resp.ID, payload).Err()
}

// bridgeTimeout derives the per-request wait for the gateway→device leg from
// the request's TimeoutMs (the value the engine set on the Envelope). 0 → a
// sane floor so a missing cap can't wedge a bridge goroutine forever.
func bridgeTimeout(timeoutMs int) time.Duration {
	if timeoutMs > 0 {
		return time.Duration(timeoutMs) * time.Millisecond
	}
	return 30 * time.Second
}

// newRequestIDHex returns a 128-bit random hex ID. Collision probability across
// concurrent engine processes is negligible, so the same-ID response crossover
// risk is effectively zero.
func newRequestIDHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
