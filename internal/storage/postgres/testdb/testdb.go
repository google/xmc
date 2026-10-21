// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package testdb gives PostgreSQL repository tests isolated, real databases. It is the PostgreSQL
// counterpart of internal/storage/spanner/emulator: Setup creates a fresh pair of XID and ESS
// databases with the canonical schemas applied and drops both when the test completes.
//
// The databases live on a postgres:16-alpine container that testcontainers starts once per test
// process and its Ryuk reaper removes when the process exits, so Bazel test targets need
// tags = ["local"] for Docker access. To use an existing server instead, set POSTGRES_TEST_DSN
// (for example with bazel test --test_env) to a postgres:// URL whose user can create databases.
//
// All functions are safe for use by parallel tests.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "embed"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"xmanager_cloud/internal/storage/postgres/migrations/version"
)

// adminDSNEnv names the environment variable that points the harness at an existing PostgreSQL
// server, given as a postgres:// URL, instead of a container.
const adminDSNEnv = "POSTGRES_TEST_DSN"

// Settings of the container started when adminDSNEnv is unset.
const (
	postgresImage    = "postgres:16-alpine"
	postgresPort     = "5432/tcp"
	postgresUser     = "postgres"
	postgresPassword = "postgres"
	postgresAdminDB  = "postgres"
)

const (
	// serverStartTimeout bounds starting the container, including pulling its image, and
	// connecting to the admin database.
	serverStartTimeout = 2 * time.Minute
	// dropTimeout bounds dropping one test database during test cleanup.
	dropTimeout = 30 * time.Second
	// suffixBytes is the number of random bytes, hex-encoded, that make test database names unique
	// across tests and across processes sharing one server.
	suffixBytes = 8
	// maxPoolConns caps the connections of each pool so that parallel tests sharing one server stay
	// well below its default max_connections of 100.
	maxPoolConns = 10
)

const (
	// createMigrationsTableSQL creates the version table exactly as golang-migrate's postgres
	// driver does, so that migrations can run on top of the harness schema.
	createMigrationsTableSQL = "CREATE TABLE schema_migrations (version bigint not null primary key, dirty boolean not null)"
	// stampVersionSQL records a clean migration version, as golang-migrate does after a successful
	// migration.
	stampVersionSQL = "INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)"
)

var (
	//go:embed xid-schema.sql
	xidSchemaDDL string
	//go:embed ess-schema.sql
	essSchemaDDL string
)

// sharedServer returns the server shared by all tests in the process, starting it on first use.
// It also caches a startup error, so an unavailable Docker daemon fails every test immediately
// instead of each test retrying a slow container start.
var sharedServer = sync.OnceValues(startServer)

// TestPools holds the connection pools and DSNs of one test's isolated XID and ESS databases.
type TestPools struct {
	XIDPool *pgxpool.Pool
	ESSPool *pgxpool.Pool
	XIDDSN  string
	ESSDSN  string
}

// schema is the canonical DDL of one database and the migration version it corresponds to.
type schema struct {
	name    string
	ddl     string
	version int64
}

// server is a PostgreSQL server on which the harness creates and drops test databases.
type server struct {
	// adminURL locates the admin database. Test database DSNs differ from it only in the path.
	adminURL *url.URL
	// admin is connected to the admin database and runs CREATE DATABASE and DROP DATABASE.
	admin *pgxpool.Pool
}

// Setup creates isolated XID and ESS databases with the canonical schemas applied and returns
// their pools as (xidPool, essPool). Each schema_migrations table is stamped with the expected
// migration version, as if every migration had run. Both databases are dropped when the test
// completes.
func Setup(ctx context.Context, t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	pools := SetupWithPools(ctx, t)
	return pools.XIDPool, pools.ESSPool
}

// SetupWithPools is like Setup but also returns the DSNs of both databases, for code under test
// that opens its own connections.
func SetupWithPools(ctx context.Context, t *testing.T) TestPools {
	t.Helper()
	pools := SetupEmpty(ctx, t)
	applySchema(ctx, t, pools.XIDPool, schema{name: "xid", ddl: xidSchemaDDL, version: version.XidExpectedSchemaVersion()})
	applySchema(ctx, t, pools.ESSPool, schema{name: "ess", ddl: essSchemaDDL, version: version.EssExpectedSchemaVersion()})
	return pools
}

// SetupEmpty creates isolated XID and ESS databases without any tables, schema_migrations
// included, for tests that run migrations themselves. Both databases are dropped when the test
// completes.
func SetupEmpty(ctx context.Context, t *testing.T) TestPools {
	t.Helper()
	srv, err := sharedServer()
	if err != nil {
		t.Fatalf("failed to start postgres test server: %v", err)
	}
	suffix, err := randomSuffix()
	if err != nil {
		t.Fatalf("failed to generate test database suffix: %v", err)
	}
	xidPool, xidDSN := srv.newDatabase(ctx, t, "test_xid_db_"+suffix)
	essPool, essDSN := srv.newDatabase(ctx, t, "test_ess_db_"+suffix)
	return TestPools{XIDPool: xidPool, ESSPool: essPool, XIDDSN: xidDSN, ESSDSN: essDSN}
}

// startServer connects to the server named by POSTGRES_TEST_DSN or, when it is unset, to a newly
// started container. It runs once on behalf of all tests in the process, so it is bounded by its
// own timeout rather than by the context of whichever test triggered it.
func startServer() (*server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), serverStartTimeout)
	defer cancel()
	adminURL, err := resolveAdminURL(ctx)
	if err != nil {
		return nil, err
	}
	admin, err := openPool(ctx, adminURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to admin database: %w", err)
	}
	return &server{adminURL: adminURL, admin: admin}, nil
}

// resolveAdminURL returns the admin database URL given by POSTGRES_TEST_DSN, starting a container
// when the variable is unset.
func resolveAdminURL(ctx context.Context) (*url.URL, error) {
	if dsn := strings.TrimSpace(os.Getenv(adminDSNEnv)); dsn != "" {
		return parseAdminDSN(dsn)
	}
	return startContainer(ctx)
}

// parseAdminDSN parses dsn, which must be a postgres:// or postgresql:// URL so that test database
// DSNs can be derived from it.
func parseAdminDSN(dsn string) (*url.URL, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", adminDSNEnv, err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, fmt.Errorf("%s must be a postgres:// URL, got scheme %q", adminDSNEnv, u.Scheme)
	}
	return u, nil
}

// startContainer starts a postgres container and returns the URL of its admin database.
func startContainer(ctx context.Context) (*url.URL, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: postgresContainerRequest(),
		Started:          true,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to start %s container: %w", postgresImage, err), testcontainers.TerminateContainer(c))
	}
	endpoint, err := c.PortEndpoint(ctx, postgresPort, "")
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to resolve postgres container endpoint: %w", err), testcontainers.TerminateContainer(c))
	}
	return &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(postgresUser, postgresPassword),
		Host:     endpoint,
		Path:     "/" + postgresAdminDB,
		RawQuery: "sslmode=disable",
	}, nil
}

// postgresContainerRequest describes a disposable postgres container tuned for test speed.
func postgresContainerRequest() testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		Image:        postgresImage,
		ExposedPorts: []string{postgresPort},
		Env: map[string]string{
			"POSTGRES_USER":     postgresUser,
			"POSTGRES_PASSWORD": postgresPassword,
			"POSTGRES_DB":       postgresAdminDB,
		},
		// Test databases are thrown away, so trade durability for faster DDL.
		Cmd: []string{"-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off"},
		WaitingFor: wait.ForAll(
			// The entrypoint restarts the server after initdb, so readiness is logged twice.
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort(postgresPort),
		),
	}
}

// openPool opens a connection pool to dsn, capped at maxPoolConns connections, and verifies that
// the database is reachable.
func openPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection pool config: %w", err)
	}
	cfg.MaxConns = maxPoolConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return pool, nil
}

// randomSuffix returns suffixBytes random bytes, hex-encoded.
func randomSuffix() (string, error) {
	b := make([]byte, suffixBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// newDatabase creates database name, registers its removal as a test cleanup and returns a pool
// connected to it together with its DSN. The pool's own cleanup is registered later, so it runs
// first and closes the pool before the database is dropped.
func (s *server) newDatabase(ctx context.Context, t *testing.T, name string) (*pgxpool.Pool, string) {
	t.Helper()
	if _, err := s.admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("failed to create database %s: %v", name, err)
	}
	t.Cleanup(func() { s.dropDatabase(t, name) })
	dsn := s.databaseDSN(name)
	pool, err := openPool(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to database %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool, dsn
}

// dropDatabase drops database name, forcibly closing any connections the test leaked. It runs as
// a test cleanup, after the test context is canceled, so it uses a fresh context.
func (s *server) dropDatabase(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()
	if _, err := s.admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Errorf("failed to drop database %s: %v", name, err)
	}
}

// databaseDSN returns the DSN of database name, reusing the credentials and connection parameters
// of the admin URL.
func (s *server) databaseDSN(name string) string {
	u := *s.adminURL
	u.Path = "/" + name
	u.RawPath = ""
	return u.String()
}

// applySchema runs s.ddl on pool and stamps schema_migrations with s.version, leaving the
// database as golang-migrate would after migrating up to that version.
func applySchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool, s schema) {
	t.Helper()
	if _, err := pool.Exec(ctx, s.ddl); err != nil {
		t.Fatalf("failed to apply %s schema: %v", s.name, err)
	}
	if _, err := pool.Exec(ctx, createMigrationsTableSQL); err != nil {
		t.Fatalf("failed to create %s schema_migrations table: %v", s.name, err)
	}
	if _, err := pool.Exec(ctx, stampVersionSQL, s.version); err != nil {
		t.Fatalf("failed to stamp %s schema version %d: %v", s.name, s.version, err)
	}
}
