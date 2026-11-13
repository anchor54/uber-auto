// Database chores against DATABASE_URL:
//
//	db migrate                          apply new migrations
//	db seed                             wipe app data and load the demo city (dev and tests only)
//	db seed-config [--demo-drivers]     add missing fare rules, ride-type config and places
//	                                    (and the 15 demo drivers); never deletes
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"commute-backend/internal/app"
	"commute-backend/internal/db"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: db migrate | seed | seed-config [--demo-drivers]")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	switch args[0] {
	case "migrate":
		applied, err := db.Migrate(ctx, pool)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			fmt.Println("Up to date")
		} else {
			fmt.Println("Applied", applied)
		}
	case "seed":
		if app.IsProduction(os.Getenv) {
			return fmt.Errorf("seed wipes every table; use seed-config in production")
		}
		if err := db.SeedDemoData(ctx, pool); err != nil {
			return err
		}
		fmt.Println("Seeded demo city")
	case "seed-config":
		added, err := db.SeedConfig(ctx, pool, slices.Contains(args[1:], "--demo-drivers"))
		if err != nil {
			return err
		}
		b, _ := json.Marshal(added)
		fmt.Println("Added", string(b))
	default:
		return fmt.Errorf("unknown command %q: use migrate, seed or seed-config", args[0])
	}
	return nil
}
