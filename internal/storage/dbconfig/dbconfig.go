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

// Package dbconfig loads the database settings from the db-config ConfigMap and builds PostgreSQL
// connection URLs from them.
package dbconfig

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strconv"

	"xmanager_cloud/internal/configutil"
	"xmanager_cloud/internal/logging"
)

const (
	defaultPostgresPort    = 5432
	minPostgresPort        = 1
	maxPostgresPort        = 65535
	defaultPostgresSSLMode = "verify-full"
)

// Backend identifies the database backend selected through DB_BACKEND.
type Backend string

const (
	// BackendSpanner selects Cloud Spanner. It is the default when DB_BACKEND is unset.
	BackendSpanner Backend = "spanner"
	// BackendPostgres selects PostgreSQL.
	BackendPostgres Backend = "postgres"
)

// ParseBackend returns the Backend named by value. An empty value selects BackendSpanner.
func ParseBackend(value string) (Backend, error) {
	switch b := Backend(value); b {
	case "":
		return BackendSpanner, nil
	case BackendSpanner, BackendPostgres:
		return b, nil
	default:
		return "", fmt.Errorf("unsupported database backend %q: must be %q or %q", value, BackendSpanner, BackendPostgres)
	}
}

// Config holds the database settings. Only the selected backend's settings are set.
type Config struct {
	Backend  Backend
	Spanner  Spanner
	Postgres Postgres
}

// Spanner holds the Cloud Spanner settings. Each field is empty when db-config does not set it.
type Spanner struct {
	EssDatabase    string
	XidDatabase    string
	QuotaProjectID string
}

// Postgres holds the PostgreSQL settings. Port defaults to 5432 and SSLMode to verify-full;
// MaxConns is an optional POSTGRES_MAX_CONNS override (0 uses the driver default), and the other
// fields are empty when unset.
type Postgres struct {
	Host         string
	Port         int
	User         string
	PasswordFile string
	EssDatabase  string
	XidDatabase  string
	SSLMode      string
	MaxConns     int32
}

// Load reads DB_BACKEND and the settings of the backend it selects; the other backend's settings
// stay empty. An unknown DB_BACKEND or invalid integer setting returns an error instead of falling
// back to a default.
func Load() (Config, error) {
	var rawBackend string
	configutil.StringEnv(&rawBackend, "DB_BACKEND", "")
	backend, err := ParseBackend(rawBackend)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_BACKEND: %w", err)
	}
	if backend == BackendSpanner {
		return Config{Backend: backend, Spanner: loadSpanner()}, nil
	}
	pg, err := loadPostgres()
	if err != nil {
		return Config{}, err
	}
	return Config{Backend: backend, Postgres: pg}, nil
}

func loadSpanner() Spanner {
	return Spanner{
		EssDatabase:    spannerSetting("SPANNER_ESS_DATABASE", "ess_spanner_database_name", "dashboard_spanner_database_name", "spanner_database_name"),
		XidDatabase:    spannerSetting("SPANNER_XID_DATABASE", "xid_spanner_database_name"),
		QuotaProjectID: spannerSetting("SPANNER_QUOTA_PROJECT_ID", "ess_quota_project_id", "xid_quota_project_id", "quota_project_id"),
	}
}

// spannerSetting reads key from the environment, falling back to deprecated legacy per-service keys
// when key is unset.
// TODO: Remove legacy per-service environment variable fallbacks once non-Helm consumers migrate to db-config.
func spannerSetting(key string, legacyKeys ...string) string {
	var val string
	configutil.StringEnv(&val, key, "")
	if val != "" {
		return val
	}
	for _, legacyKey := range legacyKeys {
		configutil.StringEnv(&val, legacyKey, "")
		if val != "" {
			logging.Warn(context.Background(), "Using deprecated database environment variable; migrate to db-config", "deprecated_env", legacyKey, "replacement_env", key)
			return val
		}
	}
	return ""
}

func loadPostgres() (Postgres, error) {
	port, err := intSetting("POSTGRES_PORT", defaultPostgresPort, minPostgresPort, maxPostgresPort)
	if err != nil {
		return Postgres{}, err
	}
	maxConns, err := intSetting("POSTGRES_MAX_CONNS", 0, 0, math.MaxInt32)
	if err != nil {
		return Postgres{}, err
	}
	var p Postgres
	p.Port = port
	p.MaxConns = int32(maxConns)
	configutil.StringEnv(&p.Host, "POSTGRES_HOST", "")
	configutil.StringEnv(&p.User, "POSTGRES_USER", "")
	configutil.StringEnv(&p.PasswordFile, "POSTGRES_PASSWORD_FILE", "")
	configutil.StringEnv(&p.EssDatabase, "POSTGRES_ESS_DATABASE", "")
	configutil.StringEnv(&p.XidDatabase, "POSTGRES_XID_DATABASE", "")
	configutil.StringEnv(&p.SSLMode, "POSTGRES_SSLMODE", defaultPostgresSSLMode)
	if p.SSLMode == "" {
		p.SSLMode = defaultPostgresSSLMode
	}
	return p, nil
}

func intSetting(key string, def, minVal, maxVal int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val < minVal || val > maxVal {
		return 0, fmt.Errorf("invalid %s %q: must be an integer between %d and %d", key, raw, minVal, maxVal)
	}
	return val, nil
}

// EssDSN returns the PostgreSQL connection URL for the ESS database.
func (p Postgres) EssDSN() (string, error) {
	return p.dsn(p.EssDatabase, "POSTGRES_ESS_DATABASE")
}

// XidDSN returns the PostgreSQL connection URL for the XID database.
func (p Postgres) XidDSN() (string, error) {
	return p.dsn(p.XidDatabase, "POSTGRES_XID_DATABASE")
}

// dsn builds the connection URL of database. It fails when the host, the user or the database is
// not set; databaseKey names the database's variable in that error.
func (p Postgres) dsn(database, databaseKey string) (string, error) {
	if key := p.missingSetting(database, databaseKey); key != "" {
		return "", fmt.Errorf("%s is not set", key)
	}
	password, err := p.resolvePassword()
	if err != nil {
		return "", err
	}
	u := &url.URL{
		Scheme:   "postgres",
		User:     userInfo(p.User, password),
		Host:     net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
		Path:     database,
		RawQuery: url.Values{"sslmode": {p.SSLMode}}.Encode(),
	}
	return u.String(), nil
}

// missingSetting returns the variable of the first required setting that is empty, or "".
func (p Postgres) missingSetting(database, databaseKey string) string {
	switch {
	case p.Host == "":
		return "POSTGRES_HOST"
	case p.User == "":
		return "POSTGRES_USER"
	case database == "":
		return databaseKey
	default:
		return ""
	}
}

// userInfo leaves out an empty password, so the driver can still look it up in PGPASSFILE.
func userInfo(user, password string) *url.Userinfo {
	if password == "" {
		return url.User(user)
	}
	return url.UserPassword(user, password)
}

// resolvePassword prefers PasswordFile over POSTGRES_PASSWORD. An unreadable file is an error, as
// in pkg/clickhouse.
func (p Postgres) resolvePassword() (string, error) {
	if p.PasswordFile == "" {
		var password string
		configutil.StringEnv(&password, "POSTGRES_PASSWORD", "")
		return password, nil
	}
	return configutil.ReadSecretFile(p.PasswordFile, "POSTGRES_PASSWORD_FILE")
}
