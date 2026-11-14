package db

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// A first deploy starts the API and the worker together on an empty
// database; both migrate. The advisory lock must make that safe.
func TestTwoProcessesMigrateAnEmptyDatabaseTogether(t *testing.T) {
	ctx := context.Background()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		base = "postgres://app:app@localhost:5432/commute_test"
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	const name = "commute_migrate_race"
	admin.Exec(ctx, "drop database if exists "+name+" with (force)")
	if _, err := admin.Exec(ctx, "create database "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "drop database if exists "+name+" with (force)")

	url := base[:strings.LastIndex(base, "/")+1] + name
	var wg sync.WaitGroup
	applied := make([][]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool, err := Open(ctx, url)
			if err != nil {
				errs[i] = err
				return
			}
			defer pool.Close()
			applied[i], errs[i] = Migrate(ctx, pool)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("process %d: %v", i, err)
		}
	}
	// One of them applied everything; the other found nothing left to do.
	total := len(applied[0]) + len(applied[1])
	files, _ := migrations.ReadDir("migrations")
	if total != len(files) || (len(applied[0]) != 0 && len(applied[1]) != 0) {
		t.Fatalf("applied %v and %v; want all %d by one process", applied[0], applied[1], len(files))
	}
}
