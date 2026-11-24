// Package seed loads config and test/demo data: the ride types and fare
// rules every environment needs, and the "spec" world from the R1+D2 doc's
// Integration tests section.
package seed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/offers"
	"commute-backend/internal/scheduling"
	"commute-backend/internal/travel"
)

// City is the only city until multi-city sign-up exists.
const City = "blr"

type rideType struct {
	Type, Label, Description string
	Seats                    int
	Speed                    float64
	Fare                     travel.FareRule
}

// RideTypes is the default ride-type config and Bengaluru's fares.
var RideTypes = []rideType{
	{"bike", "Bike", "1 rider · helmet provided", 1, 20, travel.FareRule{Base: 20, PerKm: 6, Minimum: 35}},
	{"auto", "Auto", "Up to 3 riders · open sides", 3, 18, travel.FareRule{Base: 30, PerKm: 11, Minimum: 40}},
	{"cab_hatchback", "Hatchback", "4 seats · AC", 4, 20, travel.FareRule{Base: 50, PerKm: 14, Minimum: 90}},
	{"cab_sedan", "Sedan", "4 seats · AC · laptop bag fits", 4, 20, travel.FareRule{Base: 60, PerKm: 16, Minimum: 110}},
	{"cab_suv", "SUV", "6 seats · AC · big boot", 6, 20, travel.FareRule{Base: 80, PerKm: 21, Minimum: 150}},
}

// Config adds the ride types, fare rules and settings that are missing;
// rows that exist are left alone, so ops changes survive a re-run.
func Config(ctx context.Context, pool *pgxpool.Pool) error {
	for i, t := range RideTypes {
		if _, err := pool.Exec(ctx, `insert into ride_type_config (ride_type, label, description, seats, speed_kmh, flows, cities, sort)
			values ($1, $2, $3, $4, $5, $6, $7, $8) on conflict do nothing`,
			t.Type, t.Label, t.Description, t.Seats, t.Speed, []string{offers.FlowRecurring, "one_time"}, []string{City}, i); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `insert into fare_rules (city, ride_type, base, per_km, minimum) values ($1, $2, $3, $4, $5)
			on conflict do nothing`, City, t.Type, t.Fare.Base, t.Fare.PerKm, t.Fare.Minimum); err != nil {
			return err
		}
	}
	for _, p := range Plans {
		if _, err := pool.Exec(ctx, `insert into plans (vehicle_type, cab_class, label, monthly_price)
			values ($1, nullif($2, ''), $3, $4) on conflict do nothing`, p.VehicleType, p.CabClass, p.Label, p.MonthlyPrice); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{
		"series_window_minutes": "15", "ride_window_minutes": "5", "max_rounds": "3", "horizon_days": "14",
		// D1: sign-up cities, and the UPI ID drivers pay the subscription to.
		"driver_cities": `[{"code": "blr", "name": "Bengaluru"}]`, "payee_upi": `"commuteride@icici"`,
	} {
		if _, err := pool.Exec(ctx, "insert into app_config (key, value) values ($1, $2::jsonb) on conflict do nothing", k, v); err != nil {
			return err
		}
	}
	return nil
}

// Plan is one subscription plan (D1): one per vehicle type and cab class.
type Plan struct {
	VehicleType, CabClass, Label string
	MonthlyPrice                 int
}

// Plans are the default monthly prices, in rupees.
var Plans = []Plan{
	{"bike", "", "Bike plan", 799},
	{"auto", "", "Auto plan", 1199},
	{"cab", "hatchback", "Hatchback plan", 1999},
	{"cab", "sedan", "Sedan plan", 2299},
	{"cab", "suv", "SUV plan", 2799},
}

// AdminPerson is the seeded admin account (signs in through /v1/auth/*).
var AdminPerson = Person{Name: "Admin", Phone: "9000000001", Role: "admin"}

// Admin adds the admin account; returns its id.
func Admin(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `insert into users (phone, role) values ($1, 'admin')
		on conflict (phone) do update set role = 'admin' returning id`, AdminPerson.Phone).Scan(&id)
	if err != nil {
		return id, err
	}
	_, err = pool.Exec(ctx, "insert into admins (user_id, name) values ($1, $2) on conflict do nothing", id, AdminPerson.Name)
	return id, err
}

// D1 loads the D1 Integration tests seed (after Config): rider Priya and the
// admin; no drivers (each test signs its drivers up). Returns ids by first name.
func D1(ctx context.Context, pool *pgxpool.Pool) (map[string]uuid.UUID, error) {
	ids := map[string]uuid.UUID{}
	p := SpecPeople[0]
	var id uuid.UUID
	if err := pool.QueryRow(ctx, "insert into users (phone, role) values ($1, 'rider') returning id", p.Phone).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, "insert into riders (user_id, name, rating) values ($1, $2, 4.8)", id, p.Name); err != nil {
		return nil, err
	}
	ids[offers.FirstName(p.Name)] = id
	admin, err := Admin(ctx, pool)
	if err != nil {
		return nil, err
	}
	ids[AdminPerson.Name] = admin
	return ids, nil
}

// Places used by the spec seed. Koramangala → Whitefield is 51 minutes by
// auto (straight line × 1.4 at 18 km/h, rounded up).
var (
	Koramangala = offers.Place{Lat: 12.9352, Lng: 77.6245, Label: "Koramangala"}
	Whitefield  = offers.Place{Lat: 12.9698, Lng: 77.7178, Label: "Whitefield"}
	Indiranagar = offers.Place{Lat: 12.9784, Lng: 77.6408, Label: "Indiranagar"}
	HSRLayout   = offers.Place{Lat: 12.9121, Lng: 77.6446, Label: "HSR Layout"}
)

// SpecNow is the spec seed's clock: Thu 19 Nov 2026 10:00 IST.
var SpecNow = time.Date(2026, 11, 19, 10, 0, 0, 0, time.FixedZone("IST", 330*60))

// Person is a seeded user.
type Person struct {
	Name, Phone, Role string
	// Drivers only.
	VehicleType, VehicleNumber, VehicleDesc string
}

// SpecPeople are the spec seed's users, signed in by first name in tests.
var SpecPeople = []Person{
	{Name: "Priya Sharma", Phone: "9000000101", Role: "rider"},
	{Name: "Ankit Jain", Phone: "9000000102", Role: "rider"},
	{Name: "Kavya Nair", Phone: "9000000103", Role: "rider"},
	{Name: "Ravi Kumar", Phone: "9000000201", Role: "driver", VehicleType: "auto", VehicleNumber: "KA 05 AB 1234", VehicleDesc: "Green auto · 3 seats"},
	{Name: "Meena Rao", Phone: "9000000202", Role: "driver", VehicleType: "auto", VehicleNumber: "KA 01 AC 4420", VehicleDesc: "Green auto · 3 seats"},
	{Name: "Anil Gowda", Phone: "9000000203", Role: "driver", VehicleType: "auto", VehicleNumber: "KA 03 AD 1189", VehicleDesc: "Green auto · 3 seats"},
	{Name: "Suresh M", Phone: "9000000204", Role: "driver", VehicleType: "auto", VehicleNumber: "KA 02 AB 7765", VehicleDesc: "Green auto · 3 seats"},
	{Name: "Vijay B", Phone: "9000000205", Role: "driver", VehicleType: "bike", VehicleNumber: "KA 51 EX 2210", VehicleDesc: "Black Splendor · helmet"},
}

// Spec loads the R1+D2 Integration tests seed into an empty database (after
// Config): rider Priya; live, paid auto drivers Ravi (drives Ankit weekdays
// 09:50 from Whitefield), Meena (a Wednesday 09:00 ride for Kavya, which
// clashes with Priya's 08:30 Koramangala → Whitefield), Anil and Suresh; a
// bike driver, Vijay. svc's clock should read SpecNow. Returns user ids by
// first name.
func Spec(ctx context.Context, pool *pgxpool.Pool, svc *offers.Service) (map[string]uuid.UUID, error) {
	ids := map[string]uuid.UUID{}
	paidUntil := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	for _, p := range SpecPeople {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, "insert into users (phone, role) values ($1, $2) returning id", p.Phone, p.Role).Scan(&id); err != nil {
			return nil, err
		}
		var err error
		if p.Role == "rider" {
			_, err = pool.Exec(ctx, "insert into riders (user_id, name, rating) values ($1, $2, 4.8)", id, p.Name)
		} else {
			// Live and paid, as if through D1: verified vehicle, day off "none".
			_, err = pool.Exec(ctx, `insert into drivers (user_id, name, city, vehicle_type, vehicle_number, vehicle_desc, status,
				subscription_until, rating, reference, day_off_set, upi_id, approved_at)
				values ($1, $2, $3, $4, $5, $6, 'live', $7, 4.8, $8, true, $9, now())`,
				id, p.Name, City, p.VehicleType, p.VehicleNumber, p.VehicleDesc, paidUntil,
				fmt.Sprintf("DRV-%05d", 10000+len(ids)), strings.ToLower(offers.FirstName(p.Name))+"@okaxis")
			if err == nil {
				_, err = pool.Exec(ctx, `insert into vehicles (driver_id, type, plate, colour, review_state)
					values ($1, $2, $3, $4, 'verified')`, id, p.VehicleType, p.VehicleNumber, "")
			}
		}
		if err != nil {
			return nil, err
		}
		ids[offers.FirstName(p.Name)] = id
	}
	// Ravi drives Ankit every weekday at 09:50 from Whitefield.
	if err := activeSeries(ctx, pool, svc, ids["Ankit"], ids["Ravi"], Whitefield, Indiranagar, []int{1, 2, 3, 4, 5}, "09:50", "2026-11-02"); err != nil {
		return nil, fmt.Errorf("Ankit's series: %w", err)
	}
	// Meena drives Kavya on Wednesdays at 09:00.
	if err := activeSeries(ctx, pool, svc, ids["Kavya"], ids["Meena"], HSRLayout, Koramangala, []int{3}, "09:00", "2026-11-04"); err != nil {
		return nil, fmt.Errorf("Kavya's series: %w", err)
	}
	return ids, nil
}

// activeSeries inserts an auto series that is already active with [driver]
// (as if picked earlier) and creates its rides.
func activeSeries(ctx context.Context, pool *pgxpool.Pool, svc *offers.Service, rider, driver uuid.UUID,
	from, to offers.Place, weekdays []int, at, start string) error {
	road := travel.RoadKm(travel.Point{Lat: from.Lat, Lng: from.Lng}, travel.Point{Lat: to.Lat, Lng: to.Lng})
	auto := RideTypes[1]
	requested := svc.Clock.Now().Add(-30 * 24 * time.Hour)
	var id uuid.UUID
	err := pool.QueryRow(ctx, `insert into series (rider_id, status, city, vehicle_type, pickup_lat, pickup_lng, pickup_label,
		drop_lat, drop_lng, drop_label, time_zone, start_date, distance_km, travel_minutes, suggested_fare, usual_payment, requested_at)
		values ($1, 'finding_driver', $2, 'auto', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'upi', $14) returning id`,
		rider, City, from.Lat, from.Lng, from.Label, to.Lat, to.Lng, to.Label, scheduling.DefaultTimeZone, start,
		travel.RoundKm(road), travel.Minutes(road, auto.Speed), travel.Fare(auto.Fare, road), requested).Scan(&id)
	if err != nil {
		return err
	}
	for _, w := range weekdays {
		if _, err := pool.Exec(ctx, `insert into series_slots (series_id, weekday, direction, timing, local_pickup_time)
			values ($1, $2, 'outbound', 'pickup_at', $3::time)`, id, w, at); err != nil {
			return err
		}
	}
	return svc.ActivateWithDriver(ctx, id, driver)
}
