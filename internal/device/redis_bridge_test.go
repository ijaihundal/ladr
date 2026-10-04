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
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

)

// newTestRedis stands up a miniredis and returns a client pointed at it. The
// device channel's cross-process path is pure Redis pub/sub, so miniredis
// exercises it faithfully without a real broker.
func newTestRedis(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// waitForPubSub gives go-redis a moment to register its PSubscribe before
// messages flow. In production the engine subscribes once at startup long
// before any ladder runs, so this race only exists in tightly-coupled tests.
func waitForPubSub() { time.Sleep(150 * time.Millisecond) }

// TestRedisBridge_CrossProcessDispatch is the headline Phase 3 proof: an engine
// process (RedisDispatcher) and a gateway process (Hub + RedisBridge + a device
// over loopback) communicate ONLY through Redis. A dispatch from the engine
// reaches the device, runs, and the result returns — exactly the real topology
// where engine and gateway are separate binaries.
func TestRedisBridge_CrossProcessDispatch(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// --- gateway side: hub holds the device connection; bridge ties it to Redis.
	hub := NewHub()
	gw, dev := loopback(t, hub, "user-1")
	hub.Register(gw)
	backend := &fakeBackend{result: ExecResult{Stdout: "ran-on-device", ExitCode: 0}}
	go func() { _ = (&Client{Exec: backend, Transport: dev}).Serve(context.Background()) }()
	bridge := NewRedisBridge(hub, rdb)
	go func() { _ = bridge.Run(ctx) }()

	// --- engine side: dispatches reach the device over Redis.
	rd := NewRedisDispatcher(rdb)
	go func() { _ = rd.Run(ctx) }()
	waitForPubSub()

	resp, err := rd.Dispatch(ctx, "user-1", Envelope{
		SessionID: "sess-x", Runtime: "python", Code: "print(1)", TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.Stdout != "ran-on-device" {
		t.Errorf("stdout = %q, want ran-on-device", resp.Stdout)
	}
	if resp.Type != TypeExecResponse {
		t.Errorf("type = %q, want %q", resp.Type, TypeExecResponse)
	}
	if n := backend.callsCount(); n != 1 {
		t.Errorf("device ran %d times, want 1", n)
	}
}

// TestRedisBridge_HubChannelAcrossRedis: the full engine seam —
// HubChannel(Dispatcher=RedisDispatcher) → Redis → bridge → hub → device —
// proving the cross-process path end to end (no in-process Hub shared between
// engine and gateway).
func TestRedisBridge_HubChannelAcrossRedis(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewHub()
	gw, dev := loopback(t, hub, "user-9")
	hub.Register(gw)
	go func() {
		_ = (&Client{Exec: &fakeBackend{result: ExecResult{Stdout: "ok-9"}}, Transport: dev}).Serve(context.Background())
	}()
	go func() { _ = NewRedisBridge(hub, rdb).Run(ctx) }()

	rd := NewRedisDispatcher(rdb)
	go func() { _ = rd.Run(ctx) }()
	waitForPubSub()

	// The engine's HubChannel (what a hosted engine wires as its code-execution
	// seam) routes through the dispatcher; userID is the instance owner.
	ch := &HubChannel{Dispatcher: rd}
	res, err := ch.Exec(ctx, "user-9", "s9", ExecParams{
		SessionID: "s9", Runtime: RuntimeBash, Code: "echo ok",
	})
	if err != nil {
		t.Fatalf("HubChannel.Exec across Redis: %v", err)
	}
	if res.Stdout != "ok-9" {
		t.Errorf("stdout = %q, want ok-9", res.Stdout)
	}
}

// TestRedisBridge_FsAcrossRedis: fs ops cross the Redis topology with the same
// correlation machinery as exec — the hosted engine's FilesOp seam proves out
// cross-process.
func TestRedisBridge_FsAcrossRedis(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewHub()
	gw, dev := loopback(t, hub, "user-fs9")
	hub.Register(gw)
	serveFsResponder(t, dev)
	go func() { _ = NewRedisBridge(hub, rdb).Run(ctx) }()

	rd := NewRedisDispatcher(rdb)
	go func() { _ = rd.Run(ctx) }()
	waitForPubSub()

	ch := &HubChannel{Dispatcher: rd}
	resp, err := ch.FsRead(ctx, "user-fs9", "ladder-9", "report.md")
	if err != nil {
		t.Fatalf("FsRead across Redis: %v", err)
	}
	if resp.Type != TypeFsReadResponse || resp.Content != "contents of report.md" {
		t.Errorf("read response = %+v", resp)
	}
}

// TestRedisBridge_NoDeviceFastError: a request for a user with no connected
// device is answered immediately with an error response (the engine does not
// wait out its full timeout).
func TestRedisBridge_NoDeviceFastError(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hub := NewHub() // no device registered
	go func() { _ = NewRedisBridge(hub, rdb).Run(ctx) }()

	rd := NewRedisDispatcher(rdb)
	go func() { _ = rd.Run(ctx) }()
	waitForPubSub()

	start := time.Now()
	resp, err := rd.Dispatch(ctx, "ghost-user", Envelope{
		Runtime: "python", Code: "x", TimeoutMs: 5000,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Dispatch returned Go error %v; want an error *response*", err)
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "no client connected") {
		t.Errorf("error = %q, want it to mention no client connected", resp.Error)
	}
	// Must return well under the 5s timeout — proving fast-fail, not timeout.
	if elapsed > 2*time.Second {
		t.Errorf("took %s; no-device should fast-fail, not wait out the timeout", elapsed)
	}
}

// TestRedisBridge_DispatchTimeout: when the bridge is running but the device
// never responds, the engine's context deadline surfaces (no leaked pending).
func TestRedisBridge_DispatchTimeout(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	hub := NewHub()
	// Register a connection that swallows requests and never responds.
	hub.Register(&silentConn{userID: "user-slow"})
	go func() { _ = NewRedisBridge(hub, rdb).Run(ctx) }()

	rd := NewRedisDispatcher(rdb)
	go func() { _ = rd.Run(ctx) }()
	waitForPubSub()

	dctx, dcancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer dcancel()
	_, err := rd.Dispatch(dctx, "user-slow", Envelope{Runtime: "python", Code: "x", TimeoutMs: 5000})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	rd.mu.Lock()
	leaked := len(rd.pending)
	rd.mu.Unlock()
	if leaked != 0 {
		t.Errorf("pending map leaked %d entries after timeout", leaked)
	}
}

// silentConn is a Connection that accepts requests but never responds — used to
// force the engine dispatch to hit its context deadline.
type silentConn struct {
	userID string
}

func (c *silentConn) Send(_ context.Context, _ Envelope) error { return nil }
func (c *silentConn) Close() error                             { return nil }
func (c *silentConn) UserID() string                           { return c.userID }
