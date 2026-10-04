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
	"errors"
	"sync"
	"testing"
	"time"

)

// fakeBackend is a scriptable Backend used in tests.
type fakeBackend struct {
	mu     sync.Mutex
	calls  []ExecParams
	result ExecResult
	err    error
}

func (f *fakeBackend) Exec(_ context.Context, p ExecParams) (ExecResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	return f.result, f.err
}

func (f *fakeBackend) callsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// loopback wires a gateway Connection to a device Transport via two channels,
// with a goroutine forwarding device→gateway messages into the hub's Deliver.
func loopback(t *testing.T, hub *Hub, userID string) (*loopbackConn, Transport) {
	t.Helper()
	toDevice := make(chan Envelope, 8)
	toGw := make(chan Envelope, 8)
	gw := &loopbackConn{userID: userID, toDevice: toDevice, toGw: toGw}
	dev := &loopbackTransport{toDevice: toDevice, toGw: toGw}
	// gateway read loop: deliver responses to the hub.
	go func() {
		for env := range toGw {
			hub.Deliver(env)
		}
	}()
	return gw, dev
}

type loopbackConn struct {
	userID   string
	toDevice chan Envelope
	toGw     chan Envelope
	closed   bool
	mu       sync.Mutex
}

func (c *loopbackConn) Send(_ context.Context, env Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("connection closed")
	}
	c.toDevice <- env
	return nil
}
func (c *loopbackConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	close(c.toGw)
	return nil
}
func (c *loopbackConn) UserID() string { return c.userID }

type loopbackTransport struct {
	toDevice chan Envelope
	toGw     chan Envelope
}

func (t *loopbackTransport) Recv() (Envelope, error) {
	env, ok := <-t.toDevice
	if !ok {
		return Envelope{}, errors.New("transport closed")
	}
	return env, nil
}
func (t *loopbackTransport) Send(env Envelope) error {
	t.toGw <- env
	return nil
}
func (t *loopbackTransport) Close() error { close(t.toGw); return nil }

// TestDeviceChannel_LoopbackDispatch: gateway dispatches an exec request → the
// device client runs it via its local executor → the result returns to the
// gateway. This is the federated execution round-trip, in-memory.
func TestDeviceChannel_LoopbackDispatch(t *testing.T) {
	hub := NewHub()
	gw, dev := loopback(t, hub, "user-1")
	hub.Register(gw)

	backend := &fakeBackend{result: ExecResult{Stdout: "42", ExitCode: 0}}
	client := &Client{Exec: backend, Transport: dev}
	go func() { _ = client.Serve(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := hub.Dispatch(ctx, "user-1", Envelope{
		SessionID: "sess-A", Runtime: "python", Code: "print(6*7)", TimeoutMs: 1000,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.Stdout != "42" {
		t.Errorf("stdout = %q, want 42", resp.Stdout)
	}
	if backend.callsCount() != 1 {
		t.Fatalf("expected 1 device-side exec, got %d", backend.callsCount())
	}
	backend.mu.Lock()
	got := backend.calls[0]
	backend.mu.Unlock()
	if got.Code != "print(6*7)" || got.Runtime != RuntimePython || got.SessionID != "sess-A" {
		t.Errorf("device exec params = %+v", got)
	}
}

// TestDeviceChannel_HubChannelAdapter: HubChannel is the engine-side seam —
// Exec routes through the hub to the device and back.
func TestDeviceChannel_HubChannelAdapter(t *testing.T) {
	hub := NewHub()
	gw, dev := loopback(t, hub, "user-7")
	hub.Register(gw)
	backend := &fakeBackend{result: ExecResult{Stdout: "ok"}}
	go func() { _ = (&Client{Exec: backend, Transport: dev}).Serve(context.Background()) }()

	ch := &HubChannel{Dispatcher: hub}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := ch.Exec(ctx, "user-7", "sess-1", ExecParams{
		SessionID: "sess-1", Runtime: RuntimeBash, Code: "echo ok",
	})
	if err != nil {
		t.Fatalf("HubChannel.Exec: %v", err)
	}
	if res.Stdout != "ok" {
		t.Errorf("stdout = %q, want ok", res.Stdout)
	}
}

func TestDeviceChannel_NoDeviceConnected(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := hub.Dispatch(ctx, "nobody", Envelope{Code: "x"})
	if !errors.Is(err, ErrNoDevice) {
		t.Errorf("expected ErrNoDevice, got %v", err)
	}
}

func TestDeviceChannel_DispatchTimeout(t *testing.T) {
	hub := NewHub()
	gw, _ := loopback(t, hub, "user-2")
	hub.Register(gw)
	// No device client consuming requests → the dispatch must time out, and the
	// late response (never produced) must not leak the pending entry.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := hub.Dispatch(ctx, "user-2", Envelope{Code: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	// pending must be cleaned up after timeout.
	hub.mu.Lock()
	remaining := len(hub.pending)
	hub.mu.Unlock()
	if remaining != 0 {
		t.Errorf("pending map leaked %d entries after timeout", remaining)
	}
}

func TestDeviceChannel_ConcurrentDispatches(t *testing.T) {
	hub := NewHub()
	gw, dev := loopback(t, hub, "user-3")
	hub.Register(gw)
	backend := &fakeBackend{result: ExecResult{Stdout: "done"}}
	go func() { _ = (&Client{Exec: backend, Transport: dev}).Serve(context.Background()) }()

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := hub.Dispatch(ctx, "user-3", Envelope{Code: "x"})
			if err != nil {
				t.Errorf("concurrent dispatch: %v", err)
			}
		}()
	}
	wg.Wait()
	if backend.callsCount() != n {
		t.Errorf("expected %d device execs, got %d", n, backend.callsCount())
	}
}

// serveFsResponder is a minimal device side that answers every request with
// the response type mapped by ResponseTypeFor and a canned payload per type —
// the shape the beacon TS client implements for fs ops.
func serveFsResponder(t *testing.T, dev Transport) {
	t.Helper()
	go func() {
		for {
			env, err := dev.Recv()
			if err != nil {
				return
			}
			resp := Envelope{ID: env.ID, Type: ResponseTypeFor(env.Type)}
			switch env.Type {
			case TypeFsReadRequest:
				resp.Content = "contents of " + env.Path
			case TypeFsWriteRequest:
				resp.Ok, resp.Bytes = true, len(env.Content)
			case TypeFsListRequest:
				resp.Entries = []string{"a.txt", "sub/b.txt"}
			case TypeExecRequest:
				resp.Stdout = "execed"
			}
			_ = dev.Send(resp)
		}
	}()
}

// TestDeviceChannel_FsRoundTrip proves the type-agnostic dispatch end to end
// (in-process hub): all three fs ops route through the same correlation
// machinery as exec and come back with the right response type + payload.
func TestDeviceChannel_FsRoundTrip(t *testing.T) {
	hub := NewHub()
	gw, dev := loopback(t, hub, "user-fs")
	hub.Register(gw)
	serveFsResponder(t, dev)

	ch := &HubChannel{Dispatcher: hub}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// read
	resp, err := ch.FsRead(ctx, "user-fs", "ladder-1", "docs/a.txt")
	if err != nil {
		t.Fatalf("FsRead: %v", err)
	}
	if resp.Type != TypeFsReadResponse || resp.Content != "contents of docs/a.txt" {
		t.Errorf("read response = %+v", resp)
	}

	// write
	resp, err = ch.FsWrite(ctx, "user-fs", "ladder-1", "docs/a.txt", "hello", false)
	if err != nil {
		t.Fatalf("FsWrite: %v", err)
	}
	if resp.Type != TypeFsWriteResponse || !resp.Ok || resp.Bytes != len("hello") {
		t.Errorf("write response = %+v", resp)
	}

	// list
	resp, err = ch.FsList(ctx, "user-fs", "ladder-1", "docs", "**/*.txt")
	if err != nil {
		t.Fatalf("FsList: %v", err)
	}
	if resp.Type != TypeFsListResponse || len(resp.Entries) != 2 {
		t.Errorf("list response = %+v", resp)
	}
}

// TestDeviceChannel_FsDeniedIsPayloadNotTransport: a device refusal ("denied
// by user") arrives as a well-typed response carrying Error — the transport
// succeeds; the caller decides. This is the consent model's wire shape.
func TestDeviceChannel_FsDeniedIsPayloadNotTransport(t *testing.T) {
	hub := NewHub()
	gw, dev := loopback(t, hub, "user-deny")
	hub.Register(gw)
	go func() {
		env, err := dev.Recv()
		if err != nil {
			return
		}
		_ = dev.Send(Envelope{ID: env.ID, Type: ResponseTypeFor(env.Type), Error: "denied by user"})
	}()

	ch := &HubChannel{Dispatcher: hub}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := ch.FsRead(ctx, "user-deny", "ladder-1", "secret.txt")
	if err != nil {
		t.Fatalf("denial must be a payload error, not a transport error: %v", err)
	}
	if resp.Error != "denied by user" {
		t.Errorf("response error = %q, want 'denied by user'", resp.Error)
	}
}
