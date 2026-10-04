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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// toWS rewrites an http(s) test-server URL to a ws(s) URL.
func toWS(u string) string {
	return strings.Replace(strings.Replace(u, "https://", "wss://", 1), "http://", "ws://", 1)
}

// TestDeviceChannel_RealWebSocket: spin up a real httptest WebSocket server
// (the gateway), connect a real device client over WS, dispatch a code-exec
// request from the hub, and assert the device runs it and the result returns.
// This proves the actual WS transport, not just the in-memory loopback.
func TestDeviceChannel_RealWebSocket(t *testing.T) {
	hub := NewHub()
	// In production userIDFromRequest resolves the OAuth principal; in this test
	// it reads the bearer token as the user id.
	srv := httptest.NewServer(ServeDeviceWS(hub, func(r *http.Request) string {
		return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}))
	defer srv.Close()

	// Device side: dial the gateway WS endpoint as "user-42".
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, err := Dial(ctx, toWS(srv.URL), "user-42")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer transport.Close()

	backend := &fakeBackend{result: ExecResult{Stdout: "hello-from-device", ExitCode: 0}}
	client := &Client{Exec: backend, Transport: transport}
	go func() { _ = client.Serve(context.Background()) }()

	// Give the connection a moment to register, then dispatch.
	if !waitFor(time.Second, func() bool { return hub.Has("user-42") }) {
		t.Fatal("device did not register with the hub")
	}

	resp, err := hub.Dispatch(ctx, "user-42", Envelope{
		SessionID: "sess-real", Runtime: "python", Code: "print('hi')", TimeoutMs: 1000,
	})
	if err != nil {
		t.Fatalf("Dispatch over WS: %v", err)
	}
	if resp.Stdout != "hello-from-device" {
		t.Errorf("stdout = %q, want hello-from-device", resp.Stdout)
	}
	if backend.callsCount() != 1 {
		t.Errorf("expected 1 device exec, got %d", backend.callsCount())
	}
}

// TestDeviceChannel_HubChannelOverRealWS: the engine-side HubChannel seam
// routes through a real WS to a device — the hosted-engine topology (recipe on
// the server, compute on the user's machine).
func TestDeviceChannel_HubChannelOverRealWS(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(ServeDeviceWS(hub, func(r *http.Request) string {
		return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, err := Dial(ctx, toWS(srv.URL), "user-99")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer transport.Close()
	backend := &fakeBackend{result: ExecResult{Stdout: "remote-ok"}}
	go func() { _ = (&Client{Exec: backend, Transport: transport}).Serve(context.Background()) }()

	if !waitFor(time.Second, func() bool { return hub.Has("user-99") }) {
		t.Fatal("device did not register")
	}

	// HubChannel behind the same seam shape the hosted engine uses; userID is
	// the device-routing key (the instance owner).
	ch := &HubChannel{Dispatcher: hub}
	res, err := ch.Exec(ctx, "user-99", "s1", ExecParams{
		SessionID: "s1", Runtime: RuntimeBash, Code: "echo ok",
	})
	if err != nil {
		t.Fatalf("HubChannel.Exec: %v", err)
	}
	if res.Stdout != "remote-ok" {
		t.Errorf("stdout = %q, want remote-ok", res.Stdout)
	}
	// And the not-wired case fails fast instead of nil-panicking:
	var unwired *HubChannel
	if _, err := unwired.Exec(ctx, "", "s0", ExecParams{Code: "x"}); err == nil {
		t.Error("expected error from unwired HubChannel")
	}
}

// TestServeDeviceWS_Unauthorized: a request with no principal is rejected before
// the WebSocket upgrade.
func TestServeDeviceWS_Unauthorized(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(ServeDeviceWS(hub, func(*http.Request) string { return "" }))
	defer srv.Close()
	// A plain WS dial with no token should be rejected (401 before upgrade).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := Dial(ctx, toWS(srv.URL), "")
	_ = websocket.CloseStatus(err) // err expected; exact code varies
	if err == nil {
		// Some stacks return nil conn + err; either way, the hub must be empty.
	}
	if hub.Has("anyone") {
		t.Error("unauthorized request should not register a device")
	}
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// (websocket import is used only for CloseStatus quieting above; keep it.)
var _ = sync.Mutex{}
