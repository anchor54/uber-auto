package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/clock"
	"commute-backend/internal/notify"
	"commute-backend/internal/offers"
	"commute-backend/internal/scheduling"
	"commute-backend/internal/storage"
)

// LinkTTL is how long signed upload and read links last.
const LinkTTL = 10 * time.Minute

// SupportLine is shown with errors only support can fix.
const SupportLine = "Call support on 080 4000 1234 if this is yours."

// Service holds what the D1 operations need.
type Service struct {
	DB      *pgxpool.Pool
	Clock   clock.Clock
	Storage storage.Storage
	// Cities drivers can sign up in (city code → names accepted). Default: Bengaluru.
	Cities   map[string][]string
	TimeZone string
	// RequireFile makes item_uploaded check that the file is in storage.
	RequireFile bool
	Log         *slog.Logger

	loc *time.Location
}

func (s *Service) now() time.Time { return s.Clock.Now() }

func (s *Service) location() *time.Location {
	if s.loc == nil {
		name := s.TimeZone
		if name == "" {
			name = scheduling.DefaultTimeZone
		}
		l, err := time.LoadLocation(name)
		if err != nil {
			l = time.FixedZone("IST", 330*60)
		}
		s.loc = l
	}
	return s.loc
}

// today is the local date (midnight UTC).
func (s *Service) today(now time.Time) time.Time { return scheduling.DateOf(now, s.location()) }

func (s *Service) cities() map[string][]string {
	if s.Cities != nil {
		return s.Cities
	}
	return map[string][]string{"blr": {"blr", "bengaluru", "bangalore", "blr_in"}}
}

func (s *Service) normalizeCity(c string) (string, bool) {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" && len(s.cities()) == 1 {
		for code := range s.cities() {
			return code, true
		}
	}
	for code, names := range s.cities() {
		if c == code || slices.Contains(names, c) {
			return code, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------- errors

func errf(status int, code, format string, args ...any) *offers.Error {
	return &offers.Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func withState(e *offers.Error, state any) *offers.Error { e.State = state; return e }

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func uniqueViolation(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && (constraint == "" || pg.ConstraintName == constraint)
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) tx(ctx context.Context, f func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.DB, f)
}

func event(ctx context.Context, q querier, at time.Time, actor uuid.UUID, entity string, id uuid.UUID, typ string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `insert into events (at, actor_id, entity, entity_id, type, data, created_at) values ($1, $2, $3, $4, $5, $6, $1)`,
		at, actor, entity, id, typ, raw)
	return err
}

// statusEvent records a driver status change: one events row per transition.
func statusEvent(ctx context.Context, q querier, at time.Time, actor, driver uuid.UUID, from, to string, extra map[string]any) error {
	data := map[string]any{"old_status": from, "new_status": to}
	for k, v := range extra {
		data[k] = v
	}
	return event(ctx, q, at, actor, "driver", driver, "status_changed", data)
}

func push(ctx context.Context, q querier, now time.Time, user uuid.UUID, kind, title, body string, data map[string]string) error {
	return notify.Enqueue(ctx, q, now, notify.Message{UserID: user, Kind: kind, Title: title, Body: body, Data: data})
}

// pushAdmins sends to every admin account.
func pushAdmins(ctx context.Context, q querier, now time.Time, kind, title, body string, data map[string]string) error {
	rows, err := q.Query(ctx, "select id from users where role = 'admin' order by id")
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := push(ctx, q, now, id, kind, title, body, data); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- views

// ItemView is one item as the driver (or admin) sees it.
type ItemView struct {
	Kind         string     `json:"kind"`
	Label        string     `json:"label"`
	State        string     `json:"state"`
	RejectReason *string    `json:"reject_reason"`
	StoragePath  *string    `json:"storage_path,omitempty"`
	Value        *string    `json:"value,omitempty"`
	UploadedAt   *time.Time `json:"uploaded_at"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
	// Admin only: a 10-minute signed link to the file.
	ReadURL *string `json:"read_url,omitempty"`
	URL     *string `json:"url,omitempty"`
	// Set on review responses.
	DriverStatus string `json:"driver_status,omitempty"`
}

// VehicleView is the vehicle.
type VehicleView struct {
	Type          string  `json:"type"`
	CabClass      *string `json:"cab_class"`
	RideType      string  `json:"ride_type"`
	VehicleType   string  `json:"vehicle_type"`
	MakeModel     string  `json:"make_model"`
	Year          *int    `json:"year"`
	Plate         string  `json:"plate"`
	Colour        string  `json:"colour"`
	AC            bool    `json:"ac"`
	CNGOrElectric bool    `json:"cng_or_electric"`
	ReviewState   string  `json:"review_state"`
	State         string  `json:"state"`
	RejectReason  *string `json:"reject_reason"`
}

// PlanView is the plan for a vehicle type.
type PlanView struct {
	VehicleType  string `json:"vehicle_type"`
	CabClass     string `json:"cab_class"`
	RideType     string `json:"ride_type"`
	Label        string `json:"label"`
	MonthlyPrice int    `json:"monthly_price"`
}

// PaymentView is a subscription payment.
type PaymentView struct {
	ID           uuid.UUID  `json:"id"`
	DriverID     uuid.UUID  `json:"driver_id"`
	DriverName   string     `json:"driver_name,omitempty"`
	PeriodStart  string     `json:"period_start"`
	PeriodEnd    string     `json:"period_end"`
	Amount       int        `json:"amount"`
	Reference    string     `json:"reference"`
	Status       string     `json:"status"`
	ClaimedAt    time.Time  `json:"claimed_at"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
	RejectReason *string    `json:"reject_reason"`
}

// DriverView is driver.get: profile, status, items, vehicle, day off, plan
// and latest payment.
type DriverView struct {
	ID                uuid.UUID    `json:"id"`
	UserID            uuid.UUID    `json:"user_id"`
	Name              string       `json:"name"`
	Phone             string       `json:"phone"`
	City              string       `json:"city"`
	Status            string       `json:"status"`
	RejectReason      *string      `json:"reject_reason"`
	Reference         string       `json:"reference"`
	UPIID             *string      `json:"upi_id"`
	DayOff            *int         `json:"day_off"`
	DayOffName        *string      `json:"day_off_name"`
	DayOffSet         bool         `json:"day_off_set"`
	SubscriptionUntil *string      `json:"subscription_until"`
	Rating            float64      `json:"rating"`
	Version           int          `json:"version"`
	SubmittedAt       *time.Time   `json:"submitted_at"`
	ApprovedAt        *time.Time   `json:"approved_at"`
	CreatedAt         time.Time    `json:"created_at"`
	Items             []ItemView   `json:"items"`
	DocumentsAdded    int          `json:"documents_added"`
	DocumentsVerified int          `json:"documents_verified"`
	Missing           []string     `json:"missing"`
	Vehicle           *VehicleView `json:"vehicle"`
	Plan              *PlanView    `json:"plan"`
	LatestPayment     *PaymentView `json:"latest_payment"`
	// Live and paid: receives offers.
	Eligible bool `json:"eligible"`
}

type driverRow struct {
	ID                uuid.UUID
	Name, Phone, City string
	Status            string
	RejectReason      *string
	Reference         string
	UPI               *string
	DayOff            *int
	DayOffSet         bool
	SubscriptionUntil *time.Time
	Rating            float64
	Version           int
	SubmittedAt       *time.Time
	ApprovedAt        *time.Time
	CreatedAt         time.Time
}

const driverSelect = `select d.user_id, d.name, u.phone, d.city, d.status, d.reject_reason, coalesce(d.reference, ''), d.upi_id,
	d.day_off, d.day_off_set, d.subscription_until, d.rating::float8, d.version, d.submitted_at, d.approved_at, d.created_at
	from drivers d join users u on u.id = d.user_id where d.user_id = $1`

func readDriver(ctx context.Context, q querier, id uuid.UUID, lock bool) (driverRow, error) {
	sql := driverSelect
	if lock {
		sql += " for update of d"
	}
	var d driverRow
	err := q.QueryRow(ctx, sql, id).Scan(&d.ID, &d.Name, &d.Phone, &d.City, &d.Status, &d.RejectReason, &d.Reference, &d.UPI,
		&d.DayOff, &d.DayOffSet, &d.SubscriptionUntil, &d.Rating, &d.Version, &d.SubmittedAt, &d.ApprovedAt, &d.CreatedAt)
	return d, err
}

type itemRow struct {
	Kind         string
	Path         *string
	State        string
	RejectReason *string
	UploadedAt   *time.Time
	ReviewedAt   *time.Time
}

func readItems(ctx context.Context, q querier, id uuid.UUID) (map[string]itemRow, error) {
	rows, err := q.Query(ctx, `select kind, storage_path, state, reject_reason, uploaded_at, reviewed_at
		from driver_items where driver_id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (itemRow, error) {
		var it itemRow
		err := r.Scan(&it.Kind, &it.Path, &it.State, &it.RejectReason, &it.UploadedAt, &it.ReviewedAt)
		return it, err
	})
	if err != nil {
		return nil, err
	}
	out := map[string]itemRow{}
	for _, it := range list {
		out[it.Kind] = it
	}
	return out, nil
}

type vehicleRow struct {
	Type         string
	CabClass     *string
	MakeModel    string
	Year         *int
	Plate        string
	Colour       string
	AC, CNG      bool
	ReviewState  string
	RejectReason *string
	ReviewedAt   *time.Time
}

func readVehicle(ctx context.Context, q querier, id uuid.UUID) (*vehicleRow, error) {
	var v vehicleRow
	err := q.QueryRow(ctx, `select type, cab_class, make_model, year, plate, colour, ac, cng_or_electric, review_state, reject_reason, reviewed_at
		from vehicles where driver_id = $1`, id).Scan(&v.Type, &v.CabClass, &v.MakeModel, &v.Year, &v.Plate, &v.Colour, &v.AC, &v.CNG,
		&v.ReviewState, &v.RejectReason, &v.ReviewedAt)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (v *vehicleRow) class() string {
	if v == nil || v.CabClass == nil {
		return ""
	}
	return *v.CabClass
}

func (v *vehicleRow) view() *VehicleView {
	if v == nil {
		return nil
	}
	return &VehicleView{Type: v.Type, CabClass: v.CabClass, RideType: RideType(v.Type, v.class()), VehicleType: v.Type,
		MakeModel: v.MakeModel, Year: v.Year, Plate: v.Plate, Colour: v.Colour, AC: v.AC, CNGOrElectric: v.CNG,
		ReviewState: v.ReviewState, State: v.ReviewState, RejectReason: v.RejectReason}
}

// states is every required kind's state (missing when absent).
func states(d driverRow, items map[string]itemRow, v *vehicleRow) map[string]string {
	out := map[string]string{}
	for _, k := range ReviewKinds {
		out[k] = StateMissing
		if it, ok := items[k]; ok {
			out[k] = it.State
		}
	}
	if v != nil {
		out[KindVehicle] = v.ReviewState
	}
	if d.DayOffSet {
		out[KindDayOff] = StateUploaded
	} else {
		out[KindDayOff] = StateMissing
	}
	return out
}

func readPlan(ctx context.Context, q querier, typ, class string) (*PlanView, error) {
	var p PlanView
	err := q.QueryRow(ctx, "select vehicle_type, coalesce(cab_class, ''), label, monthly_price from plans where vehicle_type = $1 and coalesce(cab_class, '') = $2",
		typ, class).Scan(&p.VehicleType, &p.CabClass, &p.Label, &p.MonthlyPrice)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.RideType = RideType(p.VehicleType, p.CabClass)
	return &p, nil
}

const paymentSelect = `select p.id, p.driver_id, d.name, p.period_start, p.period_end, p.amount, p.reference, p.status,
	p.claimed_at, p.reviewed_at, p.reject_reason from subscription_payments p join drivers d on d.user_id = p.driver_id `

func scanPayment(r pgx.Row) (PaymentView, error) {
	var p PaymentView
	var start, end time.Time
	err := r.Scan(&p.ID, &p.DriverID, &p.DriverName, &start, &end, &p.Amount, &p.Reference, &p.Status, &p.ClaimedAt, &p.ReviewedAt, &p.RejectReason)
	p.PeriodStart, p.PeriodEnd = scheduling.FormatDate(start), scheduling.FormatDate(end)
	return p, err
}

func latestPayment(ctx context.Context, q querier, driver uuid.UUID) (*PaymentView, error) {
	p, err := scanPayment(q.QueryRow(ctx, paymentSelect+"where p.driver_id = $1 order by p.claimed_at desc, p.created_at desc limit 1", driver))
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func readPayment(ctx context.Context, q querier, id uuid.UUID) (PaymentView, error) {
	return scanPayment(q.QueryRow(ctx, paymentSelect+"where p.id = $1", id))
}

// view builds driver.get.
func (s *Service) view(ctx context.Context, q querier, id uuid.UUID, withLinks bool) (DriverView, error) {
	d, err := readDriver(ctx, q, id, false)
	if err != nil {
		return DriverView{}, err
	}
	items, err := readItems(ctx, q, id)
	if err != nil {
		return DriverView{}, err
	}
	v, err := readVehicle(ctx, q, id)
	if err != nil {
		return DriverView{}, err
	}
	st := states(d, items, v)
	out := DriverView{ID: d.ID, UserID: d.ID, Name: d.Name, Phone: d.Phone, City: d.City, Status: d.Status, RejectReason: d.RejectReason,
		Reference: d.Reference, UPIID: d.UPI, DayOff: d.DayOff, DayOffSet: d.DayOffSet, Rating: d.Rating, Version: d.Version,
		SubmittedAt: d.SubmittedAt, ApprovedAt: d.ApprovedAt, CreatedAt: d.CreatedAt, Vehicle: v.view(), Missing: Missing(st)}
	if d.DayOff != nil {
		n := DayName(*d.DayOff)
		out.DayOffName = &n
	}
	if d.SubscriptionUntil != nil {
		su := scheduling.FormatDate(*d.SubscriptionUntil)
		out.SubscriptionUntil = &su
		out.Eligible = d.Status == StatusLive && !d.SubscriptionUntil.Before(s.today(s.now()))
	}
	for _, k := range ReviewKinds {
		iv := ItemView{Kind: k, Label: Label(k), State: st[k]}
		if k == KindVehicle {
			if v != nil {
				iv.RejectReason, iv.ReviewedAt = v.RejectReason, v.ReviewedAt
			}
		} else if it, ok := items[k]; ok {
			iv.RejectReason, iv.StoragePath, iv.UploadedAt, iv.ReviewedAt = it.RejectReason, it.Path, it.UploadedAt, it.ReviewedAt
			if k == KindUPI {
				iv.Value = d.UPI
			}
			if withLinks && it.Path != nil && s.Storage != nil {
				u, err := s.Storage.ReadURL(ctx, *it.Path, LinkTTL)
				if err != nil {
					return out, err
				}
				iv.ReadURL, iv.URL = &u, &u
			}
		}
		out.Items = append(out.Items, iv)
	}
	for _, k := range DocumentKinds {
		switch st[k] {
		case StateUploaded, StateVerified:
			out.DocumentsAdded++
		}
		if st[k] == StateVerified {
			out.DocumentsVerified++
		}
	}
	if v != nil {
		if out.Plan, err = readPlan(ctx, q, v.Type, v.class()); err != nil {
			return out, err
		}
	}
	if out.LatestPayment, err = latestPayment(ctx, q, id); err != nil {
		return out, err
	}
	return out, nil
}

// Get is driver.get. 404 not_registered before driver.register.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (DriverView, error) {
	v, err := s.view(ctx, s.DB, id, false)
	if noRows(err) {
		return v, errf(404, "not_registered", "Sign up as a driver first.")
	}
	return v, err
}

// AdminShow is admin.show_driver: the driver with 10-minute read links.
func (s *Service) AdminShow(ctx context.Context, id uuid.UUID) (DriverView, error) {
	v, err := s.view(ctx, s.DB, id, true)
	if noRows(err) {
		return v, errf(404, "not_found", "No such driver")
	}
	return v, err
}

// stateErr attaches the driver as it is now to an error.
func (s *Service) stateErr(ctx context.Context, e *offers.Error, id uuid.UUID) error {
	v, err := s.view(ctx, s.DB, id, false)
	if err == nil {
		e.State = v
	}
	return e
}

// ---------------------------------------------------------------- register

// RegisterRequest is driver.register.
type RegisterRequest struct {
	Name          string `json:"name"`
	City          string `json:"city"`
	TermsAccepted *bool  `json:"terms_accepted"`
	Terms         *bool  `json:"terms"`
	AcceptTerms   *bool  `json:"accept_terms"`
	VehicleType   string `json:"vehicle_type"` // ignored; set with the vehicle
}

func (r RegisterRequest) terms() bool {
	for _, b := range []*bool{r.TermsAccepted, r.Terms, r.AcceptTerms} {
		if b != nil {
			return *b
		}
	}
	return false
}

// Register is driver.register: a drivers row in signing_up with a fresh
// DRV-xxxxx reference.
func (s *Service) Register(ctx context.Context, user uuid.UUID, req RegisterRequest) (DriverView, error) {
	var role string
	err := s.DB.QueryRow(ctx, "select role from users where id = $1", user).Scan(&role)
	if err != nil {
		return DriverView{}, err
	}
	if role == "rider" {
		return DriverView{}, errf(409, "phone_in_use_as_rider", "This number has a rider account. Use another number to drive.")
	}
	if role != "driver" {
		return DriverView{}, errf(403, "drivers_only", "This needs a driver account.")
	}
	if existing, err := s.view(ctx, s.DB, user, false); err == nil {
		return DriverView{}, withState(errf(409, "already_registered", "You've already signed up. Log in instead."), existing)
	} else if !noRows(err) {
		return DriverView{}, err
	}
	name := strings.Join(strings.Fields(req.Name), " ")
	if len([]rune(name)) < 2 {
		return DriverView{}, errf(422, "invalid_name", "Enter your full name as on your licence.")
	}
	city, ok := s.normalizeCity(req.City)
	if !ok {
		return DriverView{}, errf(422, "city_not_served", "We don't serve that city yet.")
	}
	if !req.terms() {
		return DriverView{}, errf(422, "terms_not_accepted", "Agree to the driver terms to continue.")
	}
	now := s.now()
	for attempt := 0; ; attempt++ {
		ref := NewReference()
		err = s.tx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `insert into drivers (user_id, name, city, status, reference, created_at, updated_at)
				values ($1, $2, $3, 'signing_up', $4, $5, $5)`, user, name, city, ref, now); err != nil {
				return err
			}
			return statusEvent(ctx, tx, now, user, user, "", StatusSigningUp, map[string]any{"reference": ref})
		})
		if uniqueViolation(err, "drivers_reference_key") && attempt < 20 {
			continue
		}
		if uniqueViolation(err, "drivers_pkey") {
			return DriverView{}, s.stateErr(ctx, errf(409, "already_registered", "You've already signed up. Log in instead."), user)
		}
		if err != nil {
			return DriverView{}, err
		}
		break
	}
	return s.view(ctx, s.DB, user, false)
}

// ---------------------------------------------------------------- items

// lockedErr is 409 locked with the driver.
func (s *Service) lockedErr(ctx context.Context, id uuid.UUID, what string) error {
	return s.stateErr(ctx, errf(409, "locked", "%s can't be changed now.", what), id)
}

// editable is the locking rule: everything while signing_up; in
// changes_needed, only items that were rejected (or replaced since).
func editable(status, itemState string) bool {
	switch status {
	case StatusSigningUp:
		return true
	case StatusChangesNeeded:
		return itemState == StateRejected || itemState == StateUploaded
	}
	return false
}

// lockForEdit locks the driver and checks [kind] may change.
func (s *Service) lockForEdit(ctx context.Context, tx pgx.Tx, id uuid.UUID, kind string) (driverRow, error) {
	d, err := readDriver(ctx, tx, id, true)
	if noRows(err) {
		return d, errf(404, "not_registered", "Sign up as a driver first.")
	}
	if err != nil {
		return d, err
	}
	state := StateMissing
	switch kind {
	case KindVehicle:
		var rs string
		err := tx.QueryRow(ctx, "select review_state from vehicles where driver_id = $1", id).Scan(&rs)
		if err == nil {
			state = rs
		} else if !noRows(err) {
			return d, err
		}
	case KindDayOff:
		if d.Status != StatusSigningUp {
			return d, errLocked
		}
	default:
		var st string
		err := tx.QueryRow(ctx, "select state from driver_items where driver_id = $1 and kind = $2", id, kind).Scan(&st)
		if err == nil {
			state = st
		} else if !noRows(err) {
			return d, err
		}
	}
	if !editable(d.Status, state) {
		return d, errLocked
	}
	return d, nil
}

var errLocked = errors.New("locked")

// UploadURLRequest is driver.upload_url.
type UploadURLRequest struct {
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
}

// UploadURL is driver.upload_url: a signed upload URL (10 min) for the
// item's fixed path driver-docs/{driver}/{kind}.
type UploadURL struct {
	Kind        string    `json:"kind"`
	UploadURL   string    `json:"upload_url"`
	URL         string    `json:"url"`
	Method      string    `json:"method"`
	StoragePath string    `json:"storage_path"`
	Path        string    `json:"path"`
	ContentType string    `json:"content_type"`
	MaxBytes    int       `json:"max_bytes"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// StoragePath is an item's file path.
func StoragePath(driver uuid.UUID, kind string) string {
	return storage.Bucket + "/" + driver.String() + "/" + kind
}

func (s *Service) checkFileKind(kind string) (string, error) {
	kind = NormalizeKind(kind)
	if !IsFileKind(kind) {
		return kind, errf(422, "bad_kind", "Unknown document %q", kind)
	}
	return kind, nil
}

func (s *Service) UploadURL(ctx context.Context, id uuid.UUID, req UploadURLRequest) (UploadURL, error) {
	kind, err := s.checkFileKind(req.Kind)
	if err != nil {
		return UploadURL{}, err
	}
	ct := strings.ToLower(strings.TrimSpace(req.ContentType))
	if ct == "" {
		ct = "image/jpeg"
	}
	if ct == "image/jpg" {
		ct = "image/jpeg"
	}
	if !storage.AllowedContentTypes[ct] {
		return UploadURL{}, errf(422, "bad_content_type", "Upload a JPEG or PNG photo.")
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		_, err := s.lockForEdit(ctx, tx, id, kind)
		return err
	})
	if err == errLocked {
		return UploadURL{}, s.lockedErr(ctx, id, Label(kind))
	}
	if err != nil {
		return UploadURL{}, err
	}
	path := StoragePath(id, kind)
	u, err := s.Storage.UploadURL(ctx, path, ct, LinkTTL)
	if err != nil {
		return UploadURL{}, err
	}
	return UploadURL{Kind: kind, UploadURL: u, URL: u, Method: "PUT", StoragePath: path, Path: path, ContentType: ct,
		MaxBytes: storage.MaxFileBytes, ExpiresAt: s.now().Add(LinkTTL)}, nil
}

// ItemUploaded is driver.item_uploaded: the file at [path] is the item.
func (s *Service) ItemUploaded(ctx context.Context, id uuid.UUID, kind, path string) (ItemView, error) {
	kind, err := s.checkFileKind(kind)
	if err != nil {
		return ItemView{}, err
	}
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	if !strings.HasPrefix(path, storage.Bucket+"/"+id.String()+"/") || strings.Contains(path, "..") {
		return ItemView{}, errf(403, "not_your_path", "That file isn't yours.")
	}
	if s.RequireFile {
		ok, err := s.Storage.Exists(ctx, path)
		if err != nil {
			return ItemView{}, err
		}
		if !ok {
			return ItemView{}, errf(409, "not_uploaded", "The file hasn't arrived. Upload it again.")
		}
	}
	now := s.now()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := s.lockForEdit(ctx, tx, id, kind); err != nil {
			return err
		}
		if err := upsertItem(ctx, tx, id, kind, &path, now); err != nil {
			return err
		}
		return event(ctx, tx, now, id, "driver", id, "item_uploaded", map[string]any{"kind": kind, "storage_path": path})
	})
	if err == errLocked {
		return ItemView{}, s.lockedErr(ctx, id, Label(kind))
	}
	if err != nil {
		return ItemView{}, err
	}
	return s.item(ctx, id, kind)
}

func upsertItem(ctx context.Context, tx pgx.Tx, id uuid.UUID, kind string, path *string, now time.Time) error {
	_, err := tx.Exec(ctx, `insert into driver_items (driver_id, kind, storage_path, state, uploaded_at, created_at, updated_at)
		values ($1, $2, $3, 'uploaded', $4, $4, $4)
		on conflict (driver_id, kind) do update set storage_path = excluded.storage_path, state = 'uploaded',
		  reject_reason = null, reviewed_at = null, reviewed_by = null, uploaded_at = excluded.uploaded_at, updated_at = excluded.updated_at`,
		id, kind, path, now)
	return err
}

func (s *Service) item(ctx context.Context, id uuid.UUID, kind string) (ItemView, error) {
	v, err := s.view(ctx, s.DB, id, false)
	if err != nil {
		return ItemView{}, err
	}
	for _, it := range v.Items {
		if it.Kind == kind {
			it.DriverStatus = v.Status
			return it, nil
		}
	}
	return ItemView{}, errf(404, "not_found", "No such item")
}

// SetUPI is driver.set_upi.
func (s *Service) SetUPI(ctx context.Context, id uuid.UUID, upi string) (ItemView, error) {
	upi = strings.TrimSpace(upi)
	now := s.now()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		// Locked comes before a bad pattern.
		if _, err := s.lockForEdit(ctx, tx, id, KindUPI); err != nil {
			return err
		}
		if !ValidUPI(upi) {
			return errf(422, "bad_upi", "Enter a UPI ID like name@bank.")
		}
		if _, err := tx.Exec(ctx, "update drivers set upi_id = $2, updated_at = $3 where user_id = $1", id, upi, now); err != nil {
			return err
		}
		if err := upsertItem(ctx, tx, id, KindUPI, nil, now); err != nil {
			return err
		}
		return event(ctx, tx, now, id, "driver", id, "item_uploaded", map[string]any{"kind": KindUPI})
	})
	if err == errLocked {
		return ItemView{}, s.lockedErr(ctx, id, Label(KindUPI))
	}
	if err != nil {
		return ItemView{}, err
	}
	return s.item(ctx, id, KindUPI)
}

// VehicleRequest is driver.set_vehicle.
type VehicleRequest struct {
	Type          string `json:"type"`
	VehicleType   string `json:"vehicle_type"`
	CabClass      string `json:"cab_class"`
	MakeModel     string `json:"make_model"`
	Year          *int   `json:"year"`
	Plate         string `json:"plate"`
	Colour        string `json:"colour"`
	Color         string `json:"color"`
	AC            bool   `json:"ac"`
	CNGOrElectric bool   `json:"cng_or_electric"`
}

func (r *VehicleRequest) normalize(thisYear int) error {
	t := strings.ToLower(strings.TrimSpace(r.Type))
	if t == "" {
		t = strings.ToLower(strings.TrimSpace(r.VehicleType))
	}
	if c, ok := strings.CutPrefix(t, "cab_"); ok {
		t = "cab"
		if r.CabClass == "" {
			r.CabClass = c
		}
	}
	r.Type = t
	r.CabClass = strings.ToLower(strings.TrimSpace(r.CabClass))
	switch t {
	case "bike", "auto":
		r.CabClass = ""
	case "cab":
		if !slices.Contains([]string{"hatchback", "sedan", "suv"}, r.CabClass) {
			return errf(422, "bad_cab_class", "Choose hatchback, sedan or SUV.")
		}
	default:
		return errf(422, "bad_vehicle_type", "Choose bike, auto or cab.")
	}
	r.Plate = NormalizePlate(r.Plate)
	if !ValidPlate(r.Plate) {
		return errf(422, "bad_plate", "Enter the number plate as on the RC.")
	}
	if r.Year != nil && (*r.Year < 1980 || *r.Year > thisYear+1) {
		return errf(422, "bad_year", "Enter the year the vehicle was made.")
	}
	if r.Colour == "" {
		r.Colour = r.Color
	}
	r.Colour = strings.TrimSpace(r.Colour)
	r.MakeModel = strings.TrimSpace(r.MakeModel)
	if r.Type != "cab" {
		// Working AC is for cabs only.
		r.AC = false
	}
	return nil
}

// SetVehicle is driver.set_vehicle. The vehicle type decides the offers
// the driver receives (copied to drivers.vehicle_type).
func (s *Service) SetVehicle(ctx context.Context, id uuid.UUID, req VehicleRequest) (VehicleView, error) {
	now := s.now()
	if err := req.normalize(now.Year()); err != nil {
		return VehicleView{}, err
	}
	var class *string
	if req.CabClass != "" {
		class = &req.CabClass
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := s.lockForEdit(ctx, tx, id, KindVehicle); err != nil {
			return err
		}
		var other uuid.UUID
		err := tx.QueryRow(ctx, `select driver_id from vehicles where regexp_replace(plate, '[^A-Z0-9]', '', 'g') = $1 and driver_id <> $2`,
			PlateKey(req.Plate), id).Scan(&other)
		if err == nil {
			return errPlate
		} else if !noRows(err) {
			return err
		}
		_, err = tx.Exec(ctx, `insert into vehicles (driver_id, type, cab_class, make_model, year, plate, colour, ac, cng_or_electric,
				review_state, created_at, updated_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'uploaded', $10, $10)
			on conflict (driver_id) do update set type = excluded.type, cab_class = excluded.cab_class, make_model = excluded.make_model,
			  year = excluded.year, plate = excluded.plate, colour = excluded.colour, ac = excluded.ac,
			  cng_or_electric = excluded.cng_or_electric, review_state = 'uploaded', reject_reason = null, reviewed_at = null,
			  updated_at = excluded.updated_at`,
			id, req.Type, class, req.MakeModel, req.Year, req.Plate, req.Colour, req.AC, req.CNGOrElectric, now)
		if uniqueViolation(err, "vehicles_plate_key") {
			return errPlate
		}
		if err != nil {
			return err
		}
		desc := strings.TrimSpace(strings.Join(strings.Fields(req.Colour+" "+req.MakeModel), " "))
		if _, err := tx.Exec(ctx, `update drivers set vehicle_type = $2, vehicle_number = $3, vehicle_desc = $4, updated_at = $5
			where user_id = $1`, id, RideType(req.Type, req.CabClass), req.Plate, desc, now); err != nil {
			return err
		}
		return event(ctx, tx, now, id, "driver", id, "vehicle_set", map[string]any{"type": req.Type, "cab_class": req.CabClass, "plate": req.Plate})
	})
	switch {
	case err == errLocked:
		return VehicleView{}, s.lockedErr(ctx, id, "Your vehicle")
	case err == errPlate:
		return VehicleView{}, errf(409, "plate_in_use", "This number plate is already registered with another driver. %s", SupportLine)
	case err != nil:
		return VehicleView{}, err
	}
	v, err := readVehicle(ctx, s.DB, id)
	if err != nil {
		return VehicleView{}, err
	}
	return *v.view(), nil
}

var errPlate = errors.New("plate in use")

// SetDayOff is driver.set_day_off: a weekday (1–7) or nil for "No fixed
// day off". Changing it after sign-up is a D4 action.
func (s *Service) SetDayOff(ctx context.Context, id uuid.UUID, weekday *int) (DriverView, error) {
	if weekday != nil && (*weekday < 1 || *weekday > 7) {
		return DriverView{}, errf(422, "bad_weekday", "Pick a day from Monday to Sunday, or no fixed day off.")
	}
	now := s.now()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := s.lockForEdit(ctx, tx, id, KindDayOff); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update drivers set day_off = $2, day_off_set = true, updated_at = $3 where user_id = $1",
			id, weekday, now); err != nil {
			return err
		}
		return event(ctx, tx, now, id, "driver", id, "day_off_set", map[string]any{"day_off": weekday})
	})
	if err == errLocked {
		return DriverView{}, s.lockedErr(ctx, id, "Your day off")
	}
	if err != nil {
		return DriverView{}, err
	}
	return s.view(ctx, s.DB, id, false)
}

// Submit is driver.submit: every item present → submitted.
func (s *Service) Submit(ctx context.Context, id uuid.UUID) (DriverView, error) {
	now := s.now()
	var missing []string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		d, err := readDriver(ctx, tx, id, true)
		if noRows(err) {
			return errf(404, "not_registered", "Sign up as a driver first.")
		}
		if err != nil {
			return err
		}
		if d.Status != StatusSigningUp && d.Status != StatusChangesNeeded {
			return errf(409, "wrong_status", "You've already submitted.")
		}
		items, err := readItems(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err := readVehicle(ctx, tx, id)
		if err != nil {
			return err
		}
		if missing = Missing(states(d, items, v)); len(missing) > 0 {
			return errIncomplete
		}
		if _, err := tx.Exec(ctx, `update drivers set status = 'submitted', submitted_at = $2, reject_reason = null,
			version = version + 1, updated_at = $2 where user_id = $1 and status = $3`, id, now, d.Status); err != nil {
			return err
		}
		if err := statusEvent(ctx, tx, now, id, id, d.Status, StatusSubmitted, nil); err != nil {
			return err
		}
		return pushAdmins(ctx, tx, now, "admin_driver_submitted", "Driver to review", fmt.Sprintf("%s is waiting for review", d.Name),
			map[string]string{"driver_id": id.String()})
	})
	if err == errIncomplete {
		labels := make([]string, len(missing))
		for i, k := range missing {
			labels[i] = Label(k)
		}
		e := errf(409, "incomplete", "Add these first: %s.", strings.Join(labels, ", "))
		e.Extra = map[string]any{"missing": missing}
		return DriverView{}, s.stateErr(ctx, e, id)
	}
	var oe *offers.Error
	if errors.As(err, &oe) && oe.Code == "wrong_status" {
		return DriverView{}, s.stateErr(ctx, oe, id)
	}
	if err != nil {
		return DriverView{}, err
	}
	return s.view(ctx, s.DB, id, false)
}

var errIncomplete = errors.New("incomplete")

// ---------------------------------------------------------------- plan and payment

// PlanDue is plans.get: the plan for the driver's vehicle type and the amount due now.
type PlanDue struct {
	Plan          PlanView     `json:"plan"`
	MonthlyPrice  int          `json:"monthly_price"`
	AmountDue     int          `json:"amount_due"`
	PeriodStart   string       `json:"period_start"`
	PeriodEnd     string       `json:"period_end"`
	DaysLeft      int          `json:"days_left"`
	DaysInMonth   int          `json:"days_in_month"`
	Reference     string       `json:"reference"`
	PayeeUPI      string       `json:"payee_upi"`
	Status        string       `json:"status"`
	LatestPayment *PaymentView `json:"latest_payment"`
}

func canPay(status string) bool {
	return status == StatusApproved || status == StatusPaused
}

// due works out the period and amount for [d] on [today].
func (s *Service) due(ctx context.Context, q querier, d driverRow, today time.Time) (PlanDue, error) {
	v, err := readVehicle(ctx, q, d.ID)
	if err != nil {
		return PlanDue{}, err
	}
	if v == nil {
		return PlanDue{}, errf(409, "not_approved", "Choose a plan once you're approved.")
	}
	p, err := readPlan(ctx, q, v.Type, v.class())
	if err != nil {
		return PlanDue{}, err
	}
	if p == nil {
		return PlanDue{}, fmt.Errorf("no plan for %s %s", v.Type, v.class())
	}
	from := today
	if d.Status == StatusLive && d.SubscriptionUntil != nil && !d.SubscriptionUntil.Before(today) {
		from = d.SubscriptionUntil.AddDate(0, 0, 1)
	}
	start, end := Period(from)
	out := PlanDue{Plan: *p, MonthlyPrice: p.MonthlyPrice, AmountDue: AmountDue(p.MonthlyPrice, start),
		PeriodStart: scheduling.FormatDate(start), PeriodEnd: scheduling.FormatDate(end),
		DaysLeft: DaysIn(start) - start.Day() + 1, DaysInMonth: DaysIn(start), Reference: d.Reference, Status: d.Status}
	var payee string
	if err := q.QueryRow(ctx, "select value #>> '{}' from app_config where key = 'payee_upi'").Scan(&payee); err != nil && !noRows(err) {
		return out, err
	}
	out.PayeeUPI = payee
	return out, nil
}

// Plan is plans.get. Shown once the driver is approved (or live, or paused).
func (s *Service) Plan(ctx context.Context, id uuid.UUID) (PlanDue, error) {
	d, err := readDriver(ctx, s.DB, id, false)
	if noRows(err) {
		return PlanDue{}, errf(404, "not_registered", "Sign up as a driver first.")
	}
	if err != nil {
		return PlanDue{}, err
	}
	if !canPay(d.Status) && d.Status != StatusLive {
		return PlanDue{}, s.stateErr(ctx, errf(409, "not_approved", "Your plan shows once you're approved."), id)
	}
	out, err := s.due(ctx, s.DB, d, s.today(s.now()))
	if err != nil {
		return out, err
	}
	out.LatestPayment, err = latestPayment(ctx, s.DB, id)
	return out, err
}

// ClaimPayment is driver.claim_payment ("I've paid"). [periodStart] is
// optional; it must fall in the period due now.
func (s *Service) ClaimPayment(ctx context.Context, id uuid.UUID, periodStart string) (PaymentView, error) {
	now := s.now()
	today := s.today(now)
	var payID uuid.UUID
	var existing *PaymentView
	err := s.tx(ctx, func(tx pgx.Tx) error {
		d, err := readDriver(ctx, tx, id, true)
		if noRows(err) {
			return errf(404, "not_registered", "Sign up as a driver first.")
		}
		if err != nil {
			return err
		}
		if !canPay(d.Status) {
			if periodStart != "" {
				// A claim for a period already claimed or paid (e.g. a live
				// driver tapping "I've paid" again) is already_claimed.
				p, err := scanPayment(tx.QueryRow(ctx, paymentSelect+`where p.driver_id = $1 and p.status in ('claimed', 'confirmed')
					and p.period_start::text = $2 order by p.claimed_at desc limit 1`, id, periodStart))
				if err == nil {
					existing = &p
					return errClaimed
				} else if !noRows(err) {
					return err
				}
			}
			return errf(409, "not_approved", "You can pay once you're approved.")
		}
		due, err := s.due(ctx, tx, d, today)
		if err != nil {
			return err
		}
		if periodStart != "" {
			ps, err := scheduling.ParseDate(periodStart)
			if err != nil {
				return errf(422, "bad_period", "period_start must be YYYY-MM-DD")
			}
			if f := scheduling.FormatDate(ps); f != due.PeriodStart && (f < due.PeriodStart[:8]+"01" || f > due.PeriodEnd) {
				return errf(422, "bad_period", "The period due now starts %s.", due.PeriodStart)
			}
		}
		// One open claim at a time, and one claimed or confirmed payment per period.
		p, err := scanPayment(tx.QueryRow(ctx, paymentSelect+`where p.driver_id = $1 and (p.status = 'claimed'
			or (p.status = 'confirmed' and p.period_start = $2)) order by p.claimed_at desc limit 1`, id, due.PeriodStart))
		if err == nil {
			existing = &p
			return errClaimed
		} else if !noRows(err) {
			return err
		}
		err = tx.QueryRow(ctx, `insert into subscription_payments (driver_id, period_start, period_end, amount, reference, status, claimed_at, created_at)
			values ($1, $2, $3, $4, $5, 'claimed', $6, $6) returning id`, id, due.PeriodStart, due.PeriodEnd, due.AmountDue, d.Reference, now).Scan(&payID)
		if uniqueViolation(err, "subscription_payments_one_per_period") {
			return errClaimed
		}
		if err != nil {
			return err
		}
		if err := event(ctx, tx, now, id, "subscription_payment", payID, "payment_claimed",
			map[string]any{"old_status": nil, "new_status": "claimed", "driver_id": id, "amount": due.AmountDue, "period_start": due.PeriodStart, "period_end": due.PeriodEnd}); err != nil {
			return err
		}
		return pushAdmins(ctx, tx, now, "admin_payment_claimed", "Payment to confirm",
			fmt.Sprintf("%s says they've paid ₹%d", d.Name, due.AmountDue), map[string]string{"payment_id": payID.String(), "driver_id": id.String()})
	})
	if err == errClaimed {
		e := errf(409, "already_claimed", "We're checking your payment already.")
		if existing == nil {
			if p, err := latestPayment(ctx, s.DB, id); err == nil {
				existing = p
			}
		}
		if existing != nil {
			e.State = *existing
		}
		return PaymentView{}, e
	}
	var oe *offers.Error
	if errors.As(err, &oe) && oe.Code == "not_approved" {
		return PaymentView{}, s.stateErr(ctx, oe, id)
	}
	if err != nil {
		return PaymentView{}, err
	}
	return readPayment(ctx, s.DB, payID)
}

var errClaimed = errors.New("already claimed")

// ---------------------------------------------------------------- admin

// Pending is admin.pending: submitted drivers and claimed payments, oldest first.
type Pending struct {
	Drivers  []DriverView  `json:"drivers"`
	Payments []PaymentView `json:"payments"`
}

func (s *Service) Pending(ctx context.Context) (Pending, error) {
	out := Pending{Drivers: []DriverView{}, Payments: []PaymentView{}}
	rows, err := s.DB.Query(ctx, "select user_id from drivers where status = 'submitted' order by submitted_at, user_id")
	if err != nil {
		return out, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		v, err := s.view(ctx, s.DB, id, false)
		if err != nil {
			return out, err
		}
		out.Drivers = append(out.Drivers, v)
	}
	rows, err = s.DB.Query(ctx, paymentSelect+"where p.status = 'claimed' order by p.claimed_at, p.id")
	if err != nil {
		return out, err
	}
	pays, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PaymentView, error) { return scanPayment(r) })
	if err != nil {
		return out, err
	}
	out.Payments = append(out.Payments, pays...)
	return out, nil
}

// lockAdmin locks a driver for an admin action; 404 when missing.
func lockAdmin(ctx context.Context, tx pgx.Tx, id uuid.UUID) (driverRow, error) {
	d, err := readDriver(ctx, tx, id, true)
	if noRows(err) {
		return d, errf(404, "not_found", "No such driver")
	}
	return d, err
}

// NormalizeDecision reads verified / rejected (and verify, reject, approve…).
func NormalizeDecision(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "verified", "verify", "approve", "approved", "ok", "accept", "accepted":
		return StateVerified
	case "rejected", "reject", "fix", "declined":
		return StateRejected
	}
	return ""
}

// ReviewItem is admin.review_item. When the last pending item of a
// submitted driver is reviewed and any item is rejected, the driver moves
// to changes_needed with one push listing every rejected item.
func (s *Service) ReviewItem(ctx context.Context, admin, id uuid.UUID, kind, decision, reason string) (ItemView, error) {
	kind = NormalizeKind(kind)
	if !IsReviewKind(kind) {
		return ItemView{}, errf(422, "bad_kind", "Unknown item %q", kind)
	}
	decision = NormalizeDecision(decision)
	if decision == "" {
		return ItemView{}, errf(422, "bad_decision", "The decision is verified or rejected.")
	}
	reason = strings.TrimSpace(reason)
	if decision == StateRejected && reason == "" {
		return ItemView{}, errf(422, "reason_required", "Give a reason for the rejection.")
	}
	var rp *string
	if decision == StateRejected {
		rp = &reason
	}
	now := s.now()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		d, err := lockAdmin(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Status != StatusSubmitted {
			return errf(409, "wrong_status", "%s isn't waiting for review (%s).", d.Name, d.Status)
		}
		var tag pgconn.CommandTag
		if kind == KindVehicle {
			tag, err = tx.Exec(ctx, `update vehicles set review_state = $2, reject_reason = $3, reviewed_at = $4, updated_at = $4
				where driver_id = $1`, id, decision, rp, now)
		} else {
			tag, err = tx.Exec(ctx, `update driver_items set state = $3, reject_reason = $4, reviewed_at = $5, reviewed_by = $6, updated_at = $5
				where driver_id = $1 and kind = $2`, id, kind, decision, rp, now, admin)
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf(404, "not_found", "%s hasn't been added", Label(kind))
		}
		if err := event(ctx, tx, now, admin, "driver", id, "item_reviewed", map[string]any{"kind": kind, "state": decision, "reason": rp}); err != nil {
			return err
		}
		items, err := readItems(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err := readVehicle(ctx, tx, id)
		if err != nil {
			return err
		}
		st := states(d, items, v)
		var rejected []string
		for _, k := range ReviewKinds {
			switch st[k] {
			case StateUploaded:
				return nil // review not finished
			case StateRejected:
				rejected = append(rejected, k)
			}
		}
		if len(rejected) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `update drivers set status = 'changes_needed', version = version + 1, updated_at = $2
			where user_id = $1 and status = 'submitted'`, id, now); err != nil {
			return err
		}
		if err := statusEvent(ctx, tx, now, admin, id, StatusSubmitted, StatusChangesNeeded, map[string]any{"rejected": rejected}); err != nil {
			return err
		}
		labels := make([]string, len(rejected))
		for i, k := range rejected {
			labels[i] = Label(k)
		}
		return push(ctx, tx, now, id, "driver_changes_needed", "A few things need fixing",
			fmt.Sprintf("A few things need fixing: %s. Open the app to see why.", strings.Join(labels, ", ")),
			map[string]string{"driver_id": id.String(), "items": strings.Join(rejected, ",")})
	})
	if err != nil {
		var oe *offers.Error
		if errors.As(err, &oe) && oe.Code == "wrong_status" {
			return ItemView{}, s.stateErr(ctx, oe, id)
		}
		return ItemView{}, err
	}
	return s.item(ctx, id, kind)
}

// Approve is admin.approve: only when every item is verified.
func (s *Service) Approve(ctx context.Context, admin, id uuid.UUID) (DriverView, error) {
	now := s.now()
	var unverified []string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		d, err := lockAdmin(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Status != StatusSubmitted {
			return errf(409, "wrong_status", "%s isn't waiting for review (%s).", d.Name, d.Status)
		}
		items, err := readItems(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err := readVehicle(ctx, tx, id)
		if err != nil {
			return err
		}
		if unverified = Unverified(states(d, items, v)); len(unverified) > 0 {
			return errNotVerified
		}
		tag, err := tx.Exec(ctx, `update drivers set status = 'approved', approved_at = $2, reject_reason = null,
			version = version + 1, updated_at = $2 where user_id = $1 and status = 'submitted'`, id, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errf(409, "wrong_status", "Someone got there first.")
		}
		if err := statusEvent(ctx, tx, now, admin, id, StatusSubmitted, StatusApproved, nil); err != nil {
			return err
		}
		return push(ctx, tx, now, id, "driver_approved", "You're approved", "You're approved. Choose your plan to go live.",
			map[string]string{"driver_id": id.String()})
	})
	if err == errNotVerified {
		e := errf(409, "items_not_verified", "Verify every item first: %s.", strings.Join(unverified, ", "))
		e.Extra = map[string]any{"unverified": unverified}
		return DriverView{}, s.stateErr(ctx, e, id)
	}
	if err != nil {
		var oe *offers.Error
		if errors.As(err, &oe) && oe.Code == "wrong_status" {
			return DriverView{}, s.stateErr(ctx, oe, id)
		}
		return DriverView{}, err
	}
	return s.view(ctx, s.DB, id, false)
}

var errNotVerified = errors.New("items not verified")

// Reject is admin.reject: the application ends for good.
func (s *Service) Reject(ctx context.Context, admin, id uuid.UUID, reason string) (DriverView, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return DriverView{}, errf(422, "reason_required", "Give a reason.")
	}
	now := s.now()
	err := s.tx(ctx, func(tx pgx.Tx) error {
		d, err := lockAdmin(ctx, tx, id)
		if err != nil {
			return err
		}
		if !slices.Contains([]string{StatusSubmitted, StatusChangesNeeded, StatusApproved}, d.Status) {
			return errf(409, "wrong_status", "%s can't be rejected now (%s).", d.Name, d.Status)
		}
		if _, err := tx.Exec(ctx, `update drivers set status = 'rejected', reject_reason = $2, version = version + 1, updated_at = $3
			where user_id = $1 and status = $4`, id, reason, now, d.Status); err != nil {
			return err
		}
		if err := statusEvent(ctx, tx, now, admin, id, d.Status, StatusRejected, map[string]any{"reason": reason}); err != nil {
			return err
		}
		return push(ctx, tx, now, id, "driver_rejected", "Application closed", "We can't approve your application. Open the app for details.",
			map[string]string{"driver_id": id.String()})
	})
	if err != nil {
		var oe *offers.Error
		if errors.As(err, &oe) && oe.Code == "wrong_status" {
			return DriverView{}, s.stateErr(ctx, oe, id)
		}
		return DriverView{}, err
	}
	return s.view(ctx, s.DB, id, false)
}

// PaymentResult is admin.confirm_payment / admin.reject_payment.
type PaymentResult struct {
	Payment PaymentView `json:"payment"`
	Driver  DriverView  `json:"driver"`
}

// paymentErr is 404 or 409 wrong_status with the payment as it is now.
func (s *Service) paymentErr(ctx context.Context, id uuid.UUID) error {
	p, err := readPayment(ctx, s.DB, id)
	if noRows(err) {
		return errf(404, "not_found", "No such payment")
	}
	if err != nil {
		return err
	}
	return withState(errf(409, "wrong_status", "This payment is already %s.", p.Status), p)
}

// ConfirmPayment is admin.confirm_payment: in one transaction the payment
// is confirmed, subscription_until moves to the period's end, and an
// approved (or paused) driver goes live.
func (s *Service) ConfirmPayment(ctx context.Context, admin, id uuid.UUID) (PaymentResult, error) {
	now := s.now()
	var driver uuid.UUID
	var lost bool
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var periodEnd time.Time
		err := tx.QueryRow(ctx, `update subscription_payments set status = 'confirmed', reviewed_at = $2, reviewed_by = $3
			where id = $1 and status = 'claimed' returning driver_id, period_end`, id, now, admin).Scan(&driver, &periodEnd)
		if noRows(err) {
			lost = true
			return nil
		}
		if err != nil {
			return err
		}
		d, err := readDriver(ctx, tx, driver, true)
		if err != nil {
			return err
		}
		to := d.Status
		if canPay(d.Status) {
			to = StatusLive
		}
		if _, err := tx.Exec(ctx, `update drivers set status = $2,
			subscription_until = greatest(coalesce(subscription_until, $3::date), $3::date), version = version + 1, updated_at = $4
			where user_id = $1`, driver, to, periodEnd, now); err != nil {
			return err
		}
		if err := event(ctx, tx, now, admin, "subscription_payment", id, "payment_confirmed", map[string]any{
			"old_status": "claimed", "new_status": "confirmed", "driver_id": driver, "driver_status": to, "subscription_until": scheduling.FormatDate(periodEnd)}); err != nil {
			return err
		}
		if to != d.Status {
			if err := statusEvent(ctx, tx, now, admin, driver, d.Status, to, map[string]any{"payment_id": id}); err != nil {
				return err
			}
		}
		typ := "auto"
		if v, err := readVehicle(ctx, tx, driver); err != nil {
			return err
		} else if v != nil {
			typ = TypeLabel(v.Type, v.class())
		}
		return push(ctx, tx, now, driver, "driver_live", "You're live",
			fmt.Sprintf("You're live. New requests for %s drivers will show up in Requests.", typ),
			map[string]string{"driver_id": driver.String(), "payment_id": id.String()})
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if lost {
		return PaymentResult{}, s.paymentErr(ctx, id)
	}
	return s.paymentResult(ctx, id, driver)
}

// RejectPayment is admin.reject_payment: the driver can claim again.
func (s *Service) RejectPayment(ctx context.Context, admin, id uuid.UUID, reason string) (PaymentResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return PaymentResult{}, errf(422, "reason_required", "Give a reason.")
	}
	now := s.now()
	var driver uuid.UUID
	var lost bool
	err := s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `update subscription_payments set status = 'rejected', reviewed_at = $2, reviewed_by = $3, reject_reason = $4
			where id = $1 and status = 'claimed' returning driver_id`, id, now, admin, reason).Scan(&driver)
		if noRows(err) {
			lost = true
			return nil
		}
		if err != nil {
			return err
		}
		if err := event(ctx, tx, now, admin, "subscription_payment", id, "payment_rejected",
			map[string]any{"old_status": "claimed", "new_status": "rejected", "driver_id": driver, "reason": reason}); err != nil {
			return err
		}
		return push(ctx, tx, now, driver, "driver_payment_rejected", "Payment not found",
			"We couldn't find your payment. Open the app to try again.", map[string]string{"driver_id": driver.String(), "payment_id": id.String()})
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if lost {
		return PaymentResult{}, s.paymentErr(ctx, id)
	}
	return s.paymentResult(ctx, id, driver)
}

func (s *Service) paymentResult(ctx context.Context, id, driver uuid.UUID) (PaymentResult, error) {
	p, err := readPayment(ctx, s.DB, id)
	if err != nil {
		return PaymentResult{}, err
	}
	v, err := s.view(ctx, s.DB, driver, false)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{Payment: p, Driver: v}, nil
}
