package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ChunkSize is the chunk size of the postgres driver.
const ChunkSize = 1 << 20

// Postgres stores objects in storage_objects and storage_chunks (D-11). It is the default driver.
type Postgres struct {
	Pool *pgxpool.Pool
}

// Driver returns "postgres".
func (p *Postgres) Driver() string { return "postgres" }

// Close does nothing. The pool belongs to the caller.
func (p *Postgres) Close() error { return nil }

// Put streams r into 1 MiB chunks in one transaction.
func (p *Postgres) Put(ctx context.Context, key string, r io.Reader, _ string) (int64, error) {
	if err := ValidKey(key); err != nil {
		return 0, err
	}
	var total int64
	err := pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO storage_objects (key, size, created_at) VALUES ($1, 0, now())
			ON CONFLICT (key) DO UPDATE SET size = 0, created_at = now()`, key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM storage_chunks WHERE key = $1", key); err != nil {
			return err
		}
		buf := make([]byte, ChunkSize)
		for idx := 0; ; idx++ {
			n, rerr := io.ReadFull(r, buf)
			if n > 0 {
				if _, err := tx.Exec(ctx, "INSERT INTO storage_chunks (key, idx, data) VALUES ($1, $2, $3)", key, idx, buf[:n]); err != nil {
					return err
				}
				total += int64(n)
			}
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				break
			}
			if rerr != nil {
				return rerr
			}
		}
		_, err := tx.Exec(ctx, "UPDATE storage_objects SET size = $2 WHERE key = $1", key, total)
		return err
	})
	return total, err
}

// Get returns a reader that fetches one chunk at a time.
func (p *Postgres) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	info, err := p.Stat(ctx, key)
	if err != nil {
		return nil, err
	}
	return &pgReader{ctx: ctx, pool: p.Pool, key: key, size: info.Size}, nil
}

type pgReader struct {
	ctx  context.Context
	pool *pgxpool.Pool
	key  string
	size int64
	idx  int
	read int64
	cur  []byte
}

func (r *pgReader) Read(b []byte) (int, error) {
	for len(r.cur) == 0 {
		if r.read >= r.size {
			return 0, io.EOF
		}
		var data []byte
		err := r.pool.QueryRow(r.ctx, "SELECT data FROM storage_chunks WHERE key = $1 AND idx = $2", r.key, r.idx).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, io.ErrUnexpectedEOF
		}
		if err != nil {
			return 0, err
		}
		r.idx++
		r.cur = data
	}
	n := copy(b, r.cur)
	r.cur = r.cur[n:]
	r.read += int64(n)
	return n, nil
}

func (r *pgReader) Close() error {
	r.cur = nil
	return nil
}

// Stat returns the object size.
func (p *Postgres) Stat(ctx context.Context, key string) (Info, error) {
	var info Info
	err := p.Pool.QueryRow(ctx, "SELECT key, size, created_at FROM storage_objects WHERE key = $1", key).Scan(&info.Key, &info.Size, &info.ModTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return Info{}, ErrNotFound
	}
	return info, err
}

// Delete removes the object and its chunks.
func (p *Postgres) Delete(ctx context.Context, key string) error {
	return pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM storage_chunks WHERE key = $1", key); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM storage_objects WHERE key = $1", key)
		return err
	})
}

// List pages through keys with the prefix in key order.
func (p *Postgres) List(ctx context.Context, prefix string, fn func(Info) error) error {
	after := ""
	for {
		rows, err := p.Pool.Query(ctx, `SELECT key, size, created_at FROM storage_objects
			WHERE starts_with(key, $1) AND key > $2 ORDER BY key LIMIT 1000`, prefix, after)
		if err != nil {
			return err
		}
		var page []Info
		for rows.Next() {
			var i Info
			var t time.Time
			if err := rows.Scan(&i.Key, &i.Size, &t); err != nil {
				rows.Close()
				return err
			}
			i.ModTime = t
			page = append(page, i)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, i := range page {
			if err := fn(i); err != nil {
				return err
			}
		}
		if len(page) < 1000 {
			return nil
		}
		after = page[len(page)-1].Key
	}
}
