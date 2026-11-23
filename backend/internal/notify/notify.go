// Package notify is the notification outbox and push delivery. Business code
// never pushes: it writes an outbox row in its own transaction (Enqueue), and
// the sweep's send step (Outbox.SendDue) delivers it.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/clock"
)

// Message is one push to one user (to each of their devices).
type Message struct {
	UserID uuid.UUID
	Kind   string
	Title  string
	Body   string
	// Lets the app open the right screen from a tap.
	Data map[string]string
}

// Pusher delivers a message to a user's devices. It returns the tokens that
// are no longer registered, which are then forgotten. Called once per
// message even when the user has no device, so a recorder sees every one.
type Pusher interface {
	Push(ctx context.Context, tokens []string, m Message) (dead []string, err error)
}

// LogPusher logs pushes instead of sending them (development, and production
// before Firebase is set up).
type LogPusher struct{ Log *slog.Logger }

func (l LogPusher) Push(_ context.Context, tokens []string, m Message) ([]string, error) {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info(fmt.Sprintf("[push] %s (%d devices): %s: %s", m.UserID, len(tokens), m.Title, m.Body))
	return nil, nil
}

// Execer is a transaction (or pool).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type payload struct {
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data"`
}

// Enqueue writes an outbox row, to be sent from [sendAfter] on.
func Enqueue(ctx context.Context, q Execer, sendAfter time.Time, m Message) error {
	data := m.Data
	if data == nil {
		data = map[string]string{}
	}
	if m.Kind != "" {
		data["kind"] = m.Kind
	}
	p, err := json.Marshal(payload{m.Title, m.Body, data})
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `insert into notification_outbox (user_id, kind, payload, send_after, created_at)
		values ($1, $2, $3, $4, $4)`, m.UserID, m.Kind, p, sendAfter)
	return err
}

// MaxAttempts is how many times a push is tried before it is given up.
const MaxAttempts = 5

// Outbox sends due outbox rows.
type Outbox struct {
	DB     *pgxpool.Pool
	Clock  clock.Clock
	Pusher Pusher
	Log    *slog.Logger
}

// SendDue sends every due row, each in its own transaction. Rows are claimed
// with FOR UPDATE SKIP LOCKED, so two sweeps never send the same one. A
// failure is retried with backoff (1, 2, 4, 8 minutes), 5 attempts at most.
func (o *Outbox) SendDue(ctx context.Context) (int, error) {
	sent := 0
	for range 1000 {
		did, ok, err := o.sendOne(ctx)
		if err != nil {
			return sent, err
		}
		if !did {
			break
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

func (o *Outbox) sendOne(ctx context.Context) (did, ok bool, err error) {
	now := o.Clock.Now()
	err = pgx.BeginFunc(ctx, o.DB, func(tx pgx.Tx) error {
		var id, userID uuid.UUID
		var kind string
		var raw []byte
		var attempts int
		err := tx.QueryRow(ctx, `select id, user_id, kind, payload, attempts from notification_outbox
			where sent_at is null and send_after <= $1 and attempts < $2
			order by send_after, seq limit 1 for update skip locked`, now, MaxAttempts).
			Scan(&id, &userID, &kind, &raw, &attempts)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		did = true
		var p payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "select fcm_token from devices where user_id = $1 order by updated_at desc", userID)
		if err != nil {
			return err
		}
		tokens, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		dead, sendErr := o.Pusher.Push(ctx, tokens, Message{UserID: userID, Kind: kind, Title: p.Title, Body: p.Body, Data: p.Data})
		for _, t := range dead {
			if _, err := tx.Exec(ctx, "delete from devices where fcm_token = $1", t); err != nil {
				return err
			}
		}
		if sendErr == nil {
			ok = true
			_, err = tx.Exec(ctx, "update notification_outbox set sent_at = $2, attempts = attempts + 1 where id = $1", id, now)
			return err
		}
		msg := sendErr.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		backoff := time.Duration(1<<attempts) * time.Minute
		_, err = tx.Exec(ctx, "update notification_outbox set attempts = attempts + 1, last_error = $2, send_after = $3 where id = $1",
			id, strings.TrimSpace(msg), now.Add(backoff))
		return err
	})
	return did, ok, err
}
