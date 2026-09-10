//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/sluice/internal/storage"
	"github.com/alternayte/sluice/internal/testutil/pgtest"
	"github.com/alternayte/sluice/internal/testutil/storetest"
)

// patternReader produces n pseudo-random bytes without holding them in memory.
type patternReader struct {
	n, off int64
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.off >= p.n {
		return 0, io.EOF
	}
	if rem := p.n - p.off; int64(len(b)) > rem {
		b = b[:rem]
	}
	for i := range b {
		x := uint64(p.off + int64(i))
		b[i] = byte((x*2654435761 + x>>7) >> 3)
	}
	p.off += int64(len(b))
	return len(b), nil
}

func hashOf(r io.Reader) ([]byte, int64) {
	h := sha256.New()
	n, _ := io.Copy(h, r)
	return h.Sum(nil), n
}

// heapWatch samples the heap and reports the peak growth over the baseline.
func heapWatch() (stop func() uint64) {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapInuse
	var peak atomic.Uint64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapInuse > base && m.HeapInuse-base > peak.Load() {
					peak.Store(m.HeapInuse - base)
				}
			}
		}
	}()
	return func() uint64 {
		close(done)
		wg.Wait()
		return peak.Load()
	}
}

func conformance(t *testing.T, s storage.Store) {
	ctx := context.Background()
	t.Run("put_get_stat", func(t *testing.T) {
		n, err := s.Put(ctx, "files/sha256/abc", strings.NewReader("hello"), "text/plain")
		if err != nil || n != 5 {
			t.Fatalf("put: %d %v", n, err)
		}
		r, err := s.Get(ctx, "files/sha256/abc")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		_ = r.Close()
		if string(b) != "hello" {
			t.Fatalf("get: %q", b)
		}
		info, err := s.Stat(ctx, "files/sha256/abc")
		if err != nil || info.Size != 5 {
			t.Fatalf("stat: %+v %v", info, err)
		}
	})
	t.Run("overwrite", func(t *testing.T) {
		if _, err := s.Put(ctx, "logs/e/t.ndjson.gz", strings.NewReader("first version"), ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, "logs/e/t.ndjson.gz", strings.NewReader("v2"), ""); err != nil {
			t.Fatal(err)
		}
		r, err := s.Get(ctx, "logs/e/t.ndjson.gz")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		_ = r.Close()
		if string(b) != "v2" {
			t.Fatalf("overwrite read %q", b)
		}
		if info, _ := s.Stat(ctx, "logs/e/t.ndjson.gz"); info.Size != 2 {
			t.Fatalf("overwrite size %d", info.Size)
		}
	})
	t.Run("missing_and_delete", func(t *testing.T) {
		if _, err := s.Get(ctx, "files/sha256/missing"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("get missing: %v", err)
		}
		if _, err := s.Stat(ctx, "files/sha256/missing"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stat missing: %v", err)
		}
		if err := s.Delete(ctx, "files/sha256/missing"); err != nil {
			t.Fatalf("delete missing: %v", err)
		}
		if err := s.Delete(ctx, "files/sha256/abc"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Stat(ctx, "files/sha256/abc"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stat after delete: %v", err)
		}
	})
	t.Run("prefix_list", func(t *testing.T) {
		for _, k := range []string{"artifacts/e1/t1/a", "artifacts/e1/t2/b", "artifacts/e2/t3/c", "bundles/m.tar.gz"} {
			if _, err := s.Put(ctx, k, strings.NewReader(k), ""); err != nil {
				t.Fatal(err)
			}
		}
		var got []string
		if err := s.List(ctx, "artifacts/e1/", func(i storage.Info) error {
			got = append(got, i.Key)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		if strings.Join(got, ",") != "artifacts/e1/t1/a,artifacts/e1/t2/b" {
			t.Fatalf("list: %v", got)
		}
	})
	t.Run("invalid_key", func(t *testing.T) {
		if _, err := s.Put(ctx, "../x", strings.NewReader("x"), ""); err == nil {
			t.Fatal("traversal key accepted")
		}
	})
	t.Run("stream_150MiB", func(t *testing.T) {
		const size = 150 << 20
		want, _ := hashOf(&patternReader{n: size})
		stop := heapWatch()
		n, err := s.Put(ctx, "bundles/big.tar.gz", &patternReader{n: size}, "application/gzip")
		if err != nil || n != size {
			stop()
			t.Fatalf("put big: %d %v", n, err)
		}
		r, err := s.Get(ctx, "bundles/big.tar.gz")
		if err != nil {
			stop()
			t.Fatal(err)
		}
		got, gn := hashOf(r)
		_ = r.Close()
		peak := stop()
		if gn != size || !bytes.Equal(got, want) {
			t.Fatalf("big object differs: %d bytes", gn)
		}
		if peak >= 64<<20 {
			t.Fatalf("heap growth %d MiB, limit 64 MiB", peak>>20)
		}
		t.Logf("peak heap growth %d MiB", peak>>20)
		_ = s.Delete(ctx, "bundles/big.tar.gz")
	})
}

func TestSCN_STO_001_Conformance(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		pool, _ := pgtest.Shared(t).NewPool(t)
		conformance(t, &storage.Postgres{Pool: pool})
	})
	t.Run("fs", func(t *testing.T) {
		s, err := storage.OpenFS(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		conformance(t, s)
	})
	t.Run("s3_minio", func(t *testing.T) {
		m := storetest.MinIO(t)
		s, err := storage.OpenS3(context.Background(), m.Config(t, "conf", ""))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		conformance(t, s)
	})
	t.Run("azblob_azurite", func(t *testing.T) {
		a := storetest.Azurite(t)
		s, err := storage.OpenAzblob(context.Background(), storage.AzblobConfig{ConnectionString: a.ConnectionString, Container: "conf", ClientOptions: a.ClientOptions()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		conformance(t, s)
	})
}

func TestSCN_STO_002_S3EndpointPathStylePrefix(t *testing.T) {
	m := storetest.MinIO(t)
	ctx := context.Background()
	s, err := storage.OpenS3(ctx, m.Config(t, "prefixed", "tenant-a/sluice"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, k := range []string{storage.FileKey("aa"), storage.BundleKey("mm"), storage.LogKey("e", "t"), storage.ArtifactKey("e", "t", "r.html")} {
		if _, err := s.Put(ctx, k, strings.NewReader("x"), ""); err != nil {
			t.Fatal(err)
		}
	}
	keys := m.RawKeys(t, "prefixed")
	if len(keys) != 4 {
		t.Fatalf("raw keys: %v", keys)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, "tenant-a/sluice/") {
			t.Fatalf("key %q does not start with the prefix", k)
		}
	}
	// Reads through the prefixed store use unprefixed keys.
	if _, err := s.Stat(ctx, storage.FileKey("aa")); err != nil {
		t.Fatal(err)
	}
}
