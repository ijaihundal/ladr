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

	"github.com/coder/websocket"
)

// wsTransport is the device-side Transport over a WebSocket connection to the
// gateway. It is what device.Client.Serve reads exec requests from and writes
// exec responses to.
type wsTransport struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (t *wsTransport) Recv() (Envelope, error) {
	_, data, err := t.conn.Read(context.Background())
	if err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (t *wsTransport) Send(env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.conn.Write(context.Background(), websocket.MessageText, data)
}

func (t *wsTransport) Close() error {
	return t.conn.Close(websocket.StatusNormalClosure, "device closing")
}

// Dial connects a device client to the gateway's device-channel endpoint and
// returns a Transport for Client.Serve. The token is the user's session
// credential (the existing OAuth2 rdo_ access token; see apps/foundry OAuth flow).
//
// Once connected, the caller runs:  go client.Serve(ctx);  and the device is
// eligible to receive tools.codeExecution calls from any ladder the user runs.
func Dial(ctx context.Context, gatewayURL string, token string) (Transport, error) {
	hdr := http.Header{}
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	c, _, err := websocket.Dial(ctx, gatewayURL, &websocket.DialOptions{
		HTTPHeader: hdr,
	})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	return &wsTransport{conn: c}, nil
}
