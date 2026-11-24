// Database chores against DATABASE_URL:
//
//	db migrate        apply new migrations
//	db seed-config    add missing ride types, fare rules and settings; never deletes
//	db seed           wipe every table and load the spec world (dev only):
//	                  rider Priya, auto drivers Ravi, Meena, Anil, Suresh, bike driver Vijay, the admin
package main

import (
	"context"
	"fmt"
	"os"

	"commute-backend/internal/clock"
	"commute-backend/internal/offers"
	"commute-backend/internal/seed"
	"commute-backend/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: db migrate | seed-config | seed")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	switch args[0] {
	case "migrate":
		fmt.Println("Up to date")
	case "seed-config":
		if err := seed.Config(ctx, pool); err != nil {
			return err
		}
		fmt.Println("Config seeded")
	case "seed":
		if os.Getenv("APP_ENV") == "production" {
			return fmt.Errorf("seed wipes every table; use seed-config in production")
		}
		if _, err := pool.Exec(ctx, `truncate subscription_payments, driver_items, vehicles, plans, admins, sweeper_runs, notification_outbox, events, offer_recipients, offers, rides,
			series_slots, series, devices, sessions, otp_codes, drivers, riders, users, fare_rules, ride_type_config, app_config cascade`); err != nil {
			return err
		}
		if err := seed.Config(ctx, pool); err != nil {
			return err
		}
		svc := &offers.Service{DB: pool, Clock: clock.Real{}}
		if _, err := seed.Spec(ctx, pool, svc); err != nil {
			return err
		}
		if _, err := seed.Admin(ctx, pool); err != nil {
			return err
		}
		fmt.Println("Seeded: riders Priya (9000000101), Ankit, Kavya; drivers Ravi (9000000201), Meena, Anil, Suresh, Vijay (bike); admin (9000000001)")
	default:
		return fmt.Errorf("unknown command %q: use migrate, seed-config or seed", args[0])
	}
	return nil
}
