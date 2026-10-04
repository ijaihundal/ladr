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

// Package mailbox — durable, program-addressed message passing between
// ladders. The mailbox is addressed to (owner, ladder): the agent-PROGRAM,
// not any live process. A message posted now is drained by a run that has
// not started yet — coordination without simultaneity.
//
// v1 drain = consume: messages leave the table when injected. Ordering by
// post time (the bigserial id). Payloads stay in reliks; envelopes carry a
// relik-ref pointer.
package mailbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ladr/ladr/internal/ladr"
)

// Envelope is one message: a signal, not the data plane.
type Envelope struct {
	ID             int64              `json:"id"`
	Sender         string             `json:"from"`
	Type           string             `json:"type"`
	CorrelationID  string             `json:"correlationId,omitempty"`
	RelikRef       *ladr.RelikTarget  `json:"relikRef,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}

// Store is the mailbox contract (pg in production, memory in tests).
type Store interface {
	// Post leaves a message for (owner, ladder).
	Post(ctx context.Context, owner, ladder string, e Envelope) error
	// Drain consumes up to limit messages addressed to (owner, ladder),
	// oldest first. limit <= 0 means 50.
	Drain(ctx context.Context, owner, ladder string, limit int) ([]Envelope, error)
}

// Format renders drained envelopes as the injected text. The model sees
// signals + pointers; it fetches payloads from reliks on demand.
func Format(envelopes []Envelope) string {
	if len(envelopes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range envelopes {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("[mail %d from %s · %s", e.ID, e.Sender, e.Type))
		if e.CorrelationID != "" {
			b.WriteString(" · corr " + e.CorrelationID)
		}
		if e.RelikRef != nil {
			b.WriteString(fmt.Sprintf(" · payload at %s/%s/%s", e.RelikRef.Relik, e.RelikRef.Folder, e.RelikRef.File))
		}
		b.WriteString("]")
	}
	return b.String()
}

// --- memory implementation (engine tests, local tooling) -------------------

type MemStore struct {
	mu    sync.Mutex
	next  int64
	items []addrEnvelope
}

type addrEnvelope struct {
	owner, ladder string
	env           Envelope
}

func NewMemStore() *MemStore { return &MemStore{} }

func (m *MemStore) Post(_ context.Context, owner, ladder string, e Envelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	e.ID = m.next
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	m.items = append(m.items, addrEnvelope{owner, ladder, e})
	return nil
}

func (m *MemStore) Drain(_ context.Context, owner, ladder string, limit int) ([]Envelope, error) {
	if limit <= 0 {
		limit = 50
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Envelope
	kept := m.items[:0]
	for _, it := range m.items {
		if it.owner == owner && it.ladder == ladder && len(out) < limit {
			out = append(out, it.env)
			continue
		}
		kept = append(kept, it)
	}
	m.items = kept
	return out, nil
}

// --- postgres implementation -----------------------------------------------

type PgStore struct{ pool *pgxpool.Pool }

func (s *PgStore) Post(ctx context.Context, owner, ladder string, e Envelope) error {
	// nil relik-ref must arrive as SQL NULL — an empty string is invalid
	// jsonb input and the insert would fail.
	var ref any
	if e.RelikRef != nil {
		b, _ := json.Marshal(e.RelikRef)
		ref = string(b)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mailbox_messages (owner_id, ladder, sender, type, correlation_id, relik_ref)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, owner, ladder, e.Sender, e.Type, e.CorrelationID, ref)
	return err
}

// Drain consumes: DELETE ... RETURNING removes the rows as they're read —
// v1 semantics (drain = consume; acks/keep-marking are v2).
func (s *PgStore) Drain(ctx context.Context, owner, ladder string, limit int) ([]Envelope, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		DELETE FROM mailbox_messages
		WHERE id IN (
		  SELECT id FROM mailbox_messages WHERE owner_id = $1 AND ladder = $2
		  ORDER BY id LIMIT $3
		)
		RETURNING id, sender, type, correlation_id, relik_ref, created_at
	`, owner, ladder, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Envelope
	for rows.Next() {
		var e Envelope
		var ref []byte
		if err := rows.Scan(&e.ID, &e.Sender, &e.Type, &e.CorrelationID, &ref, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(ref) > 0 {
			var t ladr.RelikTarget
			if json.Unmarshal(ref, &t) == nil {
				e.RelikRef = &t
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
