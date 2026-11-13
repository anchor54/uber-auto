package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/domain"
)

const DemoCity = "blr"

// DemoPlaces are the same places as the rider app's demo backend
// (lib/core/fake_api.dart).
var DemoPlaces = []domain.Place{
	{ID: "home", Title: "Home", Detail: "14th Main, HSR Layout", Lat: 12.9121, Lng: 77.6446},
	{ID: "etv", Title: "Embassy Tech Village", Detail: "Gate 3", Lat: 12.9406, Lng: 77.6964},
	{ID: "manyata", Title: "Manyata Tech Park", Detail: "Hebbal", Lat: 13.0475, Lng: 77.6203},
	{ID: "itpl", Title: "ITPL", Detail: "Whitefield", Lat: 12.9866, Lng: 77.7378},
	{ID: "forum", Title: "Forum Mall", Detail: "Koramangala", Lat: 12.9346, Lng: 77.6113},
	{ID: "indiranagar", Title: "Indiranagar Metro", Detail: "100 Feet Road", Lat: 12.9784, Lng: 77.6408},
	{ID: "airport", Title: "Kempegowda Airport", Detail: "Terminal 2", Lat: 13.1989, Lng: 77.7068},
}

// DemoPlace is the demo place with [id].
func DemoPlace(id string) domain.Place {
	for _, p := range DemoPlaces {
		if p.ID == id {
			return p
		}
	}
	panic("no demo place " + id)
}

// DemoDriverPhone: demo drivers sign in as 9000000001, 9000000002, ... in
// the order of DemoDrivers.
func DemoDriverPhone(index int) string { return fmt.Sprintf("90000000%02d", index+1) }

type SeedDriver struct {
	ID, Name, RideType, Vehicle, Plate string
	Rating                             float64
	UPI                                string
	Lat, Lng                           float64
}

// DemoDrivers are the same drivers as the app's demo data, based around
// south-east Bengaluru.
var DemoDrivers = []SeedDriver{
	{"d_arjun", "Arjun Rao", "bike", "Black Splendor · helmet", "KA 51 EX 2210", 4.8, "arjun.rao@okhdfc", 12.914, 77.638},
	{"d_vinay", "Vinay S", "bike", "Red Pulsar · helmet", "KA 03 HY 7781", 4.7, "vinay.s@ybl", 12.925, 77.65},
	{"d_kiran", "Kiran Gowda", "bike", "Blue Activa · helmet", "KA 05 KP 9012", 4.6, "kiran.g@okaxis", 12.93, 77.62},
	{"d_manju", "Manjunath B", "auto", "Green auto · 3 seats", "KA 01 AC 4420", 4.8, "manju.b@ybl", 12.913, 77.641},
	{"d_raju", "Raju K", "auto", "Green auto · 3 seats", "KA 05 AD 1189", 4.7, "raju.k@okicici", 12.92, 77.655},
	{"d_salim", "Salim Pasha", "auto", "Green auto · 3 seats", "KA 02 AB 7765", 4.6, "salim.p@ybl", 12.935, 77.625},
	{"d_naveen", "Naveen P", "cab_hatchback", "Grey Swift · AC · 4 seats", "KA 03 MB 5521", 4.8, "naveen.p@okhdfc", 12.912, 77.646},
	{"d_sunil", "Sunil R", "cab_hatchback", "White WagonR · AC · 4 seats", "KA 04 MC 3390", 4.7, "sunil.r@ybl", 12.922, 77.66},
	{"d_mohan", "Mohan D", "cab_hatchback", "Red i20 · AC · 4 seats", "KA 51 MD 1102", 4.6, "mohan.d@okaxis", 12.94, 77.63},
	{"d_ravi", "Ravi Kumar", "cab_sedan", "White Dzire · AC · 4 seats", "KA 01 AB 1234", 4.9, "ravi.kumar@okbank", 12.9125, 77.6445},
	{"d_suresh", "Suresh M", "cab_sedan", "Silver Etios · AC · 4 seats", "KA 05 MN 4410", 4.8, "suresh.m@ybl", 12.92, 77.652},
	{"d_imran", "Imran K", "cab_sedan", "Silver Honda City · AC · 4 seats", "KA 05 MK 2290", 4.8, "imran.k@okaxis", 12.934, 77.62},
	{"d_prakash", "Prakash N", "cab_suv", "White Ertiga · AC · 6 seats", "KA 01 MP 8890", 4.8, "prakash.n@okhdfc", 12.913, 77.643},
	{"d_joseph", "Joseph A", "cab_suv", "Grey Innova · AC · 7 seats", "KA 02 MJ 3345", 4.7, "joseph.a@ybl", 12.926, 77.648},
	{"d_ganesh", "Ganesh T", "cab_suv", "White XL6 · AC · 6 seats", "KA 53 MG 6610", 4.6, "ganesh.t@okaxis", 12.938, 77.628},
}

// SeedDemoData wipes app data and loads the demo city. Used by tests and `db seed`.
func SeedDemoData(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := Migrate(ctx, pool); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `truncate job_runs, holidays, devices, notifications, ride_ratings, ride_track,
		driver_locations, ride_contacts, ride_events, rides, waitlist, match_proposals, commutes, drivers,
		ride_type_config, fare_rules, places, sessions, otp_codes, riders cascade`); err != nil {
		return err
	}
	_, err := insertConfig(ctx, pool, true, "")
	return err
}

// SeedConfig loads the city's setup into a live database without touching
// anything else: fare rules, the ride-type config and the demo places (and
// the demo drivers when asked). Rows that already exist are left alone, so
// ops changes survive a re-run. Returns how many rows each table gained.
func SeedConfig(ctx context.Context, pool *pgxpool.Pool, demoDrivers bool) (map[string]int64, error) {
	if _, err := Migrate(ctx, pool); err != nil {
		return nil, err
	}
	return insertConfig(ctx, pool, demoDrivers, " on conflict do nothing")
}

func insertConfig(ctx context.Context, pool *pgxpool.Pool, drivers bool, onConflict string) (map[string]int64, error) {
	added := map[string]int64{"fareRules": 0, "rideTypeConfig": 0, "places": 0, "drivers": 0}
	run := func(key, sql string, args ...any) error {
		tag, err := pool.Exec(ctx, sql+onConflict, args...)
		added[key] += tag.RowsAffected()
		return err
	}
	for _, typ := range domain.RideTypes {
		r := domain.DefaultFareRules[typ]
		if err := run("fareRules", `insert into fare_rules (city, ride_type, base, per_km, minimum) values ($1, $2, $3, $4, $5)`,
			DemoCity, typ, r.Base, r.PerKm, r.Minimum); err != nil {
			return added, err
		}
	}
	for _, flow := range domain.BookingFlows {
		def := "cab_sedan"
		if flow == "one_time" {
			def = "auto"
		}
		if err := run("rideTypeConfig", `insert into ride_type_config (city, flow, allowed, default_type) values ($1, $2, $3, $4)`,
			DemoCity, flow, domain.RideTypes, def); err != nil {
			return added, err
		}
	}
	for _, p := range DemoPlaces {
		if err := run("places", `insert into places (id, city, title, detail, lat, lng) values ($1, $2, $3, $4, $5, $6)`,
			p.ID, DemoCity, p.Title, p.Detail, p.Lat, p.Lng); err != nil {
			return added, err
		}
	}
	if drivers {
		for i, d := range DemoDrivers {
			if err := run("drivers", `insert into drivers (id, city, name, ride_type, vehicle, plate, rating, upi_id, has_ac,
				base_lat, base_lng, service_radius_km, work_start, work_end, phone)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 8, 420, 1290, $12)`,
				d.ID, DemoCity, d.Name, d.RideType, d.Vehicle, d.Plate, d.Rating, d.UPI,
				strings.HasPrefix(d.RideType, "cab_"), d.Lat, d.Lng, DemoDriverPhone(i)); err != nil {
				return added, err
			}
		}
	}
	return added, nil
}
