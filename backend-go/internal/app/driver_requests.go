package app

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"commute-backend/internal/domain"
)

// Flow D2: a driver accepts or declines a commute a rider has confirmed.
//
// These service functions are the only code that answers for a driver. The
// driver endpoints call them, and so does the dev simulator, so tests that
// use the simulator stay valid when the driver app ships.

// respondByFor is the earliest of: N hours from now, or an hour before the
// first pickup. Never under 15 minutes away.
func (a *App) respondByFor(startDate string, pickupTime int) time.Time {
	now := a.now()
	firstPickup := domain.LocalInstant(startDate, pickupTime, a.Config.UTCOffsetMinutes)
	byHours := now.Add(time.Duration(a.Config.DriverResponseHours * float64(time.Hour)))
	beforePickup := firstPickup.Add(-time.Hour)
	t := byHours
	if beforePickup.Before(t) {
		t = beforePickup
	}
	if floor := now.Add(15 * time.Minute); floor.After(t) {
		t = floor
	}
	return t
}

// expireUnanswered turns requests nobody answered in time into declined
// ("no answer"), freeing the slot. Safe to run any time.
func (a *App) expireUnanswered(ctx context.Context) (int64, error) {
	tag, err := a.DB.Exec(ctx, `update commutes set status = 'declined', decline_reason = 'no_answer', responded_at = $1
		where status = 'awaiting_driver' and respond_by <= $1`, a.now())
	return tag.RowsAffected(), err
}

type commuteAnswer struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// RespondToCommute accepts or declines commute [commuteID] as driver
// [driverID]. Pass "" for the driver only from the dev simulator, which acts
// as whoever was asked.
func (a *App) RespondToCommute(ctx context.Context, driverID, commuteID, response string) (commuteAnswer, error) {
	if _, err := a.expireUnanswered(ctx); err != nil {
		return commuteAnswer{}, err
	}
	status := "declined"
	if response == "accept" {
		status = "active"
	}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var owner, current string
		var reason *string
		err := tx.QueryRow(ctx, "select driver_id, status, decline_reason from commutes where id = $1 for update", commuteID).
			Scan(&owner, &current, &reason)
		if noRows(err) || (err == nil && driverID != "" && owner != driverID) {
			return apiErr(404, "request_not_found", "That request is not for you")
		}
		if err != nil {
			return err
		}
		if current == "declined" && str(reason) == "no_answer" {
			return apiErr(410, "request_expired", "This request ran out of time. The rider has been asked to pick another driver.")
		}
		if current != "awaiting_driver" {
			return apiErr(409, "already_answered", "This request has already been answered")
		}
		var declineReason *string
		if response == "decline" {
			r := "declined"
			declineReason = &r
		}
		_, err = tx.Exec(ctx, "update commutes set status = $2, responded_at = $3, decline_reason = $4 where id = $1",
			commuteID, status, a.now(), declineReason)
		return err
	})
	if err != nil {
		return commuteAnswer{}, err
	}
	// The first rides exist before the rider next opens the app.
	if status == "active" {
		if _, err := a.CreateRides(ctx, commuteID); err != nil {
			return commuteAnswer{}, err
		}
	}
	return commuteAnswer{commuteID, status}, nil
}

type pendingRequest struct {
	ID           string       `json:"id"`
	DriverID     string       `json:"driverId"`
	RiderName    string       `json:"riderName"`
	Pickup       domain.Place `json:"pickup"`
	Drop         domain.Place `json:"drop"`
	Weekdays     []int        `json:"weekdays"`
	PickupTime   int          `json:"pickupTime"`
	ReturnTrip   bool         `json:"returnTrip"`
	ReturnTime   int          `json:"returnTime"`
	StartDate    string       `json:"startDate"`
	RideType     string       `json:"rideType"`
	Fare         int          `json:"fare"`
	UsualPayment string       `json:"usualPayment"`
	RespondBy    string       `json:"respondBy"`
}

// pendingRequests is the commutes waiting for an answer, oldest first.
// Every driver's when [driverID] is "" (simulator).
func (a *App) pendingRequests(ctx context.Context, driverID string) ([]pendingRequest, error) {
	if _, err := a.expireUnanswered(ctx); err != nil {
		return nil, err
	}
	var filter *string
	if driverID != "" {
		filter = &driverID
	}
	rows, err := a.DB.Query(ctx, `select c.id, c.driver_id, c.pickup, c.drop_off, c.weekdays, c.pickup_time, c.return_trip, c.return_time,
		  to_char(c.start_date, 'YYYY-MM-DD'), c.fare, c.usual_payment, c.respond_by, c.ride_type, r.name
		from commutes c join riders r on r.id = c.rider_id
		where c.status = 'awaiting_driver' and ($1::text is null or c.driver_id = $1)
		order by c.created_at`, filter)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pendingRequest, error) {
		var p pendingRequest
		var respondBy time.Time
		var name *string
		err := row.Scan(&p.ID, &p.DriverID, &p.Pickup, &p.Drop, &p.Weekdays, &p.PickupTime, &p.ReturnTrip, &p.ReturnTime,
			&p.StartDate, &p.Fare, &p.UsualPayment, &respondBy, &p.RideType, &name)
		p.RespondBy = iso(respondBy)
		// Riders have no name until R5; the driver sees a neutral label.
		p.RiderName = "New rider"
		if name != nil {
			p.RiderName = *name
		}
		return p, err
	})
	return orEmpty(out), err
}

func (a *App) driverRequestRoutes(handle func(string, handler)) {
	handle("GET /v1/driver/requests", func(w http.ResponseWriter, r *http.Request) error {
		driverID, err := a.requireDriver(r)
		if err != nil {
			return err
		}
		reqs, err := a.pendingRequests(r.Context(), driverID)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"requests": reqs})
		return nil
	})
	for _, response := range []string{"accept", "decline"} {
		handle("POST /v1/driver/commutes/{id}/"+response, func(w http.ResponseWriter, r *http.Request) error {
			driverID, err := a.requireDriver(r)
			if err != nil {
				return err
			}
			id, err := pathID(r)
			if err != nil {
				return err
			}
			out, err := a.RespondToCommute(r.Context(), driverID, id, response)
			if err != nil {
				return err
			}
			writeJSON(w, 200, out)
			return nil
		})
	}
}
