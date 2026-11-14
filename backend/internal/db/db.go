// Package db opens the Postgres pool, applies migrations and loads seed data.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Q is what both the pool and a transaction can do.
type Q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open connects to [url], or DATABASE_URL when it is empty.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	return pgxpool.NewWithConfig(ctx, cfg)
}

//go:embed migrations/*.sql
var migrations embed.FS

// migrateLock is the Postgres advisory lock key that lets one process
// migrate at a time: the API and the worker both migrate on start, and on a
// first deploy they start together.
const migrateLock = 7_200_312_001

// Migrate applies every migration in migrations/ that hasn't run yet, in
// order, each in its own transaction. Returns the files it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", migrateLock); err != nil {
		return nil, err
	}
	// Released with a fresh context, so a cancelled start still frees the lock.
	defer conn.Exec(context.Background(), "select pg_advisory_unlock($1)", migrateLock)
	return migrate(ctx, conn)
}

func migrate(ctx context.Context, conn *pgxpool.Conn) ([]string, error) {
	if _, err := conn.Exec(ctx, `create table if not exists schema_migrations (
		name text primary key, applied_at timestamptz not null default now())`); err != nil {
		return nil, err
	}
	done := map[string]bool{}
	rows, err := conn.Query(ctx, "select name from schema_migrations")
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		done[n] = true
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var applied []string
	for _, path := range files {
		name := strings.TrimPrefix(path, "migrations/")
		if done[name] {
			continue
		}
		sql, err := migrations.ReadFile(path)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			// No arguments: sent as a simple query, so a file can hold many statements.
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "insert into schema_migrations (name) values ($1)", name)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}
