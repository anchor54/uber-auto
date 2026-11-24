package offers

// The daily ride (R2 + D3): the ride row, how riders and drivers see it,
// and the shared helpers of the ride endpoints and the sweeper's ride tasks.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/lifecycle"
	"commute-backend/internal/scheduling"
)

type rideRow struct {
	ID               uuid.UUID
	SeriesID         *uuid.UUID
	SlotID           *uuid.UUID
	RiderID          uuid.UUID
	DriverID         *uuid.UUID
	PreviousDriverID *uuid.UUID
	LocalDate        time.Time
	PickupAt         time.Time
	DropAt           time.Time
	BusyUntil        time.Time
	Status           string
	ReminderSentAt   *time.Time
	OnTheWayAt       *time.Time
	ArrivedAt        *time.Time
	PickedUpAt       *time.Time
	CompletedAt      *time.Time
	NoShowAt         *time.Time
	FailedAt         *time.Time
	Pin              *string
	LateNoticeAt     *time.Time
	GiveUpReason     *string
	FailReason       *string
	CloseReason      *string
	DriverPaidMark   *string
	DriverPaidAt     *time.Time
	RiderPaidMark    *string
	RiderPaidAt      *time.Time
	RiderStars       *int
	RiderTags        []string
	RiderNote        *string
	RatedAt          *time.Time
	DriverFlags      []string
	FlaggedAt        *time.Time
	Fare             int
	Version          int
}

const rideCols = `id, series_id, slot_id, rider_id, driver_id, previous_driver_id, local_date, pickup_at, drop_at, busy_until,
	status, reminder_sent_at, on_the_way_at, arrived_at, picked_up_at, completed_at, no_show_at, failed_at, pin,
	late_notice_at, give_up_reason, fail_reason, close_reason, driver_paid_mark, driver_paid_at, rider_paid_mark,
	rider_paid_at, rider_stars, rider_tags, rider_note, rated_at, driver_flags, flagged_at, fare_snapshot, version`

func scanRide(row pgx.Row) (rideRow, error) {
	var r rideRow
	err := row.Scan(&r.ID, &r.SeriesID, &r.SlotID, &r.RiderID, &r.DriverID, &r.PreviousDriverID, &r.LocalDate, &r.PickupAt,
		&r.DropAt, &r.BusyUntil, &r.Status, &r.ReminderSentAt, &r.OnTheWayAt, &r.ArrivedAt, &r.PickedUpAt, &r.CompletedAt,
		&r.NoShowAt, &r.FailedAt, &r.Pin, &r.LateNoticeAt, &r.GiveUpReason, &r.FailReason, &r.CloseReason,
		&r.DriverPaidMark, &r.DriverPaidAt, &r.RiderPaidMark, &r.RiderPaidAt, &r.RiderStars, &r.RiderTags, &r.RiderNote,
		&r.RatedAt, &r.DriverFlags, &r.FlaggedAt, &r.Fare, &r.Version)
	return r, err
}

func readRide(ctx context.Context, q querier, id uuid.UUID) (rideRow, error) {
	return scanRide(q.QueryRow(ctx, "select "+rideCols+" from rides where id = $1", id))
}

func lockRide(ctx context.Context, q querier, id uuid.UUID) (rideRow, error) {
	return scanRide(q.QueryRow(ctx, "select "+rideCols+" from rides where id = $1 for update", id))
}

// rideCtx is what a ride's messages and views need besides the ride: its
// series, direction, rider and driver names.
type rideCtx struct {
	Series     seriesRow
	Direction  string
	From, To   Place
	RiderName  string
	RiderPhone string
	Driver     *driverInfo
}

type driverInfo struct {
	ID            uuid.UUID
	Name          string
	Phone         string
	VehicleType   string
	VehicleNumber string
	VehicleDesc   string
	Rating        float64
	UpiID         *string
}

func readDriverInfo(ctx context.Context, q querier, id uuid.UUID) (*driverInfo, error) {
	d := &driverInfo{}
	err := q.QueryRow(ctx, `select d.user_id, d.name, u.phone, d.vehicle_type, d.vehicle_number, d.vehicle_desc, d.rating::float8, d.upi_id
		from drivers d join users u on u.id = d.user_id where d.user_id = $1`, id).
		Scan(&d.ID, &d.Name, &d.Phone, &d.VehicleType, &d.VehicleNumber, &d.VehicleDesc, &d.Rating, &d.UpiID)
	return d, err
}

func (s *Service) rideContext(ctx context.Context, q querier, r rideRow) (rideCtx, error) {
	var c rideCtx
	if r.SeriesID != nil {
		se, err := readSeries(ctx, q, *r.SeriesID)
		if err != nil {
			return c, err
		}
		c.Series = se
	}
	c.Direction = "outbound"
	if r.SlotID != nil {
		if err := q.QueryRow(ctx, "select direction from series_slots where id = $1", *r.SlotID).Scan(&c.Direction); err != nil && !noRows(err) {
			return c, err
		}
	}
	c.From, c.To = c.Series.Pickup, c.Series.Drop
	if c.Direction == "return" {
		c.From, c.To = c.To, c.From
	}
	if err := q.QueryRow(ctx, "select coalesce(rd.name, ''), u.phone from users u left join riders rd on rd.user_id = u.id where u.id = $1",
		r.RiderID).Scan(&c.RiderName, &c.RiderPhone); err != nil && !noRows(err) {
		return c, err
	}
	if r.DriverID != nil {
		d, err := readDriverInfo(ctx, q, *r.DriverID)
		if err != nil {
			return c, err
		}
		c.Driver = d
	}
	return c, nil
}

func (c rideCtx) riderFirst() string { return label(FirstName(c.RiderName), "your rider") }

func (c rideCtx) driverFirst() string {
	if c.Driver == nil {
		return "Your driver"
	}
	return label(FirstName(c.Driver.Name), "Your driver")
}

// localClock is "08:30" in the ride's zone.
func (s *Service) localClock(t time.Time, tz string) string {
	l := t.In(s.loc(tz))
	return scheduling.FormatClock(l.Hour()*60 + l.Minute())
}

// ---------------------------------------------------------------- views

// RidePerson is the other side of a ride.
type RidePerson struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	FirstName string    `json:"first_name"`
	Phone     string    `json:"phone"`
}

// RideDriver is the driver as the rider sees them.
type RideDriver struct {
	RidePerson
	VehicleType   string  `json:"vehicle_type"`
	VehicleNumber string  `json:"vehicle_number"`
	Plate         string  `json:"plate"`
	VehicleDesc   string  `json:"vehicle_desc"`
	Rating        float64 `json:"rating"`
	UpiID         *string `json:"upi_id"`
}

// RideView is a ride as either side sees it. The PIN is only in the
// rider's view, and only once the ride is on the way.
type RideView struct {
	ID              uuid.UUID   `json:"id"`
	SeriesID        *uuid.UUID  `json:"series_id"`
	SlotID          *uuid.UUID  `json:"slot_id"`
	LocalDate       string      `json:"local_date"`
	Weekday         int         `json:"weekday"`
	Day             string      `json:"day"`
	Direction       string      `json:"direction"`
	PickupAt        time.Time   `json:"pickup_at"`
	DropAt          time.Time   `json:"drop_at"`
	BusyUntil       time.Time   `json:"busy_until"`
	LocalPickupTime string      `json:"local_pickup_time"`
	LocalDropTime   string      `json:"local_drop_time"`
	TimeZone        string      `json:"time_zone"`
	Status          string      `json:"status"`
	StateLabel      string      `json:"state_label"`
	Fare            int         `json:"fare"`
	UsualPayment    string      `json:"usual_payment"`
	VehicleType     string      `json:"vehicle_type"`
	TravelMinutes   int         `json:"travel_minutes"`
	DistanceKm      float64     `json:"distance_km"`
	Pickup          Place       `json:"pickup"`
	Drop            Place       `json:"drop"`
	RiderID         uuid.UUID   `json:"rider_id"`
	Rider           *RidePerson `json:"rider"`
	DriverID        *uuid.UUID  `json:"driver_id"`
	Driver          *RideDriver `json:"driver"`
	Version         int         `json:"version"`

	ReminderSentAt *time.Time `json:"reminder_sent_at"`
	OnTheWayAt     *time.Time `json:"on_the_way_at"`
	ArrivedAt      *time.Time `json:"arrived_at"`
	PickedUpAt     *time.Time `json:"picked_up_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	NoShowAt       *time.Time `json:"no_show_at"`
	FailedAt       *time.Time `json:"failed_at"`
	LateNoticeAt   *time.Time `json:"late_notice_at"`
	RunningLate    bool       `json:"running_late"`

	OnMyWayOpensAt time.Time  `json:"on_my_way_opens_at"`
	SetsOffBy      time.Time  `json:"sets_off_by"`
	NoShowOpensAt  *time.Time `json:"no_show_opens_at"`

	Pin *string `json:"pin,omitempty"`

	GiveUpReason   *string  `json:"give_up_reason"`
	FailReason     *string  `json:"fail_reason"`
	CloseReason    *string  `json:"close_reason"`
	DriverPaidMark *string  `json:"driver_paid_mark"`
	RiderPaidMark  *string  `json:"rider_paid_mark"`
	PaymentStatus  string   `json:"payment_status"`
	RiderStars     *int     `json:"rider_stars"`
	RiderTags      []string `json:"rider_tags"`
	RiderNote      *string  `json:"rider_note"`
	DriverFlags    []string `json:"driver_flags"`
	// For a driver: false when the ride was handed to another driver.
	Mine *bool `json:"mine,omitempty"`
}

func (s *Service) rideView(ctx context.Context, q querier, r rideRow, forRider bool) (RideView, error) {
	c, err := s.rideContext(ctx, q, r)
	if err != nil {
		return RideView{}, err
	}
	return s.buildRideView(r, c, forRider), nil
}

func (s *Service) buildRideView(r rideRow, c rideCtx, forRider bool) RideView {
	tz := c.Series.TimeZone
	v := RideView{
		ID: r.ID, SeriesID: r.SeriesID, SlotID: r.SlotID, LocalDate: scheduling.FormatDate(r.LocalDate),
		Weekday: scheduling.ISOWeekday(r.LocalDate), Day: scheduling.WeekdayName(scheduling.ISOWeekday(r.LocalDate)),
		Direction: c.Direction, PickupAt: r.PickupAt, DropAt: r.DropAt, BusyUntil: r.BusyUntil,
		LocalPickupTime: s.localClock(r.PickupAt, tz), LocalDropTime: s.localClock(r.DropAt, tz), TimeZone: label(tz, s.cfg().TimeZone),
		Status: r.Status, Fare: r.Fare, UsualPayment: label(c.Series.UsualPayment, "upi"), VehicleType: c.Series.VehicleType,
		TravelMinutes: c.Series.TravelMinutes, DistanceKm: c.Series.DistanceKm, Pickup: c.From, Drop: c.To,
		RiderID: r.RiderID, DriverID: r.DriverID, Version: r.Version,
		ReminderSentAt: r.ReminderSentAt, OnTheWayAt: r.OnTheWayAt, ArrivedAt: r.ArrivedAt, PickedUpAt: r.PickedUpAt,
		CompletedAt: r.CompletedAt, NoShowAt: r.NoShowAt, FailedAt: r.FailedAt, LateNoticeAt: r.LateNoticeAt,
		RunningLate:    r.LateNoticeAt != nil,
		OnMyWayOpensAt: lifecycle.OnMyWayOpensAt(r.PickupAt), SetsOffBy: lifecycle.CheckAt(r.PickupAt),
		GiveUpReason: r.GiveUpReason, FailReason: r.FailReason, CloseReason: r.CloseReason,
		DriverPaidMark: r.DriverPaidMark, RiderPaidMark: r.RiderPaidMark,
		PaymentStatus: lifecycle.PaymentStatus(deref(r.RiderPaidMark), deref(r.DriverPaidMark)),
		RiderStars:    r.RiderStars, RiderTags: nonNil(r.RiderTags), RiderNote: r.RiderNote, DriverFlags: nonNil(r.DriverFlags),
	}
	if r.Status == lifecycle.OnTheWay && r.PickedUpAt == nil {
		v.NoShowOpensAt = ptr(lifecycle.NoShowOpensAt(r.PickupAt, r.ArrivedAt))
	}
	v.Rider = &RidePerson{ID: r.RiderID, Name: c.RiderName, FirstName: FirstName(c.RiderName), Phone: c.RiderPhone}
	if c.Driver != nil {
		d := c.Driver
		v.Driver = &RideDriver{RidePerson: RidePerson{ID: d.ID, Name: d.Name, FirstName: FirstName(d.Name), Phone: d.Phone},
			VehicleType: d.VehicleType, VehicleNumber: d.VehicleNumber, Plate: d.VehicleNumber, VehicleDesc: d.VehicleDesc,
			Rating: d.Rating, UpiID: d.UpiID}
	}
	if forRider && r.Status == lifecycle.OnTheWay {
		v.Pin = r.Pin // null while there is no PIN step (HLD)
	}
	v.StateLabel = stateLabel(r, c)
	return v
}

// stateLabel is the R2.1 chip.
func stateLabel(r rideRow, c rideCtx) string {
	d := c.driverFirst()
	switch r.Status {
	case lifecycle.Assigned:
		return "Driver confirmed"
	case lifecycle.OnTheWay:
		switch {
		case r.PickedUpAt != nil:
			return "In the ride"
		case r.ArrivedAt != nil:
			return d + " has arrived"
		}
		return d + " is on the way"
	case lifecycle.NeedsDriver:
		return "Finding another driver"
	case lifecycle.Failed:
		return "No driver found"
	case lifecycle.Skipped:
		return "Skipped"
	case lifecycle.Completed:
		return "Completed"
	case lifecycle.RiderNoShow:
		return "Missed"
	case lifecycle.Cancelled:
		return "Cancelled"
	}
	return r.Status
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// rideError is a 4xx carrying the ride as it is now.
func (s *Service) rideError(ctx context.Context, status int, code, msg string, rideID uuid.UUID, forRider bool) error {
	e := &Error{Status: status, Code: code, Message: msg}
	if r, err := readRide(ctx, s.DB, rideID); err == nil {
		if v, err := s.rideView(ctx, s.DB, r, forRider); err == nil {
			e.State = v
		}
	}
	return e
}

// rideEvent writes a ride's events row with its old and new status.
func rideEvent(ctx context.Context, q querier, now time.Time, actor *uuid.UUID, r rideRow, typ, newStatus string, extra map[string]any) error {
	data := map[string]any{"old_status": r.Status, "new_status": newStatus}
	if r.SeriesID != nil {
		data["series_id"] = *r.SeriesID
	}
	for k, v := range extra {
		data[k] = v
	}
	return event(ctx, q, now, actor, "ride", r.ID, typ, data)
}

// dayWord is "Wednesday" for the ride's local date.
func dayWord(date time.Time) string { return date.Weekday().String() }

// pushAdmins sends a message to every admin.
func (s *Service) pushAdmins(ctx context.Context, tx pgx.Tx, now time.Time, kind, title, body string, data map[string]string) error {
	rows, err := tx.Query(ctx, "select user_id from admins order by user_id")
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.push(ctx, tx, now, id, kind, title, body, data); err != nil {
			return err
		}
	}
	return nil
}

func rideData(r rideRow) map[string]string {
	d := map[string]string{"ride_id": r.ID.String()}
	if r.SeriesID != nil {
		d["series_id"] = r.SeriesID.String()
	}
	return d
}

func joinIDs(ids []uuid.UUID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	return strings.Join(parts, ",")
}
