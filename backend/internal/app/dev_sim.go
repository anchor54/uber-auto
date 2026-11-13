package app

import (
	"context"
	"net/http"
	"time"

	"commute-backend/internal/domain"
)

// Dev-only driver simulator, registered only when ENABLE_DRIVER_SIM=true
// (never in production: ConfigFromEnv refuses it). It acts as whichever
// driver was asked, through the same service functions as the driver
// endpoints, so what it exercises is what the driver app will do.
//
//	GET  /dev/sim/requests                 commutes waiting for a driver
//	POST /dev/sim/commutes/{id}/accept     the driver accepts
//	POST /dev/sim/commutes/{id}/decline    the driver declines
//	POST /dev/sim/commutes/{id}/expire     the driver never answers
//
//	GET  /dev/sim/rides?riderPhone=        open rides, soonest first
//	POST /dev/sim/rides/{id}/step {move}   depart | arrive | start | end | no_show | contact
//	POST /dev/sim/rides/{id}/play {speed}  plays the whole ride in the background
//	POST /dev/sim/clock {now}              moves the server clock (API process only)
//	POST /dev/sim/jobs                     runs the worker jobs once, now
func (a *App) devSimRoutes(handle func(string, handler)) {
	handle("GET /dev/sim/requests", func(w http.ResponseWriter, r *http.Request) error {
		reqs, err := a.pendingRequests(r.Context(), "")
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"requests": reqs})
		return nil
	})

	for _, response := range []string{"accept", "decline"} {
		handle("POST /dev/sim/commutes/{id}/"+response, func(w http.ResponseWriter, r *http.Request) error {
			id, err := pathID(r)
			if err != nil {
				return err
			}
			out, err := a.RespondToCommute(r.Context(), "", id, response)
			if err != nil {
				return err
			}
			writeJSON(w, 200, out)
			return nil
		})
	}

	handle("POST /dev/sim/commutes/{id}/expire", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if _, err := a.DB.Exec(ctx, `update commutes set respond_by = $2 where id = $1 and status = 'awaiting_driver'`, id, a.now()); err != nil {
			return err
		}
		if _, err := a.expireUnanswered(ctx); err != nil {
			return err
		}
		var status *string
		if err := a.DB.QueryRow(ctx, "select status from commutes where id = $1", id).Scan(&status); err != nil && !noRows(err) {
			return err
		}
		writeJSON(w, 200, map[string]any{"id": id, "status": status})
		return nil
	})

	handle("GET /dev/sim/rides", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		var phone *string
		if r.URL.Query().Has("riderPhone") {
			p := r.URL.Query().Get("riderPhone")
			phone = &p
		}
		rows, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r join riders p on p.id = r.rider_id
			where r.state in ('scheduled', 'on_the_way', 'arrived', 'in_ride') and ($1::text is null or p.phone = $1)
			order by r.pickup_at limit 20`, phone)
		if err != nil {
			return err
		}
		out, err := a.riderRidesJSON(ctx, rows)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"rides": out})
		return nil
	})

	handle("POST /dev/sim/rides/{id}/step", func(w http.ResponseWriter, r *http.Request) error {
		var body struct{ Move string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.OneOf(body.Move, SimMoves, "move")
		if err := c.Err(); err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		ride, err := a.SimStep(r.Context(), id, body.Move)
		if err != nil {
			return err
		}
		out, err := a.riderRideJSON(r.Context(), ride)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("POST /dev/sim/rides/{id}/play", func(w http.ResponseWriter, r *http.Request) error {
		body := struct{ Speed *float64 }{}
		if err := decode(r, &body); err != nil {
			return err
		}
		speed := 30.0
		if body.Speed != nil {
			speed = *body.Speed
		}
		var c domain.Check
		c.Range(speed, 1, 600, "speed")
		if err := c.Err(); err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		// Plays in the background, past the end of this request.
		go func() {
			if err := a.SimPlay(context.Background(), id, PlayOptions{Speed: speed}); err != nil {
				a.log().Warn("[sim] ride stopped", "ride", id, "err", err)
			}
		}()
		writeJSON(w, 202, map[string]any{"playing": true, "speed": speed})
		return nil
	})

	handle("POST /dev/sim/clock", func(w http.ResponseWriter, r *http.Request) error {
		if a.SetClock == nil {
			return apiErr(409, "clock_fixed", "This server cannot move its clock")
		}
		var body struct{ Now string }
		if err := decode(r, &body); err != nil {
			return err
		}
		now, err := time.Parse(time.RFC3339Nano, body.Now)
		if err != nil {
			var c domain.Check
			c.Fail("Invalid datetime", "now")
			return c.Err()
		}
		a.SetClock(now)
		writeJSON(w, 200, map[string]string{"now": iso(a.now())})
		return nil
	})

	handle("POST /dev/sim/jobs", func(w http.ResponseWriter, r *http.Request) error {
		did, err := a.RunJobs(r.Context(), LogPushSender(a.log()))
		if err != nil {
			return err
		}
		writeJSON(w, 200, did)
		return nil
	})
}
