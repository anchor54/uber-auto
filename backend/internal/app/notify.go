package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"commute-backend/internal/db"
)

// Notifications are written to an outbox (the `notifications` table) and
// sent by the worker. The table is unique on (kind, ride, recipient), so
// queueing the same one twice does nothing and a restart can't double-send.
//
// Kinds: leg_is_on, driver_on_the_way, driver_arrived, driver_running_late,
// driver_alert, rider_running_late, payment_reminder.

type Recipient struct {
	Type string // rider | driver
	ID   string
}

type notification struct {
	To        Recipient
	Kind      string
	RideID    string
	SendAfter time.Time
	Payload   map[string]any
}

func queueNotification(ctx context.Context, q db.Q, n notification) error {
	payload := n.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := q.Exec(ctx, `insert into notifications (recipient_type, recipient_id, kind, ride_id, payload, send_after)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (kind, ride_id, recipient_type, recipient_id) do nothing`,
		n.To.Type, n.To.ID, n.Kind, n.RideID, payload, n.SendAfter)
	return err
}

type PushData struct {
	Kind   string `json:"kind"`
	RideID string `json:"rideId"`
}

type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Lets the app open the right screen from a tap.
	Data PushData `json:"data"`
}

// PushSender sends one push to one device. FCM in production; logs in development.
type PushSender interface {
	Send(ctx context.Context, token string, m PushMessage) error
}

type PushSenderFunc func(ctx context.Context, token string, m PushMessage) error

func (f PushSenderFunc) Send(ctx context.Context, token string, m PushMessage) error {
	return f(ctx, token, m)
}

// LogPushSender logs pushes instead of sending them.
func LogPushSender(log *slog.Logger) PushSender {
	return PushSenderFunc(func(_ context.Context, token string, m PushMessage) error {
		if len(token) > 12 {
			token = token[:12]
		}
		log.Info(fmt.Sprintf("[push] %s…: %s: %s", token, m.Title, m.Body))
		return nil
	})
}

// MessageFor is the text for each kind. [p] is the payload queued with it.
func MessageFor(kind string, p map[string]any) (title, body string) {
	driver := "Your driver"
	if v, ok := p["driverFirstName"]; ok && v != nil {
		driver = fmt.Sprint(v)
	}
	time := ""
	if v, ok := p["pickupTime"]; ok && v != nil {
		time = fmt.Sprint(v)
	}
	switch kind {
	case "leg_is_on":
		return "Your ride is on", fmt.Sprintf("%s picks you up at %s.", driver, time)
	case "driver_on_the_way":
		return driver + " is on the way", "Track your ride live."
	case "driver_arrived":
		return driver + " has arrived", "Your driver is waiting at the pickup point."
	case "driver_running_late":
		return driver + " is running late", "Open the app to see the new arrival time."
	case "driver_alert":
		return driver + " is waiting for you", "Your driver is at the pickup point."
	case "rider_running_late":
		return "Your rider is 5 min late", "Their wait now runs 10 minutes."
	case "payment_reminder":
		return "How was this ride paid?", "Mark the fare as paid so both of you have a record."
	}
	return "Commute", "Open the app for an update."
}

const maxTries = 5

// SendDueNotifications is the worker job that sends due notifications to
// every device of the recipient. Rows are claimed with FOR UPDATE SKIP
// LOCKED so two workers never send the same one. A failure is retried with
// backoff (1, 2, 4, 8 minutes), up to 5 tries.
func (a *App) SendDueNotifications(ctx context.Context, sender PushSender) (int, error) {
	sent := 0
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		type due struct {
			id                         int64
			recipientType, recipientID string
			kind, rideID               string
			payload                    map[string]any
			tries                      int
		}
		rows, err := tx.Query(ctx, `select id, recipient_type, recipient_id, kind, ride_id, payload, tries from notifications
			where sent_at is null and send_after <= $1 and tries < $2
			order by send_after limit $3 for update skip locked`, a.now(), maxTries, 50)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (due, error) {
			var n due
			err := row.Scan(&n.id, &n.recipientType, &n.recipientID, &n.kind, &n.rideID, &n.payload, &n.tries)
			return n, err
		})
		if err != nil {
			return err
		}
		for _, n := range list {
			rows, err := tx.Query(ctx, "select push_token from devices where owner_type = $1 and owner_id = $2", n.recipientType, n.recipientID)
			if err != nil {
				return err
			}
			tokens, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			title, body := MessageFor(n.kind, n.payload)
			var sendErr error
			for _, token := range tokens {
				sendErr = sender.Send(ctx, token, PushMessage{title, body, PushData{n.kind, n.rideID}})
				if errors.Is(sendErr, ErrUnregistered) {
					// The app is gone from that device: forget it and carry on.
					if _, err := tx.Exec(ctx, "delete from devices where push_token = $1", token); err != nil {
						return err
					}
					sendErr = nil
				}
				if sendErr != nil {
					break
				}
			}
			if sendErr == nil {
				// No device is not an error: the rider sees the state in the app.
				if _, err := tx.Exec(ctx, "update notifications set sent_at = $2, tries = tries + 1 where id = $1", n.id, a.now()); err != nil {
					return err
				}
				sent++
				continue
			}
			msg := sendErr.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
			backoff := time.Duration(1<<n.tries) * time.Minute
			if _, err := tx.Exec(ctx, "update notifications set tries = tries + 1, last_error = $2, send_after = $3 where id = $1",
				n.id, msg, a.now().Add(backoff)); err != nil {
				return err
			}
		}
		return nil
	})
	return sent, err
}
