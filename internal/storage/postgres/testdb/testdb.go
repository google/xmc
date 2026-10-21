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

// Package testdb provides an isolated per-test PostgreSQL database harness.
package testdb

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"xmanager_cloud/internal/storage/postgres/migrations/version"
)

const (
	testPostgresImage = "postgres:16-alpine"
	maxTestPoolConns  = 10

	createSchemaMigrationsTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version BIGINT NOT NULL PRIMARY KEY,
	dirty BOOLEAN NOT NULL
)`
	insertSchemaMigrationVersionSQL = `INSERT INTO schema_migrations (version, dirty) VALUES ($1, FALSE)`
)

var (
	//go:embed xid-schema.sql
	xidSchema string
	//go:embed ess-schema.sql
	essSchema string

	postgresOnce            sync.Once
	sharedPostgresDSN       string
	sharedPostgresErr       error
	sharedPostgresContainer testcontainers.Container
)

// TestPools contains the initialized PostgreSQL connection pools and DSNs for XID and ESS databases.
type TestPools struct {
	XIDPool *pgxpool.Pool
	ESSPool *pgxpool.Pool
	ESSDSN  string
	XIDDSN  string
}

// Setup creates isolated XID and ESS PostgreSQL databases with canonical schemas applied
// and schema_migrations initialized to the expected build-time versions.
func Setup(ctx context.Context, t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	pools := SetupWithPools(ctx, t)
	return pools.XIDPool, pools.ESSPool
}

// SetupWithPools creates isolated XID and ESS PostgreSQL databases with canonical schemas applied
// and returns the initialized pools and DSNs.
func SetupWithPools(ctx context.Context, t *testing.T) TestPools {
	t.Helper()
	return setup(ctx, t, true)
}

// SetupEmpty creates isolated, empty XID and ESS PostgreSQL databases without applying the schemas.
func SetupEmpty(ctx context.Context, t *testing.T) TestPools {
	t.Helper()
	return setup(ctx, t, false)
}

func setup(ctx context.Context, t *testing.T, applySchema bool) TestPools {
	t.Helper()
	baseDSN, err := ensurePostgresInstance()
	if err != nil {
		t.Fatalf("failed to ensure postgres instance: %v", err)
	}

	suffix := randomHexSuffix(t)
	xidDBName := fmt.Sprintf("test_xid_db_%s", suffix)
	essDBName := fmt.Sprintf("test_ess_db_%s", suffix)

	xidPool, xidDSN := createTestDatabase(ctx, t, baseDSN, xidDBName, xidSchema, version.XidExpectedSchemaVersion(), applySchema)
	essPool, essDSN := createTestDatabase(ctx, t, baseDSN, essDBName, essSchema, version.EssExpectedSchemaVersion(), applySchema)

	return TestPools{
		XIDPool: xidPool,
		ESSPool: essPool,
		ESSDSN:  essDSN,
		XIDDSN:  xidDSN,
	}
}

func createTestDatabase(
	ctx context.Context,
	t *testing.T,
	baseDSN string,
	dbName string,
	schemaSQL string,
	expectedVersion int64,
	applySchema bool,
) (*pgxpool.Pool, string) {
	t.Helper()
	if err := execAdminSQL(ctx, baseDSN, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		t.Fatalf("failed to create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = execAdminSQL(cleanupCtx, baseDSN, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
	})

	dsn, err := replaceDatabaseInDSN(baseDSN, dbName)
	if err != nil {
		t.Fatalf("failed to build DSN for %s: %v", dbName, err)
	}

	pool, err := openTestPool(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create pool for %s: %v", dbName, err)
	}
	t.Cleanup(pool.Close)

	if applySchema {
		if err := initializeDatabaseSchema(ctx, pool, schemaSQL, expectedVersion); err != nil {
			t.Fatalf("failed to initialize schema for %s: %v", dbName, err)
		}
	}
	return pool, dsn
}

func openTestPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pool config: %w", err)
	}
	cfg.MaxConns = maxTestPoolConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping pool: %w", err)
	}
	return pool, nil
}

func initializeDatabaseSchema(ctx context.Context, pool *pgxpool.Pool, schemaSQL string, expectedVersion int64) error {
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("failed to apply schema DDL: %w", err)
	}
	if _, err := pool.Exec(ctx, createSchemaMigrationsTableSQL); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}
	if _, err := pool.Exec(ctx, insertSchemaMigrationVersionSQL, expectedVersion); err != nil {
		return fmt.Errorf("failed to initialize schema_migrations version: %w", err)
	}
	return nil
}

func replaceDatabaseInDSN(baseDSN, dbName string) (string, error) {
	if !strings.Contains(baseDSN, "://") && strings.Contains(baseDSN, "=") {
		return replaceKeyValueDatabase(baseDSN, dbName), nil
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		return "", fmt.Errorf("failed to parse base DSN: %w", err)
	}
	u.Path = "/" + dbName
	u.RawPath = ""
	return u.String(), nil
}

func replaceKeyValueDatabase(baseDSN, dbName string) string {
	parts := strings.Fields(baseDSN)
	for i, part := range parts {
		if strings.HasPrefix(part, "dbname=") {
			parts[i] = "dbname=" + dbName
			return strings.Join(parts, " ")
		}
	}
	parts = append(parts, "dbname="+dbName)
	return strings.Join(parts, " ")
}

func execAdminSQL(ctx context.Context, baseDSN, query string) error {
	conn, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to admin database: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to execute admin query: %w", err)
	}
	return nil
}

func randomHexSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to generate random suffix: %v", err)
	}
	return hex.EncodeToString(b)
}

func ensurePostgresInstance() (string, error) {
	if dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN")); dsn != "" {
		return dsn, nil
	}
	postgresOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		sharedPostgresContainer, sharedPostgresDSN, sharedPostgresErr = startPostgresContainer(ctx)
	})
	return sharedPostgresDSN, sharedPostgresErr
}

func startPostgresContainer(ctx context.Context) (testcontainers.Container, string, error) {
	req := testcontainers.ContainerRequest{
		Image:        testPostgresImage,
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "postgres",
			"POSTGRES_PASSWORD": "postgres",
			"POSTGRES_DB":       "postgres",
		},
		Cmd: []string{
			"-c", "fsync=off",
			"-c", "synchronous_commit=off",
			"-c", "full_page_writes=off",
			"-c", "max_connections=500",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to start postgres testcontainer: %w", err)
	}
	dsn, err := containerDSN(ctx, c)
	if err != nil {
		return c, "", err
	}
	if err := waitForPostgresReady(ctx, dsn); err != nil {
		return c, "", err
	}
	return c, dsn, nil
}

func containerDSN(ctx context.Context, c testcontainers.Container) (string, error) {
	host, err := c.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get container host: %w", err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", fmt.Errorf("failed to get mapped postgres port: %w", err)
	}
	addr := net.JoinHostPort(host, port.Port())
	return fmt.Sprintf("postgres://postgres:postgres@%s/postgres?sslmode=disable", addr), nil
}

func waitForPostgresReady(ctx context.Context, dsn string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := pingPostgres(ctx, dsn); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for postgres readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func pingPostgres(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	return conn.Ping(ctx)
}
