package offers

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/scheduling"
)

// OfferSlot is one weekday and direction as a driver sees it.
type OfferSlot struct {
	Weekday           int     `json:"weekday"`
	Day               string  `json:"day"`
	Direction         string  `json:"direction"`
	Timing            string  `json:"timing"`
	ArriveByTime      *string `json:"arrive_by_time"`
	LocalPickupTime   string  `json:"local_pickup_time"`
	EstimatedDropTime string  `json:"estimated_drop_time"`
}

// OfferRider is the rider as a driver sees them before a pick.
type OfferRider struct {
	FirstName string  `json:"first_name"`
	Name      string  `json:"name"` // first name and initial: "Priya S."
	Rating    float64 `json:"rating"`
}

// OfferView is an offer on D2.1 and D2.2.
type OfferView struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	SeriesID uuid.UUID `json:"series_id"`
	Round    int       `json:"round"`
	// open, closing (past expires_at, being picked) or closed.
	Status        string      `json:"status"`
	Outcome       *string     `json:"outcome"`
	OpenedAt      time.Time   `json:"opened_at"`
	ExpiresAt     time.Time   `json:"expires_at"`
	SecondsLeft   int         `json:"seconds_left"`
	CanRespond    bool        `json:"can_respond"`
	Interested    bool        `json:"interested"`
	InterestedAt  *time.Time  `json:"interested_at"`
	Dismissed     bool        `json:"dismissed"`
	Result        *string     `json:"result"`
	Rider         OfferRider  `json:"rider"`
	VehicleType   string      `json:"vehicle_type"`
	Preferences   []string    `json:"preferences"`
	Pickup        Place       `json:"pickup"`
	Drop          Place       `json:"drop"`
	StartDate     string      `json:"start_date"`
	DistanceKm    float64     `json:"distance_km"`
	TravelMinutes int         `json:"travel_minutes"`
	FarePerRide   int         `json:"fare_per_ride"`
	RidesPerMonth int         `json:"rides_per_month"`
	FarePerMonth  int         `json:"fare_per_month"`
	UsualPayment  string      `json:"usual_payment"`
	Slots         []OfferSlot `json:"slots"`
	// One-ride offers (kind "ride") only: the ride offered.
	RideID          *uuid.UUID `json:"ride_id,omitempty"`
	LocalDate       *string    `json:"local_date,omitempty"`
	PickupAt        *time.Time `json:"pickup_at,omitempty"`
	LocalPickupTime *string    `json:"local_pickup_time,omitempty"`
}

// OfferResult is a result card for a request the driver was interested in,
// kept 24 hours.
type OfferResult struct {
	OfferID  uuid.UUID `json:"offer_id"`
	SeriesID uuid.UUID `json:"series_id"`
	// picked, went_to_another_driver or rider_withdrew.
	Result   string    `json:"result"`
	Message  string    `json:"message"`
	ClosedAt time.Time `json:"closed_at"`
}

// OffersList is offers.list's answer.
type OffersList struct {
	Offers  []OfferView   `json:"offers"`
	Results []OfferResult `json:"results"`
}

func (s *Service) requireLiveDriver(ctx context.Context, driverID uuid.UUID, now time.Time) (driverRow, error) {
	d, err := readDriver(ctx, s.DB, driverID)
	if noRows(err) {
		return d, errf(403, "not_a_driver", "This needs a driver account.")
	}
	if err != nil {
		return d, err
	}
	if !d.live(s.today(now)) {
		return d, errf(403, "not_eligible", "Requests show while you're live with an active plan.")
	}
	return d, nil
}

// ListOffers is offers.list: open offers this driver received and hasn't
// dismissed, newest first, with their interest.
func (s *Service) ListOffers(ctx context.Context, driverID uuid.UUID) (OffersList, error) {
	now := s.Clock.Now()
	out := OffersList{Offers: []OfferView{}, Results: []OfferResult{}}
	if _, err := s.requireLiveDriver(ctx, driverID, now); err != nil {
		return out, err
	}
	rows, err := s.DB.Query(ctx, `select o.id from offers o join offer_recipients r on r.offer_id = o.id
		where r.driver_id = $1 and o.status = 'open' and o.expires_at > $2 and r.dismissed_at is null
		order by o.opened_at desc, o.id`, driverID, now)
	if err != nil {
		return out, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		v, err := s.offerView(ctx, s.DB, driverID, id, now)
		if err != nil {
			return out, err
		}
		out.Offers = append(out.Offers, v)
	}
	rows, err = s.DB.Query(ctx, `select o.id, o.series_id, o.outcome, r.result, o.closed_at
		from offers o join offer_recipients r on r.offer_id = o.id
		where r.driver_id = $1 and o.status = 'closed' and o.closed_at > $2
		  and (r.result in ('picked', 'not_picked', 'clashed', 'ineligible')
		       or (o.outcome = 'cancelled' and r.interested_at is not null))
		order by o.closed_at desc`, driverID, now.Add(-24*time.Hour))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r OfferResult
		var outcome, result *string
		if err := rows.Scan(&r.OfferID, &r.SeriesID, &outcome, &result, &r.ClosedAt); err != nil {
			rows.Close()
			return out, err
		}
		switch {
		case result != nil && *result == "picked":
			r.Result, r.Message = "picked", "You got it."
		case outcome != nil && *outcome == "cancelled":
			r.Result, r.Message = "rider_withdrew", "The rider withdrew this request."
		default:
			r.Result, r.Message = "went_to_another_driver", "Went to another driver"
		}
		out.Results = append(out.Results, r)
	}
	rows.Close()
	return out, rows.Err()
}

// GetOffer is offer.get: the detail for D2.2. 404 unless the driver received it.
func (s *Service) GetOffer(ctx context.Context, driverID, offerID uuid.UUID) (OfferView, error) {
	return s.offerView(ctx, s.DB, driverID, offerID, s.Clock.Now())
}

func (s *Service) offerView(ctx context.Context, q querier, driverID, offerID uuid.UUID, now time.Time) (OfferView, error) {
	var v OfferView
	var seriesID, rideID *uuid.UUID
	var interestedAt, dismissedAt *time.Time
	err := q.QueryRow(ctx, `select o.id, o.kind, o.series_id, o.ride_id, o.round, o.status, o.outcome, o.opened_at, o.expires_at,
			r.interested_at, r.dismissed_at, r.result
		from offers o join offer_recipients r on r.offer_id = o.id and r.driver_id = $2
		where o.id = $1`, offerID, driverID).
		Scan(&v.ID, &v.Kind, &seriesID, &rideID, &v.Round, &v.Status, &v.Outcome, &v.OpenedAt, &v.ExpiresAt, &interestedAt, &dismissedAt, &v.Result)
	if noRows(err) {
		return v, errNotFound("offer")
	}
	if err != nil {
		return v, err
	}
	if v.Status == "open" && !v.ExpiresAt.After(now) {
		v.Status = "closing"
	}
	v.CanRespond = v.Status == "open"
	if v.CanRespond {
		v.SecondsLeft = int(v.ExpiresAt.Sub(now).Seconds())
	}
	v.Interested, v.InterestedAt, v.Dismissed = interestedAt != nil, interestedAt, dismissedAt != nil
	if seriesID == nil {
		return v, nil
	}
	v.SeriesID = *seriesID
	se, err := readSeries(ctx, q, *seriesID)
	if err != nil {
		return v, err
	}
	var riderName string
	if err := q.QueryRow(ctx, "select name, rating::float8 from riders where user_id = $1", se.RiderID).Scan(&riderName, &v.Rider.Rating); err != nil && !noRows(err) {
		return v, err
	}
	v.Rider.FirstName, v.Rider.Name = label(FirstName(riderName), "Rider"), nameAndInitial(riderName)
	v.VehicleType, v.Preferences, v.Pickup, v.Drop = se.VehicleType, se.Preferences, se.Pickup, se.Drop
	if v.Preferences == nil {
		v.Preferences = []string{}
	}
	v.StartDate, v.DistanceKm, v.TravelMinutes = scheduling.FormatDate(se.StartDate), se.DistanceKm, se.TravelMinutes
	v.FarePerRide, v.UsualPayment = se.SuggestedFare, se.UsualPayment
	slots, err := readSlots(ctx, q, se.ID)
	if err != nil {
		return v, err
	}
	v.Slots = []OfferSlot{}
	for _, sl := range slots {
		os := OfferSlot{Weekday: sl.Weekday, Day: scheduling.WeekdayName(sl.Weekday), Direction: sl.Direction, Timing: sl.Timing,
			LocalPickupTime: scheduling.FormatClock(sl.Pickup), EstimatedDropTime: scheduling.FormatClock(sl.Pickup + se.TravelMinutes)}
		if sl.ArriveBy != nil {
			os.ArriveByTime = ptr(scheduling.FormatClock(*sl.ArriveBy))
		}
		v.Slots = append(v.Slots, os)
	}
	v.RidesPerMonth = scheduling.RidesPerMonth(len(slots))
	v.FarePerMonth = v.RidesPerMonth * v.FarePerRide
	if rideID != nil {
		r, err := readRide(ctx, q, *rideID)
		if err != nil {
			return v, err
		}
		c, err := s.rideContext(ctx, q, r)
		if err != nil {
			return v, err
		}
		v.RideID, v.PickupAt = rideID, &r.PickupAt
		v.LocalDate = ptr(scheduling.FormatDate(r.LocalDate))
		v.LocalPickupTime = ptr(s.localClock(r.PickupAt, se.TimeZone))
		v.Pickup, v.Drop, v.FarePerRide, v.RidesPerMonth, v.FarePerMonth = c.From, c.To, r.Fare, 1, r.Fare
		v.Slots = []OfferSlot{}
		if r.SlotID != nil {
			for _, sl := range slots {
				if sl.ID == *r.SlotID {
					v.Slots = append(v.Slots, OfferSlot{Weekday: sl.Weekday, Day: scheduling.WeekdayName(sl.Weekday), Direction: sl.Direction,
						Timing: sl.Timing, LocalPickupTime: scheduling.FormatClock(sl.Pickup), EstimatedDropTime: scheduling.FormatClock(sl.Pickup + se.TravelMinutes)})
				}
			}
		}
	}
	return v, nil
}

// interestChange runs a change to the driver's interest while the offer is
// open: the offer row is held (FOR SHARE) so the sweep can't start closing
// it in between. 409 offer_closed once expires_at has passed.
func (s *Service) interestChange(ctx context.Context, driverID, offerID uuid.UUID, needOpen bool,
	f func(tx pgx.Tx, now time.Time, interested bool) error) error {
	now := s.Clock.Now()
	var closed bool
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var status string
		var expires time.Time
		err := tx.QueryRow(ctx, "select status, expires_at from offers where id = $1 for share", offerID).Scan(&status, &expires)
		if noRows(err) {
			return errNotFound("offer")
		}
		if err != nil {
			return err
		}
		var interestedAt *time.Time
		err = tx.QueryRow(ctx, "select interested_at from offer_recipients where offer_id = $1 and driver_id = $2 for update",
			offerID, driverID).Scan(&interestedAt)
		if noRows(err) {
			return errNotFound("offer")
		}
		if err != nil {
			return err
		}
		if needOpen && (status != "open" || !expires.After(now)) {
			closed = true
			return nil
		}
		return f(tx, now, interestedAt != nil)
	})
	if err != nil {
		return err
	}
	if closed {
		v, err := s.offerView(ctx, s.DB, driverID, offerID, now)
		if err != nil {
			return err
		}
		return &Error{Status: 409, Code: "offer_closed", Message: "This request has closed.", State: v}
	}
	return nil
}

// MarkInterested is offer.mark_interested.
func (s *Service) MarkInterested(ctx context.Context, driverID, offerID uuid.UUID) error {
	now := s.Clock.Now()
	if _, err := s.requireLiveDriver(ctx, driverID, now); err != nil {
		return err
	}
	return s.interestChange(ctx, driverID, offerID, true, func(tx pgx.Tx, now time.Time, interested bool) error {
		if interested {
			return nil
		}
		if _, err := tx.Exec(ctx, `update offer_recipients set interested_at = $3, withdrawn_at = null, dismissed_at = null, result = null
			where offer_id = $1 and driver_id = $2`, offerID, driverID, now); err != nil {
			return err
		}
		return event(ctx, tx, now, &driverID, "offer", offerID, "offer.interested", map[string]any{"driver_id": driverID})
	})
}

// WithdrawInterest is offer.withdraw_interest, allowed until the window closes.
func (s *Service) WithdrawInterest(ctx context.Context, driverID, offerID uuid.UUID) error {
	return s.interestChange(ctx, driverID, offerID, true, func(tx pgx.Tx, now time.Time, interested bool) error {
		return withdraw(ctx, tx, driverID, offerID, now, interested)
	})
}

func withdraw(ctx context.Context, tx pgx.Tx, driverID, offerID uuid.UUID, now time.Time, interested bool) error {
	if !interested {
		return nil
	}
	if _, err := tx.Exec(ctx, `update offer_recipients set interested_at = null, withdrawn_at = $3, result = 'withdrew'
		where offer_id = $1 and driver_id = $2`, offerID, driverID, now); err != nil {
		return err
	}
	return event(ctx, tx, now, &driverID, "offer", offerID, "offer.withdrawn", map[string]any{"driver_id": driverID})
}

// Dismiss is offer.dismiss ("Not for me"): the request stops showing for
// this driver, in this and later rounds. Withdraws interest if the offer is
// still open.
func (s *Service) Dismiss(ctx context.Context, driverID, offerID uuid.UUID) error {
	return s.interestChange(ctx, driverID, offerID, false, func(tx pgx.Tx, now time.Time, interested bool) error {
		var status string
		var expires time.Time
		if err := tx.QueryRow(ctx, "select status, expires_at from offers where id = $1", offerID).Scan(&status, &expires); err != nil {
			return err
		}
		if interested && status == "open" && expires.After(now) {
			if err := withdraw(ctx, tx, driverID, offerID, now, true); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "update offer_recipients set dismissed_at = coalesce(dismissed_at, $3) where offer_id = $1 and driver_id = $2",
			offerID, driverID, now); err != nil {
			return err
		}
		return event(ctx, tx, now, &driverID, "offer", offerID, "offer.dismissed", map[string]any{"driver_id": driverID})
	})
}

// ---------------------------------------------------------------- the week

// WeekRide is a ride in the driver's week (D2.3).
type WeekRide struct {
	ID              uuid.UUID  `json:"id"`
	SeriesID        *uuid.UUID `json:"series_id"`
	SlotID          *uuid.UUID `json:"slot_id"`
	LocalDate       string     `json:"local_date"`
	Weekday         int        `json:"weekday"`
	Direction       string     `json:"direction"`
	PickupAt        time.Time  `json:"pickup_at"`
	DropAt          time.Time  `json:"drop_at"`
	BusyUntil       time.Time  `json:"busy_until"`
	LocalPickupTime string     `json:"local_pickup_time"`
	Status          string     `json:"status"`
	Fare            int        `json:"fare"`
	RiderID         uuid.UUID  `json:"rider_id"`
	RiderName       string     `json:"rider_name"`
	Pickup          *Place     `json:"pickup"`
	Drop            *Place     `json:"drop"`
	// Created in the last 24 hours (a new rider's rides after a win).
	New bool `json:"new"`
}

// Week is rides.week's answer.
type Week struct {
	WeekStart string     `json:"week_start"`
	WeekEnd   string     `json:"week_end"`
	Rides     []WeekRide `json:"rides"`
}

// Week is rides.week: the driver's rides from [start] (default: this
// week's Monday) for 7 days.
func (s *Service) Week(ctx context.Context, driverID uuid.UUID, start *time.Time) (Week, error) {
	now := s.Clock.Now()
	from := s.today(now)
	if start != nil {
		from = *start
	} else {
		from = scheduling.AddDays(from, 1-scheduling.ISOWeekday(from))
	}
	to := scheduling.AddDays(from, 6)
	w := Week{WeekStart: scheduling.FormatDate(from), WeekEnd: scheduling.FormatDate(to), Rides: []WeekRide{}}
	rows, err := s.DB.Query(ctx, `select r.id, r.series_id, r.slot_id, r.local_date, coalesce(sl.direction, 'outbound'),
			r.pickup_at, r.drop_at, r.busy_until, r.status, r.fare_snapshot, r.rider_id, coalesce(rd.name, ''), r.created_at,
			s.pickup_lat, s.pickup_lng, s.pickup_label, s.drop_lat, s.drop_lng, s.drop_label, coalesce(s.time_zone, '')
		from rides r left join series_slots sl on sl.id = r.slot_id left join series s on s.id = r.series_id
		left join riders rd on rd.user_id = r.rider_id
		where r.driver_id = $1 and r.local_date between $2 and $3 and r.status <> 'cancelled'
		order by r.pickup_at`, driverID, from, to)
	if err != nil {
		return w, err
	}
	defer rows.Close()
	for rows.Next() {
		var r WeekRide
		var date, created time.Time
		var pLat, pLng, dLat, dLng *float64
		var pLabel, dLabel *string
		var tz string
		if err := rows.Scan(&r.ID, &r.SeriesID, &r.SlotID, &date, &r.Direction, &r.PickupAt, &r.DropAt, &r.BusyUntil,
			&r.Status, &r.Fare, &r.RiderID, &r.RiderName, &created, &pLat, &pLng, &pLabel, &dLat, &dLng, &dLabel, &tz); err != nil {
			return w, err
		}
		r.LocalDate, r.Weekday = scheduling.FormatDate(date), scheduling.ISOWeekday(date)
		lp := r.PickupAt.In(s.loc(tz))
		r.LocalPickupTime = scheduling.FormatClock(lp.Hour()*60 + lp.Minute())
		if pLat != nil {
			p, d := &Place{*pLat, *pLng, *pLabel}, &Place{*dLat, *dLng, *dLabel}
			if r.Direction == "return" {
				p, d = d, p
			}
			r.Pickup, r.Drop = p, d
		}
		r.New = now.Sub(created) < 24*time.Hour
		w.Rides = append(w.Rides, r)
	}
	return w, rows.Err()
}
