package offers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/notify"
)

// push queues a notification in the same transaction.
func (s *Service) push(ctx context.Context, tx pgx.Tx, now time.Time, userID uuid.UUID, kind, title, body string, data map[string]string) error {
	return notify.Enqueue(ctx, tx, now, notify.Message{UserID: userID, Kind: kind, Title: title, Body: body, Data: data})
}

// openRound opens round [round] of a series' offer to every eligible driver:
// live, paid, the series' vehicle type and city; minus drivers who said "Not
// for me" to this request, and (in rounds 2 and 3) those who clashed or
// withdrew in an earlier round of this attempt. With nobody to offer it to,
// the series goes to no_driver_found instead. The series must be locked.
func (s *Service) openRound(ctx context.Context, tx pgx.Tx, se seriesRow, round int, now time.Time) error {
	today := s.today(now)
	rows, err := tx.Query(ctx, `select d.user_id from drivers d
		where d.status = 'live' and d.subscription_until >= $1 and d.vehicle_type = $2 and d.city = $3
		  and not exists (select 1 from offer_recipients r join offers o on o.id = r.offer_id
		                  where o.series_id = $4 and o.kind = 'series' and r.driver_id = d.user_id
		                    and (r.dismissed_at is not null
		                         or (o.attempt = $5 and r.result in ('clashed', 'withdrew'))))
		order by d.name, d.user_id`, today, se.VehicleType, se.City, se.ID, se.Attempt)
	if err != nil {
		return err
	}
	recipients, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return s.noDriverFound(ctx, tx, se, now)
	}
	expires := now.Add(s.cfg().SeriesWindow)
	var offerID uuid.UUID
	err = tx.QueryRow(ctx, `insert into offers (kind, series_id, attempt, round, status, opened_at, expires_at, created_at)
		values ('series', $1, $2, $3, 'open', $4, $5, $4) returning id`, se.ID, se.Attempt, round, now, expires).Scan(&offerID)
	if err != nil {
		return err
	}
	// Interest still standing from the previous round carries over.
	carried := map[uuid.UUID]time.Time{}
	if round > 1 {
		rows, err := tx.Query(ctx, `select r.driver_id, r.interested_at from offer_recipients r join offers o on o.id = r.offer_id
			where o.series_id = $1 and o.kind = 'series' and o.attempt = $2 and o.round = $3
			  and r.interested_at is not null and r.result is null`, se.ID, se.Attempt, round-1)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d uuid.UUID
			var at time.Time
			if err := rows.Scan(&d, &at); err != nil {
				rows.Close()
				return err
			}
			carried[d] = at
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	slots, err := readSlots(ctx, tx, se.ID)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s → %s, %s, ₹%d a ride", label(se.Pickup.Label, "Pickup"), label(se.Drop.Label, "Drop-off"),
		scheduleLine(slots), se.SuggestedFare)
	data := map[string]string{"offer_id": offerID.String(), "series_id": se.ID.String()}
	for _, d := range recipients {
		var interested *time.Time
		if at, ok := carried[d]; ok {
			interested = &at
		}
		if _, err := tx.Exec(ctx, `insert into offer_recipients (offer_id, driver_id, notified_at, interested_at, created_at)
			values ($1, $2, $3, $4, $3)`, offerID, d, now, interested); err != nil {
			return err
		}
		switch {
		case round == 1:
			err = s.push(ctx, tx, now, d, "offer_new", "New regular ride", line, data)
		case interested == nil:
			err = s.push(ctx, tx, now, d, "offer_still_open", "Still open", line, data)
		}
		if err != nil {
			return err
		}
	}
	return event(ctx, tx, now, nil, "offer", offerID, "offer.opened", map[string]any{
		"series_id": se.ID, "round": round, "attempt": se.Attempt, "recipients": len(recipients), "expires_at": expires,
	})
}

// noDriverFound ends the search: the rider is told and can try again.
func (s *Service) noDriverFound(ctx context.Context, tx pgx.Tx, se seriesRow, now time.Time) error {
	if _, err := tx.Exec(ctx, "update series set status = 'no_driver_found', version = version + 1, updated_at = $2 where id = $1",
		se.ID, now); err != nil {
		return err
	}
	if err := event(ctx, tx, now, nil, "series", se.ID, "series.no_driver_found",
		map[string]any{"old_status": se.Status, "new_status": "no_driver_found"}); err != nil {
		return err
	}
	return s.push(ctx, tx, now, se.RiderID, "series_no_driver", "No driver yet", "No driver yet. Try again or change the time.",
		map[string]string{"series_id": se.ID.String()})
}

func label(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
