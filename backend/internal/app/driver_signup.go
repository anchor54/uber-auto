package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"commute-backend/internal/db"
	"commute-backend/internal/domain"
)

// Flow D1, the driver's side: register, add documents, vehicle and day off,
// submit for review, read the plan and claim the first payment. Every
// change locks the driver row, checks the status it expects and bumps
// drivers.version; status changes write a driver_events row and their
// notifications in the same transaction.

// Where a registered driver's matching starts (the design dropped areas and
// working hours: every driver of the vehicle type sees every request).
const (
	cityCentreLat         = 12.9716
	cityCentreLng         = 77.5946
	signupServiceRadiusKm = 50
)

// ---------------------------------------------------------------- JSON

type driverItemJSON struct {
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	State        string  `json:"state"`
	RejectReason *string `json:"rejectReason"`
	Value        *string `json:"value"`
	// Admin's view only: a 10-minute link to the file.
	URL *string `json:"url,omitempty"`
}

type driverVehicleJSON struct {
	Type          string  `json:"type"`
	CabClass      *string `json:"cabClass"`
	MakeModel     string  `json:"makeModel"`
	Year          int     `json:"year"`
	Plate         string  `json:"plate"`
	Colour        string  `json:"colour"`
	AC            bool    `json:"ac"`
	CngOrElectric bool    `json:"cngOrElectric"`
	ReviewState   string  `json:"reviewState"`
	RejectReason  *string `json:"rejectReason"`
}

type driverPlanJSON struct {
	RideType     string  `json:"rideType"`
	VehicleType  string  `json:"vehicleType"`
	CabClass     *string `json:"cabClass"`
	Name         string  `json:"name"`
	MonthlyPrice int     `json:"monthlyPrice"`
}

type subscriptionPaymentJSON struct {
	ID           string  `json:"id"`
	PeriodStart  string  `json:"periodStart"`
	PeriodEnd    string  `json:"periodEnd"`
	Amount       int     `json:"amount"`
	Status       string  `json:"status"`
	ClaimedAt    string  `json:"claimedAt"`
	RejectReason *string `json:"rejectReason"`
}

// driverState is the driver as D1's screens and the admin tool see them.
type driverState struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	Phone             string                   `json:"phone"`
	City              string                   `json:"city"`
	Status            string                   `json:"status"`
	RejectReason      *string                  `json:"rejectReason"`
	Reference         *string                  `json:"reference"`
	DayOff            *int                     `json:"dayOff"`
	DayOffSet         bool                     `json:"dayOffSet"`
	SubscriptionUntil *string                  `json:"subscriptionUntil"`
	Items             []driverItemJSON         `json:"items"`
	Vehicle           *driverVehicleJSON       `json:"vehicle"`
	Plan              *driverPlanJSON          `json:"plan"`
	LatestPayment     *subscriptionPaymentJSON `json:"latestPayment"`
	Missing           []string                 `json:"missing"`

	rideType string
}

// itemState is the state of [kind] ("vehicle" included), "missing" when not added.
func (d *driverState) itemState(kind string) string {
	if kind == domain.VehicleItem {
		if d.Vehicle == nil {
			return domain.ItemMissing
		}
		return d.Vehicle.ReviewState
	}
	for _, it := range d.Items {
		if it.Kind == kind {
			return it.State
		}
	}
	return domain.ItemMissing
}

func (d *driverState) item(kind string) driverItemJSON {
	if kind == domain.VehicleItem {
		it := driverItemJSON{Kind: kind, Label: domain.ItemLabel(kind), State: domain.ItemMissing}
		if d.Vehicle != nil {
			it.State, it.RejectReason = d.Vehicle.ReviewState, d.Vehicle.RejectReason
		}
		return it
	}
	for _, it := range d.Items {
		if it.Kind == kind {
			return it
		}
	}
	return driverItemJSON{Kind: kind, Label: domain.ItemLabel(kind), State: domain.ItemMissing}
}

const paymentColumns = `id, to_char(period_start, 'YYYY-MM-DD'), to_char(period_end, 'YYYY-MM-DD'), amount, status, claimed_at, reject_reason`

func scanPayment(row pgx.Row) (subscriptionPaymentJSON, error) {
	var p subscriptionPaymentJSON
	var claimedAt time.Time
	err := row.Scan(&p.ID, &p.PeriodStart, &p.PeriodEnd, &p.Amount, &p.Status, &claimedAt, &p.RejectReason)
	p.ClaimedAt = iso(claimedAt)
	return p, err
}

func loadPayment(ctx context.Context, q db.Q, id string) (subscriptionPaymentJSON, string, error) {
	var driverID string
	var p subscriptionPaymentJSON
	var claimedAt time.Time
	err := q.QueryRow(ctx, `select driver_id, `+paymentColumns+` from subscription_payments where id = $1`, id).
		Scan(&driverID, &p.ID, &p.PeriodStart, &p.PeriodEnd, &p.Amount, &p.Status, &claimedAt, &p.RejectReason)
	p.ClaimedAt = iso(claimedAt)
	return p, driverID, err
}

func loadPlan(ctx context.Context, q db.Q, rideType string) (*driverPlanJSON, error) {
	p := driverPlanJSON{RideType: rideType, Name: domain.PlanName(rideType)}
	err := q.QueryRow(ctx, "select vehicle_type, cab_class, monthly_price from plans where ride_type = $1", rideType).
		Scan(&p.VehicleType, &p.CabClass, &p.MonthlyPrice)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// loadDriverState reads the driver [id]; pgx.ErrNoRows when there is none.
// With [readURL], each uploaded file item gets a signed link (the admin's view).
func (a *App) loadDriverState(ctx context.Context, q db.Q, id string, readURL func(path string) (string, error)) (driverState, error) {
	var d driverState
	var upiID string
	err := q.QueryRow(ctx, `select id, name, coalesce(phone, ''), city, status, reject_reason, reference, day_off, day_off_set,
		  to_char(subscription_until, 'YYYY-MM-DD'), upi_id, ride_type
		from drivers where id = $1`, id).Scan(&d.ID, &d.Name, &d.Phone, &d.City, &d.Status, &d.RejectReason, &d.Reference,
		&d.DayOff, &d.DayOffSet, &d.SubscriptionUntil, &upiID, &d.rideType)
	if err != nil {
		return d, err
	}

	type itemRow struct {
		path, reason *string
		state        string
	}
	rows, err := q.Query(ctx, "select kind, storage_path, state, reject_reason from driver_items where driver_id = $1", id)
	if err != nil {
		return d, err
	}
	stored := map[string]itemRow{}
	for rows.Next() {
		var kind string
		var it itemRow
		if err := rows.Scan(&kind, &it.path, &it.state, &it.reason); err != nil {
			rows.Close()
			return d, err
		}
		stored[kind] = it
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, err
	}
	states := map[string]string{}
	d.Items = []driverItemJSON{}
	for _, kind := range domain.DriverItemKinds {
		it := driverItemJSON{Kind: kind, Label: domain.ItemLabel(kind), State: domain.ItemMissing}
		if s, ok := stored[kind]; ok {
			it.State, it.RejectReason = s.state, s.reason
			states[kind] = s.state
			if kind == "upi_id" && upiID != "" {
				v := upiID
				it.Value = &v
			}
			if readURL != nil && s.path != nil {
				u, err := readURL(*s.path)
				if err != nil {
					return d, err
				}
				it.URL = &u
			}
		}
		d.Items = append(d.Items, it)
	}

	var v driverVehicleJSON
	err = q.QueryRow(ctx, `select type, cab_class, make_model, year, plate, colour, ac, cng_or_electric, review_state, reject_reason
		from vehicles where driver_id = $1`, id).Scan(&v.Type, &v.CabClass, &v.MakeModel, &v.Year, &v.Plate, &v.Colour, &v.AC,
		&v.CngOrElectric, &v.ReviewState, &v.RejectReason)
	switch {
	case err == nil:
		d.Vehicle = &v
	case !noRows(err):
		return d, err
	}

	if d.rideType != "" {
		if d.Plan, err = loadPlan(ctx, q, d.rideType); err != nil {
			return d, err
		}
	}

	p, err := scanPayment(q.QueryRow(ctx, `select `+paymentColumns+` from subscription_payments where driver_id = $1
		order by claimed_at desc, created_at desc limit 1`, id))
	switch {
	case err == nil:
		d.LatestPayment = &p
	case !noRows(err):
		return d, err
	}

	vehicleSet := d.Vehicle != nil && d.Vehicle.ReviewState != domain.ItemRejected
	d.Missing = domain.MissingForSubmit(states, vehicleSet, d.DayOffSet)
	return d, nil
}

// ---------------------------------------------------------------- helpers

// lockedDriver is the driver row, locked for this transaction.
type lockedDriver struct {
	ID, Name, Phone, Status, RideType string
	Version                           int
}

func lockDriver(ctx context.Context, tx pgx.Tx, id string) (lockedDriver, error) {
	var d lockedDriver
	err := tx.QueryRow(ctx, "select id, name, coalesce(phone, ''), status, ride_type, version from drivers where id = $1 for update", id).
		Scan(&d.ID, &d.Name, &d.Phone, &d.Status, &d.RideType, &d.Version)
	return d, err
}

// bumpDriver records a change to the driver: version + 1, only if the
// status and version are still what this transaction read. Zero rows
// means someone else got there first: 409 with the driver as it is.
func (a *App) bumpDriver(ctx context.Context, tx pgx.Tx, d lockedDriver) error {
	tag, err := tx.Exec(ctx, "update drivers set version = version + 1 where id = $1 and status = $2 and version = $3",
		d.ID, d.Status, d.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return a.driverConflict(ctx, tx, d.ID, "wrong_status", "Your application changed meanwhile. Try again.")
	}
	return nil
}

// setDriverStatus moves the driver from the status this transaction read
// to [to] (with the extra assignments in [set], from $4 on) and writes the
// event. Zero rows changed ⇒ 409 wrong_status with the current state.
func (a *App) setDriverStatus(ctx context.Context, tx pgx.Tx, d lockedDriver, to, kind, actorType, actorID string,
	data map[string]any, set string, args ...any) error {
	sql := "update drivers set status = $2, version = version + 1"
	if set != "" {
		sql += ", " + set
	}
	sql += " where id = $1 and status = $3"
	tag, err := tx.Exec(ctx, sql, append([]any{d.ID, to, d.Status}, args...)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return a.driverConflict(ctx, tx, d.ID, "wrong_status", "The application is no longer "+d.Status+".")
	}
	from := d.Status
	return a.driverEvent(ctx, tx, d.ID, kind, &from, &to, actorType, actorID, data)
}

func (a *App) driverEvent(ctx context.Context, q db.Q, driverID, kind string, from, to *string, actorType, actorID string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	_, err := q.Exec(ctx, `insert into driver_events (driver_id, kind, from_status, to_status, actor_type, actor_id, at, data)
		values ($1, $2, $3, $4, $5, $6, $7, $8)`, driverID, kind, from, to, actorType, actorID, a.now(), data)
	return err
}

// driverConflict is a 409 [code] carrying the driver as they are now.
func (a *App) driverConflict(ctx context.Context, q db.Q, id, code, message string) error {
	d, err := a.loadDriverState(ctx, q, id, nil)
	if err != nil {
		return err
	}
	return domain.NewApiError(409, code, message).WithState(d)
}

// itemEditable: everything is editable while signing up; with changes
// needed, only what the admin rejected.
func itemEditable(status, itemState string) bool {
	return status == domain.DriverSigningUp || (status == domain.DriverChangesNeeded && itemState == domain.ItemRejected)
}

func lockedMessage(status string) string {
	if status == domain.DriverChangesNeeded {
		return "Only the items marked Fix can be replaced."
	}
	return "Your details are locked while your application is " + strings.ReplaceAll(status, "_", " ") + "."
}

func (a *App) today() string { return domain.LocalDateString(a.now(), a.Config.UTCOffsetMinutes) }

func newDriverID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "drv_" + hex.EncodeToString(b)
}

func uniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && (constraint == "" || pe.ConstraintName == constraint)
}

func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func (a *App) signupCities() []map[string]string {
	return []map[string]string{{"id": a.Config.City, "name": domain.CityName(a.Config.City)}}
}

func validFileKind(r *http.Request) (string, error) {
	kind := r.PathValue("kind")
	if !domain.IsFileItem(kind) {
		var c domain.Check
		c.Fail("Expected one of "+strings.Join(domain.FileItemKinds, ", "), "kind")
		return "", c.Err()
	}
	return kind, nil
}

func driverDocsPath(driverID, kind string) string { return DocsBucket + "/" + driverID + "/" + kind }

// ---------------------------------------------------------------- routes

func (a *App) driverSignupRoutes(handle func(string, handler)) {
	handle("GET /v1/driver/signup-config", func(w http.ResponseWriter, r *http.Request) error {
		items := []map[string]string{}
		for _, k := range domain.DriverItemKinds {
			items = append(items, map[string]string{"kind": k, "label": domain.ItemLabel(k), "hint": domain.ItemHint(k)})
		}
		writeJSON(w, 200, map[string]any{"cities": a.signupCities(), "items": items})
		return nil
	})

	handle("POST /v1/driver/register", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		s, err := a.driverSession(r)
		if err != nil {
			return err
		}
		var body struct {
			Name, City    *string
			TermsAccepted *bool
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		name := strings.Join(strings.Fields(trimmed(body.Name)), " ")
		var c domain.Check
		c.If(utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 80, "Enter your name as on your licence (2 to 80 characters)", "name")
		c.If(body.City == nil || !slices.ContainsFunc(a.signupCities(), func(m map[string]string) bool { return m["id"] == *body.City }),
			"Choose one of the cities we drive in", "city")
		c.If(body.TermsAccepted == nil || !*body.TermsAccepted, "Agree to the driver terms to continue", "termsAccepted")
		if err := c.Err(); err != nil {
			return err
		}
		phone := *s.Phone
		if s.DriverID != nil {
			return a.driverConflict(ctx, a.DB, *s.DriverID, "already_registered", "You're already registered. Log in instead.")
		}
		var id string
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			var existing *string
			if err := tx.QueryRow(ctx, "select id from drivers where phone = $1", phone).Scan(&existing); err != nil && !noRows(err) {
				return err
			}
			if existing != nil {
				return a.driverConflict(ctx, tx, *existing, "already_registered", "This number is already registered as a driver. Log in instead.")
			}
			var rider bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from riders where phone = $1)", phone).Scan(&rider); err != nil {
				return err
			}
			if rider {
				return apiErr(409, "phone_in_use_as_rider", "This number already has a rider account. Use another number to drive.")
			}
			reference := ""
			for range 20 {
				ref := domain.NewReference()
				var taken bool
				if err := tx.QueryRow(ctx, "select exists (select 1 from drivers where reference = $1)", ref).Scan(&taken); err != nil {
					return err
				}
				if !taken {
					reference = ref
					break
				}
			}
			if reference == "" {
				return errors.New("no free driver reference")
			}
			id = newDriverID()
			now := a.now()
			if _, err := tx.Exec(ctx, `insert into drivers (id, city, name, phone, status, reference, terms_accepted_at, created_at,
				rating, base_lat, base_lng, service_radius_km, work_start, work_end)
				values ($1, $2, $3, $4, 'signing_up', $5, $6, $6, 5.0, $7, $8, $9, 0, 1440)`,
				id, *body.City, name, phone, reference, now, cityCentreLat, cityCentreLng, signupServiceRadiusKm); err != nil {
				return err
			}
			// Every session of this phone now belongs to the driver.
			_, err := tx.Exec(ctx, "update sessions set driver_id = $1 where role = 'driver' and phone = $2", id, phone)
			return err
		})
		if uniqueViolation(err, "drivers_phone_key") {
			return apiErr(409, "already_registered", "This number is already registered as a driver. Log in instead.")
		}
		if err != nil {
			return err
		}
		d, err := a.loadDriverState(ctx, a.DB, id, nil)
		if err != nil {
			return err
		}
		writeJSON(w, 201, d)
		return nil
	})

	handle("GET /v1/driver/me", func(w http.ResponseWriter, r *http.Request) error {
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		d, err := a.loadDriverState(r.Context(), a.DB, id, nil)
		if noRows(err) {
			return errNotRegistered()
		}
		if err != nil {
			return err
		}
		writeJSON(w, 200, d)
		return nil
	})

	handle("POST /v1/driver/items/{kind}/upload-url", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		kind, err := validFileKind(r)
		if err != nil {
			return err
		}
		var body struct{ ContentType *string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.If(body.ContentType == nil || !slices.Contains(UploadContentTypes, *body.ContentType), "Photos must be image/jpeg or image/png", "contentType")
		if err := c.Err(); err != nil {
			return err
		}
		d, err := a.loadDriverState(ctx, a.DB, id, nil)
		if err != nil {
			return err
		}
		if !itemEditable(d.Status, d.itemState(kind)) {
			return domain.NewApiError(409, "locked", lockedMessage(d.Status)).WithState(d)
		}
		path := driverDocsPath(id, kind)
		u, err := a.storage().SignUpload(ctx, path, *body.ContentType, signedURLTTL)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{
			"uploadUrl": absoluteURL(r, u),
			"method":    "PUT",
			"headers":   map[string]string{"content-type": *body.ContentType},
			"path":      path,
			"expiresAt": iso(a.now().Add(signedURLTTL)),
		})
		return nil
	})

	handle("POST /v1/driver/items/{kind}/uploaded", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		kind, err := validFileKind(r)
		if err != nil {
			return err
		}
		var body struct{ Path *string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.If(body.Path == nil || *body.Path == "", "Required", "path")
		if err := c.Err(); err != nil {
			return err
		}
		path := *body.Path
		if !strings.HasPrefix(path, DocsBucket+"/"+id+"/") || strings.Contains(path, "..") || strings.Contains(path, "//") {
			return apiErr(403, "not_your_path", "That file isn't yours.")
		}
		before, err := a.loadDriverState(ctx, a.DB, id, nil)
		if err != nil {
			return err
		}
		if !itemEditable(before.Status, before.itemState(kind)) {
			return domain.NewApiError(409, "locked", lockedMessage(before.Status)).WithState(before)
		}
		ok, err := a.storage().Exists(ctx, path)
		if err != nil {
			return err
		}
		if !ok {
			return apiErr(422, "not_uploaded", "The upload didn't arrive. Try again.")
		}
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if err != nil {
				return err
			}
			var state string
			err = tx.QueryRow(ctx, "select state from driver_items where driver_id = $1 and kind = $2", id, kind).Scan(&state)
			if noRows(err) {
				state, err = domain.ItemMissing, nil
			}
			if err != nil {
				return err
			}
			if !itemEditable(d.Status, state) {
				return a.driverConflict(ctx, tx, id, "locked", lockedMessage(d.Status))
			}
			if _, err := tx.Exec(ctx, `insert into driver_items (driver_id, kind, storage_path, state, created_at, updated_at)
				values ($1, $2, $3, 'uploaded', $4, $4)
				on conflict (driver_id, kind) do update set storage_path = excluded.storage_path, state = 'uploaded',
				  reject_reason = null, reviewed_at = null, updated_at = excluded.updated_at`, id, kind, path, a.now()); err != nil {
				return err
			}
			if err := a.bumpDriver(ctx, tx, d); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"item": out.item(kind), "driver": out})
		return nil
	})

	handle("PUT /v1/driver/upi", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		var body struct{ UpiID *string }
		if err := decode(r, &body); err != nil {
			return err
		}
		upi := trimmed(body.UpiID)
		if !domain.ValidUPI(upi) {
			return apiErr(422, "bad_upi", "Enter a UPI ID like name@bank")
		}
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if err != nil {
				return err
			}
			var state string
			err = tx.QueryRow(ctx, "select state from driver_items where driver_id = $1 and kind = 'upi_id'", id).Scan(&state)
			if noRows(err) {
				state, err = domain.ItemMissing, nil
			}
			if err != nil {
				return err
			}
			if !itemEditable(d.Status, state) {
				return a.driverConflict(ctx, tx, id, "locked", lockedMessage(d.Status))
			}
			if _, err := tx.Exec(ctx, `insert into driver_items (driver_id, kind, storage_path, state, created_at, updated_at)
				values ($1, 'upi_id', null, 'uploaded', $2, $2)
				on conflict (driver_id, kind) do update set state = 'uploaded', reject_reason = null, reviewed_at = null,
				  updated_at = excluded.updated_at`, id, a.now()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "update drivers set upi_id = $2 where id = $1", id, upi); err != nil {
				return err
			}
			if err := a.bumpDriver(ctx, tx, d); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"item": out.item("upi_id"), "driver": out})
		return nil
	})

	handle("PUT /v1/driver/vehicle", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		var body struct {
			Type, CabClass, MakeModel, Plate, Colour *string
			Year                                     *int
			AC                                       *bool `json:"ac"`
			CngOrElectric                            *bool
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		thisYear := a.now().Add(time.Duration(a.Config.UTCOffsetMinutes) * time.Minute).UTC().Year()
		makeModel, colour := trimmed(body.MakeModel), trimmed(body.Colour)
		var c domain.Check
		c.If(body.Type == nil || !slices.Contains(domain.VehicleTypes, *body.Type), "Expected one of bike, auto, cab", "type")
		isCab := body.Type != nil && *body.Type == "cab"
		c.If(isCab && (body.CabClass == nil || !slices.Contains(domain.CabClasses, *body.CabClass)),
			"Choose hatchback, sedan or SUV", "cabClass")
		c.If(!isCab && body.CabClass != nil, "Only cabs have a class", "cabClass")
		c.If(makeModel == "" || utf8.RuneCountInString(makeModel) > 60, "Enter the make and model", "makeModel")
		c.If(body.Year == nil || *body.Year < 1990 || *body.Year > thisYear+1, "Enter a year between 1990 and next year", "year")
		c.If(body.Plate == nil || !domain.ValidPlate(*body.Plate), "Enter the number plate, like KA 05 AB 1234", "plate")
		c.If(colour == "" || utf8.RuneCountInString(colour) > 30, "Enter the colour", "colour")
		if err := c.Err(); err != nil {
			return err
		}
		var cabClass *string
		if isCab {
			cabClass = body.CabClass
		}
		ac := isCab && body.AC != nil && *body.AC
		cng := body.CngOrElectric != nil && *body.CngOrElectric
		plate, plateKey := domain.NormalizePlate(*body.Plate), domain.PlateKey(*body.Plate)
		rideType := domain.RideTypeFor(*body.Type, cabClass)

		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if err != nil {
				return err
			}
			state := domain.ItemMissing
			err = tx.QueryRow(ctx, "select review_state from vehicles where driver_id = $1", id).Scan(&state)
			if err != nil && !noRows(err) {
				return err
			}
			if !itemEditable(d.Status, state) {
				return a.driverConflict(ctx, tx, id, "locked", lockedMessage(d.Status))
			}
			var taken bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from vehicles where plate_key = $1 and driver_id <> $2)
				or exists (select 1 from drivers where id <> $2 and plate <> '' and upper(regexp_replace(plate, '[\s-]', '', 'g')) = $1)`,
				plateKey, id).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return a.driverConflict(ctx, tx, id, "plate_in_use", plateInUseMessage)
			}
			now := a.now()
			if _, err := tx.Exec(ctx, `insert into vehicles (driver_id, type, cab_class, make_model, year, plate, plate_key, colour, ac,
				cng_or_electric, review_state, created_at, updated_at)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'uploaded', $11, $11)
				on conflict (driver_id) do update set type = excluded.type, cab_class = excluded.cab_class,
				  make_model = excluded.make_model, year = excluded.year, plate = excluded.plate, plate_key = excluded.plate_key,
				  colour = excluded.colour, ac = excluded.ac, cng_or_electric = excluded.cng_or_electric,
				  review_state = 'uploaded', reject_reason = null, reviewed_at = null, updated_at = excluded.updated_at`,
				id, *body.Type, cabClass, makeModel, *body.Year, plate, plateKey, colour, ac, cng, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "update drivers set ride_type = $2, vehicle = $3, plate = $4, has_ac = $5 where id = $1",
				id, rideType, domain.VehicleText(colour, makeModel, ac), plate, ac); err != nil {
				return err
			}
			if err := a.bumpDriver(ctx, tx, d); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if uniqueViolation(err, "vehicles_plate_key_key") {
			return a.driverConflict(ctx, a.DB, id, "plate_in_use", plateInUseMessage)
		}
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"vehicle": out.Vehicle, "driver": out})
		return nil
	})

	handle("PUT /v1/driver/day-off", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		var body map[string]json.RawMessage
		if err := decode(r, &body); err != nil {
			return err
		}
		raw, present := body["dayOff"]
		var dayOff *int
		var c domain.Check
		if !present {
			c.Fail("Required: a weekday 1 (Monday) to 7 (Sunday), or null for no fixed day off", "dayOff")
		} else if string(raw) != "null" {
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 1 || n > 7 {
				c.Fail("Weekdays run from 1 (Monday) to 7 (Sunday)", "dayOff")
			}
			dayOff = &n
		}
		if err := c.Err(); err != nil {
			return err
		}
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if err != nil {
				return err
			}
			if d.Status != domain.DriverSigningUp {
				return a.driverConflict(ctx, tx, id, "locked", "Your day off is set. Change it later from Take time off.")
			}
			if _, err := tx.Exec(ctx, "update drivers set day_off = $2, day_off_set = true where id = $1", id, dayOff); err != nil {
				return err
			}
			if err := a.bumpDriver(ctx, tx, d); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("POST /v1/driver/submit", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if err != nil {
				return err
			}
			if d.Status != domain.DriverSigningUp && d.Status != domain.DriverChangesNeeded {
				return a.driverConflict(ctx, tx, id, "wrong_status", "Your application is already "+strings.ReplaceAll(d.Status, "_", " ")+".")
			}
			now, err := a.loadDriverState(ctx, tx, id, nil)
			if err != nil {
				return err
			}
			if len(now.Missing) > 0 {
				labels := []string{}
				for _, k := range now.Missing {
					labels = append(labels, domain.ItemLabel(k))
				}
				e := domain.NewApiError(409, "incomplete", "Still to add: "+strings.Join(labels, ", ")+".").WithState(now)
				e.Missing = now.Missing
				return e
			}
			if err := a.setDriverStatus(ctx, tx, d, domain.DriverSubmitted, "submitted", "driver", id, nil,
				"submitted_at = $4", a.now()); err != nil {
				return err
			}
			if err := a.notifyAdmins(ctx, tx, "admin_driver_submitted", map[string]any{"name": d.Name, "driverId": id}); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("GET /v1/driver/plan", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		d, err := a.loadDriverState(ctx, a.DB, id, nil)
		if err != nil {
			return err
		}
		if !canPay(d.Status) {
			return domain.NewApiError(409, "not_approved", "Your plan opens once you're approved.").WithState(d)
		}
		if d.Plan == nil {
			return domain.NewApiError(409, "not_approved", "There's no plan for your vehicle yet. Contact support.").WithState(d)
		}
		amount, start, end := domain.AmountDueNow(d.Plan.MonthlyPrice, a.today())
		writeJSON(w, 200, map[string]any{
			"plan":          d.Plan,
			"amountDueNow":  amount,
			"periodStart":   start,
			"periodEnd":     end,
			"reference":     d.Reference,
			"payTo":         map[string]string{"upiId": a.Config.PlatformUPIID},
			"latestPayment": d.LatestPayment,
		})
		return nil
	})

	handle("POST /v1/driver/payments", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := a.registeredDriver(r)
		if err != nil {
			return err
		}
		var body struct{ PeriodStart *string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.If(body.PeriodStart == nil, "Required", "periodStart")
		if err := c.Err(); err != nil {
			return err
		}
		var payment subscriptionPaymentJSON
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if err != nil {
				return err
			}
			if !canPay(d.Status) {
				return a.driverConflict(ctx, tx, id, "not_approved", "You can pay once you're approved.")
			}
			plan, err := loadPlan(ctx, tx, d.RideType)
			if err != nil {
				return err
			}
			if plan == nil {
				return a.driverConflict(ctx, tx, id, "not_approved", "There's no plan for your vehicle yet. Contact support.")
			}
			amount, start, end := domain.AmountDueNow(plan.MonthlyPrice, a.today())
			if *body.PeriodStart != start {
				return apiErr(422, "wrong_period", "The period to pay for starts "+start+". Refresh and try again.")
			}
			existing, err := scanPayment(tx.QueryRow(ctx, `select `+paymentColumns+` from subscription_payments
				where driver_id = $1 and status in ('claimed', 'confirmed')
				  and (period_start = $2::text::date or (status = 'confirmed' and period_end >= $3::text::date))
				order by claimed_at desc limit 1`, id, start, end))
			if err == nil {
				return domain.NewApiError(409, "already_claimed", "You've already told us about this payment.").WithState(existing)
			}
			if !noRows(err) {
				return err
			}
			now := a.now()
			payment, err = scanPayment(tx.QueryRow(ctx, `insert into subscription_payments (driver_id, period_start, period_end, amount, status, claimed_at)
				values ($1, $2::text::date, $3::text::date, $4, 'claimed', $5) returning `+paymentColumns, id, start, end, amount, now))
			if err != nil {
				return err
			}
			if err := a.bumpDriver(ctx, tx, d); err != nil {
				return err
			}
			if err := a.driverEvent(ctx, tx, id, "payment_claimed", &d.Status, &d.Status, "driver", id,
				map[string]any{"paymentId": payment.ID, "amount": amount, "periodStart": start, "periodEnd": end, "paymentStatus": "claimed"}); err != nil {
				return err
			}
			if err := a.notifyAdmins(ctx, tx, "admin_payment_claimed",
				map[string]any{"name": d.Name, "amount": amount, "driverId": id, "paymentId": payment.ID}); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if uniqueViolation(err, "subscription_payments_one_open") {
			existing, err := scanPayment(a.DB.QueryRow(ctx, `select `+paymentColumns+` from subscription_payments
				where driver_id = $1 and period_start = $2::text::date and status in ('claimed', 'confirmed')`, id, *body.PeriodStart))
			if err != nil {
				return err
			}
			return domain.NewApiError(409, "already_claimed", "You've already told us about this payment.").WithState(existing)
		}
		if err != nil {
			return err
		}
		writeJSON(w, 201, map[string]any{"payment": payment, "driver": out})
		return nil
	})
}

const plateInUseMessage = "This number plate is registered to another driver. If it's your vehicle, contact support."

// canPay: the plan and payments open once approved (and stay open live or paused, for D5).
func canPay(status string) bool {
	return status == domain.DriverApproved || status == domain.DriverLive || status == domain.DriverPaused
}
