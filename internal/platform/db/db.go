// Package db opens the Postgres pool and applies embedded migrations.
//
// All access must work through a transaction-mode pooler (C-06): the pool uses
// unnamed statements (QueryExecModeExec), and no session state is used.
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/db"
)

// DBTX is the query interface a generated per-feature Queries type accepts.
// A feature package passes a value of this type where it needs to share a
// database handle, such as a pool or a transaction, across a feature
// boundary. Each generated <feature>db.DBTX has the same method set, so a
// db.DBTX value satisfies it without a conversion.
type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

// Open creates a pool that is safe for PgBouncer transaction mode.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	cfg.ConnConfig.StatementCacheCapacity = 0
	cfg.ConnConfig.DescriptionCacheCapacity = 0
	if cfg.MaxConns < 10 {
		cfg.MaxConns = 10
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return pool, nil
}

// migrationLockKey is the key of the transaction-scoped advisory lock for migrations.
const migrationLockKey int64 = 0x736c7569636530 // "sluice0"

// Migration is one embedded goose-format migration.
type Migration struct {
	Version int64
	Name    string
	Up      string
}

// Migrations parses the embedded migrations in version order.
func Migrations() ([]Migration, error) {
	return parseMigrations(db.Migrations, "migrations")
}

func parseMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: name must be <version>_<name>.sql", e.Name())
		}
		v, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad version", e.Name())
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		up, err := upSection(string(b))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: v, Name: e.Name(), Up: up})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].Version)
		}
	}
	return out, nil
}

// upSection returns the SQL between "-- +goose Up" and "-- +goose Down".
func upSection(src string) (string, error) {
	var b strings.Builder
	state := 0 // 0 before Up, 1 in Up, 2 in Down
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "-- +goose") {
			ann := strings.TrimSpace(strings.TrimPrefix(trim, "-- +goose"))
			switch ann {
			case "Up":
				state = 1
			case "Down":
				state = 2
			case "StatementBegin", "StatementEnd":
			default:
				return "", fmt.Errorf("unsupported goose annotation %q", ann)
			}
			continue
		}
		if state == 1 {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if state == 0 {
		return "", errors.New("missing -- +goose Up")
	}
	return b.String(), nil
}

// Migrate applies all pending migrations in one transaction. A transaction-scoped
// advisory lock serializes concurrent starts, so each migration applies once (REQ-CORE-003).
func Migrate(ctx context.Context, pool *pgxpool.Pool) (applied []string, err error) {
	migs, err := Migrations()
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return nil, fmt.Errorf("migration lock: %w", err)
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	done := map[int64]bool{}
	rows, err := tx.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int64
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		done[v] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, m := range migs {
		if done[m.Version] {
			continue
		}
		if _, err = tx.Exec(ctx, m.Up, pgx.QueryExecModeSimpleProtocol); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				return nil, fmt.Errorf("apply %s: %s (line %d)", m.Name, pgErr.Message, pgErr.Line)
			}
			return nil, fmt.Errorf("apply %s: %w", m.Name, err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.Version, m.Name); err != nil {
			return nil, err
		}
		applied = append(applied, m.Name)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return applied, nil
}

// MigrationsCurrent reports whether the database has every embedded migration.
func MigrationsCurrent(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) error {
	migs, err := Migrations()
	if err != nil {
		return err
	}
	var n int
	var maxV *int64
	if err := q.QueryRow(ctx, "SELECT count(*), max(version) FROM schema_migrations").Scan(&n, &maxV); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	want := migs[len(migs)-1].Version
	if n != len(migs) || maxV == nil || *maxV != want {
		return fmt.Errorf("migrations not current: have %d, want %d", n, len(migs))
	}
	return nil
}
