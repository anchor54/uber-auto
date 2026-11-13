package app

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"commute-backend/internal/db"
	"commute-backend/internal/domain"
)

type driverRow struct {
	ID              string
	Name            string
	RideType        string
	Vehicle         string
	Plate           string
	Rating          float64
	UPIID           string
	Gender          string
	HasAC           bool
	BaseLat         float64
	BaseLng         float64
	ServiceRadiusKm float64
	WorkStart       int
	WorkEnd         int
}

const driverColumns = `id, name, ride_type, vehicle, plate, rating, upi_id, gender, has_ac, base_lat, base_lng,
	service_radius_km, work_start, work_end`

func scanDriver(row pgx.Row) (driverRow, error) {
	var d driverRow
	err := row.Scan(&d.ID, &d.Name, &d.RideType, &d.Vehicle, &d.Plate, &d.Rating, &d.UPIID, &d.Gender, &d.HasAC,
		&d.BaseLat, &d.BaseLng, &d.ServiceRadiusKm, &d.WorkStart, &d.WorkEnd)
	return d, err
}

type candidate struct {
	driver             driverRow
	distanceToPickupKm float64
}

// ResolvedCommute is the commute as the server will run it: pickup time
// worked out, start date set. Stored as JSON with a match proposal.
type ResolvedCommute struct {
	domain.CommuteRequest
	PickupTime int                 `json:"pickupTime"`
	StartDate  string              `json:"startDate"`
	Trip       domain.TripEstimate `json:"trip"`
}

func (a *App) resolveRequest(ctx context.Context, req domain.CommuteRequest) (ResolvedCommute, error) {
	trip, err := a.maps().EstimateTrip(ctx, req.Pickup.Point(), req.Drop.Point())
	if err != nil {
		return ResolvedCommute{}, err
	}
	pickupTime := req.Time
	if req.Timing != "pickup_at" {
		pickupTime = domain.PickupForArrival(req.Time, trip.Minutes)
	}
	today := domain.LocalDateString(a.now(), a.Config.UTCOffsetMinutes)
	return ResolvedCommute{CommuteRequest: req, PickupTime: pickupTime, StartDate: domain.FirstRideDate(today, req.Weekdays), Trip: trip}, nil
}

// canInsert tells whether [d] can take every leg of [c] on top of what they
// already drive. The shared timeline check used by recurring matching.
func canInsert(ctx context.Context, q db.Q, d driverRow, c ResolvedCommute) (bool, error) {
	legs := domain.WeeklyLegs(domain.WeeklyCommute{Weekdays: c.Weekdays, PickupTime: c.PickupTime, ReturnTrip: c.ReturnTrip,
		ReturnTime: c.ReturnTime, TripMinutes: c.Trip.Minutes})
	for _, l := range legs {
		if l.Start < d.WorkStart || l.End > d.WorkEnd {
			return false, nil
		}
	}
	rows, err := q.Query(ctx, `select weekdays, pickup_time, return_trip, return_time, trip_minutes from commutes
		where driver_id = $1 and status in ('awaiting_driver', 'active')`, d.ID)
	if err != nil {
		return false, err
	}
	var existing []domain.WeeklyCommute
	for rows.Next() {
		var e domain.WeeklyCommute
		if err := rows.Scan(&e.Weekdays, &e.PickupTime, &e.ReturnTrip, &e.ReturnTime, &e.TripMinutes); err != nil {
			rows.Close()
			return false, err
		}
		existing = append(existing, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, e := range existing {
		theirs := domain.WeeklyLegs(e)
		for _, l := range legs {
			for _, m := range theirs {
				if domain.LegsClash(l, m) {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

// findCandidates is the drivers who can serve the commute, best first
// (nearest base, then rating). A driver who turned this rider down (or
// didn't answer) in the last 30 days, or whom the rider asked never to be
// matched with again, isn't offered.
func (a *App) findCandidates(ctx context.Context, c ResolvedCommute, riderID string, limit int) ([]candidate, error) {
	rows, err := a.DB.Query(ctx, `select `+driverColumns+`
		from drivers d where city = $1 and active and ride_type = $2
		  and not exists (
		    select 1 from commutes x where x.driver_id = d.id and x.rider_id = $3 and x.status = 'declined'
		      and x.responded_at > $4::timestamptz - interval '30 days')
		  and not exists (
		    select 1 from ride_ratings rr join rides rd on rd.id = rr.ride_id
		    where rd.driver_id = d.id and rd.rider_id = $3 and rr.dont_match)`,
		a.Config.City, c.RideType, riderID, a.now())
	if err != nil {
		return nil, err
	}
	drivers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (driverRow, error) { return scanDriver(row) })
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, d := range drivers {
		if slices.Contains(c.Preferences, "woman_driver") && d.Gender != "female" {
			continue
		}
		if slices.Contains(c.Preferences, "ac_on") && !d.HasAC {
			continue
		}
		dist := domain.HaversineKm(d.BaseLat, d.BaseLng, c.Pickup.Lat, c.Pickup.Lng)
		if dist > d.ServiceRadiusKm {
			continue
		}
		ok, err := canInsert(ctx, a.DB, d, c)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, candidate{d, dist})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].distanceToPickupKm != out[j].distanceToPickupKm {
			return out[i].distanceToPickupKm < out[j].distanceToPickupKm
		}
		return out[i].driver.Rating > out[j].driver.Rating
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func area(p domain.Place) string {
	if p.Detail == "" {
		return p.Title
	}
	parts := strings.Split(p.Detail, ", ")
	return parts[len(parts)-1]
}

type driverJSON struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Vehicle string  `json:"vehicle"`
	Plate   string  `json:"plate"`
	Rating  float64 `json:"rating"`
	UPIID   string  `json:"upiId"`
}

func firstName(name string) string {
	first, _, _ := strings.Cut(name, " ")
	return first
}

func validRequest(r *http.Request) (domain.CommuteRequest, error) {
	var body struct{ Request *domain.CommuteRequest }
	if err := decode(r, &body); err != nil {
		return domain.CommuteRequest{}, err
	}
	if err := domain.ValidateCommuteRequest(body.Request, "request"); err != nil {
		return domain.CommuteRequest{}, err
	}
	return *body.Request, nil
}

func (a *App) commuteRoutes(handle func(string, handler)) {
	// R1.4: propose drivers and hold them for the rider while they confirm.
	handle("POST /v1/commutes/proposals", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		req, err := validRequest(r)
		if err != nil {
			return err
		}
		c, err := a.resolveRequest(ctx, req)
		if err != nil {
			return err
		}
		options, err := a.bookingOptions(ctx, domain.FlowFor(c.Repeat), c.Pickup, c.Drop, c.PickupTime)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(options.Options, func(o RideTypeOption) bool { return o.Type == c.RideType })
		if i < 0 {
			return apiErr(422, "ride_type_not_allowed", "That ride type is not offered for this booking in your city")
		}
		fare := options.Options[i].Fare
		candidates, err := a.findCandidates(ctx, c, riderID, 3)
		if err != nil {
			return err
		}
		expiresAt := a.now().Add(time.Duration(a.Config.ProposalHoldMinutes) * time.Minute)
		proposals := []map[string]any{}
		for rank, cand := range candidates {
			d := cand.driver
			var id string
			err := a.DB.QueryRow(ctx, `insert into match_proposals (rider_id, driver_id, request, fare, trip_minutes, distance_km, rank, expires_at)
				values ($1, $2, $3, $4, $5, $6, $7, $8) returning id`,
				riderID, d.ID, c, fare, c.Trip.Minutes, c.Trip.DistanceKm, rank, expiresAt).Scan(&id)
			if err != nil {
				return err
			}
			reasons := []string{"Same driver on every ride", "Has a free slot at that time on your days",
				"If " + firstName(d.Name) + " can't make it, a backup driver is confirmed the night before"}
			if c.ReturnTrip {
				reasons[0] = "Same driver every day, both directions"
			}
			if rank == 0 {
				reasons[1] = "Already drives near " + area(c.Pickup) + " at this time"
			}
			proposals = append(proposals, map[string]any{
				"id":        id,
				"driver":    driverJSON{d.ID, d.Name, d.Vehicle, d.Plate, d.Rating, d.UPIID},
				"fare":      fare,
				"trip":      c.Trip,
				"expiresAt": iso(expiresAt),
				"reasons":   reasons,
			})
		}
		writeJSON(w, 200, map[string]any{"pickupTime": c.PickupTime, "startDate": c.StartDate, "proposals": proposals})
		return nil
	})

	// R1.5: confirm a held proposal. Nothing is paid; riders pay drivers directly.
	handle("POST /v1/commutes", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		var body struct{ ProposalID, UsualPayment string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var check domain.Check
		check.If(!uuidRe.MatchString(body.ProposalID), "Invalid uuid", "proposalId")
		check.OneOf(body.UsualPayment, domain.PaymentMethods, "usualPayment")
		if err := check.Err(); err != nil {
			return err
		}
		var commuteID string
		var c ResolvedCommute
		auto := a.Config.AutoAcceptCommutes
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			var proposalRider, driverID string
			var fare int
			var expires time.Time
			err := tx.QueryRow(ctx, "select rider_id, driver_id, request, fare, expires_at from match_proposals where id = $1",
				body.ProposalID).Scan(&proposalRider, &driverID, &c, &fare, &expires)
			if noRows(err) || (err == nil && proposalRider != riderID) {
				return apiErr(404, "proposal_not_found", "That match is no longer available")
			}
			if err != nil {
				return err
			}
			if !expires.After(a.now()) {
				return apiErr(410, "proposal_expired", "The driver hold ran out. Search again.")
			}
			// Lock the driver row so two riders can't take the same slot at once.
			d, err := scanDriver(tx.QueryRow(ctx, "select "+driverColumns+" from drivers where id = $1 and active for update", driverID))
			free := false
			if err == nil {
				if free, err = canInsert(ctx, tx, d, c); err != nil {
					return err
				}
			} else if !noRows(err) {
				return err
			}
			if !free {
				return apiErr(409, "driver_unavailable", "That driver just filled this slot. Pick another driver.")
			}
			status := "awaiting_driver"
			var respondBy, respondedAt *time.Time
			if auto {
				status = "active"
				now := a.now()
				respondedAt = &now
			} else {
				t := a.respondByFor(c.StartDate, c.PickupTime)
				respondBy = &t
			}
			var routeOut *string
			if c.Trip.Polyline != "" {
				routeOut = &c.Trip.Polyline
			}
			err = tx.QueryRow(ctx, `insert into commutes (rider_id, driver_id, city, pickup, drop_off, repeat, weekdays, timing, time_min,
				pickup_time, return_trip, return_time, start_date, ride_type, preferences, fare, trip_minutes, distance_km,
				usual_payment, status, respond_by, responded_at, route_out)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::text::date, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
				returning id`,
				riderID, d.ID, a.Config.City, c.Pickup, c.Drop, c.Repeat, c.Weekdays, c.Timing, c.Time,
				c.PickupTime, c.ReturnTrip, c.ReturnTime, c.StartDate, c.RideType, orEmpty(c.Preferences), fare,
				c.Trip.Minutes, c.Trip.DistanceKm, body.UsualPayment, status, respondBy, respondedAt, routeOut).Scan(&commuteID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "update riders set usual_payment = $2 where id = $1", riderID, body.UsualPayment); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "delete from match_proposals where rider_id = $1", riderID)
			return err
		})
		if err != nil {
			return err
		}
		// The way back can take other roads: ask the maps provider once, outside
		// the transaction. Without it the return ride just has no route to draw.
		if c.ReturnTrip && c.Trip.Polyline != "" {
			if back, err := a.maps().EstimateTrip(ctx, c.Drop.Point(), c.Pickup.Point()); err == nil && back.Polyline != "" {
				if _, err := a.DB.Exec(ctx, "update commutes set route_back = $2 where id = $1", commuteID, back.Polyline); err != nil {
					return err
				}
			}
		}
		if auto {
			if _, err := a.CreateRides(ctx, commuteID); err != nil {
				return err
			}
		}
		out, err := a.loadCommute(ctx, commuteID)
		if err != nil {
			return err
		}
		writeJSON(w, 201, out)
		return nil
	})

	// No driver yet: keep the request and tell the rider when one is free.
	handle("POST /v1/waitlist", func(w http.ResponseWriter, r *http.Request) error {
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		req, err := validRequest(r)
		if err != nil {
			return err
		}
		var id string
		if err := a.DB.QueryRow(r.Context(), "insert into waitlist (rider_id, request) values ($1, $2) returning id", riderID, req).Scan(&id); err != nil {
			return err
		}
		writeJSON(w, 201, map[string]string{"id": id})
		return nil
	})

	// R1.6 / R1.6b: what Home shows.
	handle("GET /v1/home", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		if _, err := a.expireUnanswered(ctx); err != nil {
			return err
		}
		var rider struct {
			ID           string  `json:"id"`
			Phone        string  `json:"phone"`
			Name         *string `json:"name"`
			UsualPayment string  `json:"usualPayment"`
		}
		if err := a.DB.QueryRow(ctx, "select id, phone, name, usual_payment from riders where id = $1", riderID).
			Scan(&rider.ID, &rider.Phone, &rider.Name, &rider.UsualPayment); err != nil {
			return err
		}
		var commute any
		var currentID string
		err = a.DB.QueryRow(ctx, `select id from commutes where rider_id = $1 and status <> 'cancelled' order by created_at desc limit 1`,
			riderID).Scan(&currentID)
		if err == nil {
			if commute, err = a.loadCommute(ctx, currentID); err != nil {
				return err
			}
		} else if !noRows(err) {
			return err
		}
		// R2: the next pickup comes from the ride records, not the commute.
		next, err := a.nextRideFor(ctx, riderID)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"rider": rider, "commute": commute, "nextRide": next, "recentRides": []any{}})
		return nil
	})
}

type commuteJSON struct {
	ID            string        `json:"id"`
	Status        string        `json:"status"`
	Pickup        domain.Place  `json:"pickup"`
	Drop          domain.Place  `json:"drop"`
	Repeat        string        `json:"repeat"`
	Weekdays      []int         `json:"weekdays"`
	Timing        string        `json:"timing"`
	Time          int           `json:"time"`
	PickupTime    int           `json:"pickupTime"`
	ReturnTrip    bool          `json:"returnTrip"`
	ReturnTime    int           `json:"returnTime"`
	StartDate     string        `json:"startDate"`
	RideType      string        `json:"rideType"`
	Preferences   []string      `json:"preferences"`
	Fare          int           `json:"fare"`
	UsualPayment  string        `json:"usualPayment"`
	Trip          commuteTrip   `json:"trip"`
	RespondBy     *string       `json:"respondBy"`
	DeclineReason *string       `json:"declineReason"`
	Driver        commuteDriver `json:"driver"`
}

type commuteTrip struct {
	Minutes    int     `json:"minutes"`
	DistanceKm float64 `json:"distanceKm"`
}

type commuteDriver struct {
	driverJSON
	// The driver's number is shared once they've accepted the rider.
	Phone *string `json:"phone"`
}

func (a *App) loadCommute(ctx context.Context, id string) (commuteJSON, error) {
	var c commuteJSON
	var respondBy *time.Time
	var phone *string
	err := a.DB.QueryRow(ctx, `select c.id, c.status, c.pickup, c.drop_off, c.repeat, c.weekdays, c.timing, c.time_min, c.pickup_time,
		  c.return_trip, c.return_time, to_char(c.start_date, 'YYYY-MM-DD'), c.ride_type, c.preferences, c.fare, c.usual_payment,
		  c.trip_minutes, c.distance_km, c.respond_by, c.decline_reason,
		  d.id, d.name, d.vehicle, d.plate, d.rating, d.upi_id, d.phone
		from commutes c join drivers d on d.id = c.driver_id where c.id = $1`, id).Scan(
		&c.ID, &c.Status, &c.Pickup, &c.Drop, &c.Repeat, &c.Weekdays, &c.Timing, &c.Time, &c.PickupTime,
		&c.ReturnTrip, &c.ReturnTime, &c.StartDate, &c.RideType, &c.Preferences, &c.Fare, &c.UsualPayment,
		&c.Trip.Minutes, &c.Trip.DistanceKm, &respondBy, &c.DeclineReason,
		&c.Driver.ID, &c.Driver.Name, &c.Driver.Vehicle, &c.Driver.Plate, &c.Driver.Rating, &c.Driver.UPIID, &phone)
	if err != nil {
		return c, err
	}
	c.Weekdays = orEmpty(c.Weekdays)
	c.Preferences = orEmpty(c.Preferences)
	c.RespondBy = isoPtr(respondBy)
	if c.Status == "active" {
		c.Driver.Phone = phone
	}
	return c, nil
}
