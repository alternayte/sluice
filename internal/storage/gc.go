package storage

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/platform/clock"
)

// GC timings (REQ-STO-006).
const (
	GCInterval       = 24 * time.Hour
	BundleUnused     = 7 * 24 * time.Hour
	orphanGrace      = time.Hour
	gcBatch          = 100
	gcLastRunSetting = "maintenance.storage_gc_last_run"
)

// GC deletes unused storage objects. The maintenance leader runs it daily.
type GC struct {
	Pool  *pgxpool.Pool
	Store Store
	Clock clock.Clock
	Log   *slog.Logger
}

// GCResult counts deleted objects.
type GCResult struct {
	Bundles     int
	FileObjects int
	Orphans     int
	Logs        int
	Artifacts   int
}

// RunIfDue runs the GC when the last run is older than GCInterval.
func (g *GC) RunIfDue(ctx context.Context) (*GCResult, error) {
	now := g.Clock.Now()
	var last *time.Time
	err := g.Pool.QueryRow(ctx, `SELECT (value #>> '{}')::timestamptz FROM settings WHERE key = $1`, gcLastRunSetting).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if last != nil && now.Sub(*last) < GCInterval {
		return nil, nil
	}
	res, err := g.Run(ctx)
	if err != nil {
		return res, err
	}
	_, err = g.Pool.Exec(ctx, `INSERT INTO settings (key, value, updated_by, updated_at) VALUES ($1, to_jsonb($2::text), 'system', $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, gcLastRunSetting, now.Format(time.RFC3339Nano), now)
	return res, err
}

// Run deletes bundles unused for 7 days, file objects without a snapshot reference,
// orphaned file objects, and logs and artifacts of purged executions.
func (g *GC) Run(ctx context.Context) (*GCResult, error) {
	res := &GCResult{}
	now := g.Clock.Now()
	var err error
	if res.Bundles, err = g.bundles(ctx, now); err != nil {
		return res, err
	}
	if res.FileObjects, err = g.fileObjects(ctx, now); err != nil {
		return res, err
	}
	if res.Orphans, err = g.orphanFiles(ctx, now); err != nil {
		return res, err
	}
	if res.Logs, err = g.purgedExecutionObjects(ctx, now, "logs/"); err != nil {
		return res, err
	}
	if res.Artifacts, err = g.purgedExecutionObjects(ctx, now, "artifacts/"); err != nil {
		return res, err
	}
	if g.Log != nil {
		g.Log.Info("storage gc done", "bundles", res.Bundles, "file_objects", res.FileObjects, "orphans", res.Orphans, "logs", res.Logs, "artifacts", res.Artifacts)
	}
	return res, nil
}

func (g *GC) bundles(ctx context.Context, now time.Time) (int, error) {
	total := 0
	for {
		n := 0
		err := pgx.BeginFunc(ctx, g.Pool, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT manifest_hash, storage_key FROM bundles WHERE last_used_at < $1
				ORDER BY manifest_hash LIMIT $2 FOR UPDATE SKIP LOCKED`, now.Add(-BundleUnused), gcBatch)
			if err != nil {
				return err
			}
			type row struct{ hash, key string }
			var list []row
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.hash, &r.key); err != nil {
					rows.Close()
					return err
				}
				list = append(list, r)
			}
			rows.Close()
			for _, r := range list {
				if err := g.Store.Delete(ctx, r.key); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, "DELETE FROM bundles WHERE manifest_hash = $1", r.hash); err != nil {
					return err
				}
			}
			n = len(list)
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < gcBatch {
			return total, nil
		}
	}
}

// fileObjects deletes file objects that no snapshot references. The row lock keeps a
// concurrent save (which takes FOR SHARE on the row) from referencing a deleted object.
func (g *GC) fileObjects(ctx context.Context, now time.Time) (int, error) {
	total := 0
	for {
		n := 0
		err := pgx.BeginFunc(ctx, g.Pool, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT f.hash FROM file_objects f
				WHERE f.created_at < $1 AND NOT EXISTS (SELECT 1 FROM snapshot_files s WHERE s.hash = f.hash)
				ORDER BY f.hash LIMIT $2 FOR UPDATE SKIP LOCKED`, now.Add(-orphanGrace), gcBatch)
			if err != nil {
				return err
			}
			var hashes []string
			for rows.Next() {
				var h string
				if err := rows.Scan(&h); err != nil {
					rows.Close()
					return err
				}
				hashes = append(hashes, h)
			}
			rows.Close()
			for _, h := range hashes {
				tag, err := tx.Exec(ctx, `DELETE FROM file_objects f WHERE f.hash = $1
					AND NOT EXISTS (SELECT 1 FROM snapshot_files s WHERE s.hash = f.hash)`, h)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 1 {
					if err := g.Store.Delete(ctx, FileKey(h)); err != nil {
						return err
					}
					n++
				}
			}
			if len(hashes) < gcBatch {
				n = -n - 1 // mark the last batch
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		if n < 0 {
			return total + (-n - 1), nil
		}
		total += n
	}
}

// orphanFiles deletes stored file objects that have no file_objects row, for example
// after a crash between upload and insert.
func (g *GC) orphanFiles(ctx context.Context, now time.Time) (int, error) {
	var candidates []string
	err := g.Store.List(ctx, "files/sha256/", func(i Info) error {
		if now.Sub(i.ModTime) > orphanGrace {
			candidates = append(candidates, strings.TrimPrefix(i.Key, "files/sha256/"))
		}
		return nil
	})
	if err != nil || len(candidates) == 0 {
		return 0, err
	}
	n := 0
	for start := 0; start < len(candidates); start += 1000 {
		batch := candidates[start:min(start+1000, len(candidates))]
		rows, err := g.Pool.Query(ctx, "SELECT hash FROM file_objects WHERE hash = ANY($1::text[])", batch)
		if err != nil {
			return n, err
		}
		known := map[string]bool{}
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err == nil {
				known[h] = true
			}
		}
		rows.Close()
		for _, h := range batch {
			if !known[h] {
				if err := g.Store.Delete(ctx, FileKey(h)); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, nil
}

// purgedExecutionObjects deletes objects under prefix/<execution_id>/ when the execution no longer exists.
func (g *GC) purgedExecutionObjects(ctx context.Context, now time.Time, prefix string) (int, error) {
	byExec := map[uuid.UUID][]string{}
	err := g.Store.List(ctx, prefix, func(i Info) error {
		if now.Sub(i.ModTime) <= orphanGrace {
			return nil
		}
		rest := strings.TrimPrefix(i.Key, prefix)
		idStr, _, ok := strings.Cut(rest, "/")
		if !ok {
			return nil
		}
		if id, perr := uuid.Parse(idStr); perr == nil {
			byExec[id] = append(byExec[id], i.Key)
		}
		return nil
	})
	if err != nil || len(byExec) == 0 {
		return 0, err
	}
	ids := make([]uuid.UUID, 0, len(byExec))
	for id := range byExec {
		ids = append(ids, id)
	}
	n := 0
	for start := 0; start < len(ids); start += 1000 {
		batch := ids[start:min(start+1000, len(ids))]
		strs := make([]string, len(batch))
		for i, id := range batch {
			strs[i] = id.String()
		}
		rows, err := g.Pool.Query(ctx, "SELECT id FROM executions WHERE id = ANY($1::uuid[])", strs)
		if err != nil {
			return n, err
		}
		exists := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err == nil {
				exists[id] = true
			}
		}
		rows.Close()
		for _, id := range batch {
			if exists[id] {
				continue
			}
			for _, k := range byExec[id] {
				if err := g.Store.Delete(ctx, k); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, nil
}
