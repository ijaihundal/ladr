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
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// wsConnection is the gateway-side, transport-agnostic Connection over a
// WebSocket. Writes are mutex-guarded (coder/websocket requires non-concurrent
// writes); the read loop runs in the HTTP handler goroutine and feeds responses
// into the hub via Deliver.
type wsConnection struct {
	conn    *websocket.Conn
	userID  string
	writeMu sync.Mutex
}

func (c *wsConnection) Send(ctx context.Context, env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *wsConnection) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "gateway closing")
}

func (c *wsConnection) UserID() string { return c.userID }

// PresenceStore records which users have a connected device. The hosted
// engine uses it for run pre-flight (rejecting code/files ladders when no
// device is connected) via short-lived presence keys. A nil store disables
// tracking — local deployments don't pre-flight.
type PresenceStore interface {
	// DeviceOnline marks the user's device as present and refreshes the TTL.
	// Called on connect and periodically while the connection lives.
	DeviceOnline(ctx context.Context, userID string)
	// DeviceOffline clears presence. Called only when the closing connection
	// still owned the user's hub slot, so a newer connection survives.
	DeviceOffline(ctx context.Context, userID string)
}

const (
	// PresenceTTL bounds a presence key; the gateway refreshes it well before
	// expiry while the device stays connected.
	PresenceTTL = 90 * time.Second
	// presenceRefresh is the refresh cadence for live connections.
	presenceRefresh = 60 * time.Second
)

// PresenceKey returns the presence-key name for a user. The hosted engine's
// run pre-flight checks the same key before starting code/files ladders.
func PresenceKey(userID string) string {
	return "ladr:dev:presence:" + userID
}

// ServeDeviceWS returns an http.HandlerFunc that upgrades to a WebSocket and
// registers the authenticated client device with the hub. userIDFromRequest
// resolves the authenticated user (production: the OAuth session principal;
// tests: a query/header value). The handler blocks for the connection's life,
// running the read loop that delivers client responses into the hub. If the
// hub has a PresenceStore, connects/offlines are reported to it.
//
// Mount this behind authenticated routing on the gateway, e.g. at /v1/device.
func ServeDeviceWS(hub *Hub, userIDFromRequest func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := ""
		if userIDFromRequest != nil {
			uid = userIDFromRequest(r)
		}
		if uid == "" {
			http.Error(w, "device channel: unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// Origin checks should be enforced by gateway middleware in production.
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		// Bound the connection to a sane read limit to protect the gateway.
		c.SetReadLimit(1 << 20) // 1 MiB max envelope
		defer c.CloseNow()

		conn := &wsConnection{conn: c, userID: uid}
		hub.Register(conn)
		defer func() {
			// Offline only when this connection still owned the user's slot —
			// a newer connection for the same user must keep its presence.
			if hub.Unregister(conn) && hub.Presence != nil {
				hub.Presence.DeviceOffline(context.Background(), uid)
			}
		}()

		ctx := r.Context()
		if hub.Presence != nil {
			hub.Presence.DeviceOnline(ctx, uid)
			ticker := time.NewTicker(presenceRefresh)
			defer ticker.Stop()
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						hub.Presence.DeviceOnline(ctx, uid)
					}
				}
			}()
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return // client gone / closed
			}
			var env Envelope
			if json.Unmarshal(data, &env) == nil && env.ID != "" {
				hub.Deliver(env)
			}
		}
	}
}
