// Package pgtest starts Postgres (and PgBouncer) containers for integration tests (D-18).
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alternayte/sluice/internal/platform/db"
)

// Image is the Postgres image used by tests.
const Image = "postgres:17-alpine"

const (
	user     = "sluice"
	password = "sluice"
)

var (
	once     sync.Once
	shared   *Server
	sharedEr error
)

// Server is one Postgres container.
type Server struct {
	Container *tcpostgres.PostgresContainer
	// AdminURL connects to the postgres database as superuser.
	AdminURL string
	Network  *testcontainers.DockerNetwork
	Alias    string
}

// Shared returns a Postgres container shared by all tests of the process.
// Ryuk removes it when the process ends.
func Shared(t testing.TB) *Server {
	t.Helper()
	once.Do(func() { shared, sharedEr = start(context.Background()) })
	if sharedEr != nil {
		t.Fatalf("start postgres: %v", sharedEr)
	}
	return shared
}

func start(ctx context.Context) (*Server, error) {
	nw, err := network.New(ctx)
	if err != nil {
		return nil, err
	}
	alias := "pg-" + uuid.NewString()[:8]
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		network.WithNetwork([]string{alias}, nw),
		testcontainers.WithCmd("postgres", "-c", "max_connections=500", "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		return nil, err
	}
	u, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}
	return &Server{Container: c, AdminURL: u, Network: nw, Alias: alias}, nil
}

// NewDatabase creates an empty database and returns its URL. It is dropped at test end.
func (s *Server) NewDatabase(t testing.TB) string {
	t.Helper()
	ctx := context.Background()
	name := "t_" + uuid.NewString()[:8]
	conn, err := pgx.Connect(ctx, s.AdminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(s.AdminURL)
	u.Path = "/" + name
	t.Cleanup(func() {
		if os.Getenv("SLUICE_TEST_KEEP_DB") != "" {
			return
		}
		c, err := pgx.Connect(context.Background(), s.AdminURL)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	return u.String()
}

// InternalURL returns the URL of database name as seen from the Docker network.
func (s *Server) InternalURL(dbURL string) string {
	u, _ := url.Parse(dbURL)
	u.Host = s.Alias + ":5432"
	return u.String()
}

// NewPool creates a database, applies migrations and returns a pool.
func (s *Server) NewPool(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	u := s.NewDatabase(t)
	pool, err := db.Open(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool, u
}

// PgBouncer starts PgBouncer in transaction mode in front of database dbURL and
// returns the pooled URL (D-18 substitute for Neon and pooled Postgres).
func (s *Server) PgBouncer(t testing.TB, dbURL string) string {
	t.Helper()
	ctx := context.Background()
	u, _ := url.Parse(dbURL)
	dbName := u.Path[1:]
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "edoburu/pgbouncer:latest",
			ExposedPorts: []string{"5432/tcp"},
			Networks:     []string{s.Network.Name},
			Env: map[string]string{
				"DB_HOST":                 s.Alias,
				"DB_PORT":                 "5432",
				"DB_USER":                 user,
				"DB_PASSWORD":             password,
				"DB_NAME":                 dbName,
				"POOL_MODE":               "transaction",
				"AUTH_TYPE":               "scram-sha-256",
				"MAX_CLIENT_CONN":         "500",
				"DEFAULT_POOL_SIZE":       "20",
				"MAX_PREPARED_STATEMENTS": "0",
				"SERVER_RESET_QUERY":      "",
				"LISTEN_PORT":             "5432",
			},
			WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start pgbouncer: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, host, port.Port(), dbName)
}
