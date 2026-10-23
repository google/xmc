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
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultPostgresPort    = 5432
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

// Config holds the database settings for both backends.
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

// Postgres holds the PostgreSQL settings. Port defaults to 5432 and SSLMode to verify-full; the
// other fields are empty when unset.
type Postgres struct {
	Host         string
	Port         int
	User         string
	Password     string
	PasswordFile string
	EssDatabase  string
	XidDatabase  string
	SSLMode      string
	MaxConns     int32
}

// Load reads the database settings from environment variables. A variable set to an invalid value
// is an error: Load never falls back to another backend or to a default.
func Load() (Config, error) {
	backend, err := ParseBackend(os.Getenv("DB_BACKEND"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_BACKEND: %w", err)
	}
	postgres, err := loadPostgres()
	if err != nil {
		return Config{}, err
	}
	return Config{Backend: backend, Spanner: loadSpanner(), Postgres: postgres}, nil
}

func loadSpanner() Spanner {
	return Spanner{
		EssDatabase:    os.Getenv("SPANNER_ESS_DATABASE"),
		XidDatabase:    os.Getenv("SPANNER_XID_DATABASE"),
		QuotaProjectID: os.Getenv("SPANNER_QUOTA_PROJECT_ID"),
	}
}

func loadPostgres() (Postgres, error) {
	port, err := intEnv("POSTGRES_PORT", defaultPostgresPort, 1, math.MaxUint16)
	if err != nil {
		return Postgres{}, err
	}
	maxConns, err := intEnv("POSTGRES_MAX_CONNS", 0, 0, math.MaxInt32)
	if err != nil {
		return Postgres{}, err
	}
	return Postgres{
		Host:         os.Getenv("POSTGRES_HOST"),
		Port:         port,
		User:         os.Getenv("POSTGRES_USER"),
		Password:     os.Getenv("POSTGRES_PASSWORD"),
		PasswordFile: os.Getenv("POSTGRES_PASSWORD_FILE"),
		EssDatabase:  os.Getenv("POSTGRES_ESS_DATABASE"),
		XidDatabase:  os.Getenv("POSTGRES_XID_DATABASE"),
		SSLMode:      stringEnv("POSTGRES_SSLMODE", defaultPostgresSSLMode),
		MaxConns:     int32(maxConns),
	}, nil
}

// intEnv returns the integer in key, or defaultVal when key is unset. A value that is not an
// integer from minVal to maxVal is an error.
func intEnv(key string, defaultVal, minVal, maxVal int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < minVal || n > maxVal {
		return 0, fmt.Errorf("%s must be an integer from %d to %d, got %q", key, minVal, maxVal, value)
	}
	return n, nil
}

// stringEnv returns the value of key, or defaultVal when key is unset or empty.
func stringEnv(key, defaultVal string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultVal
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

// resolvePassword prefers PasswordFile over Password. An unreadable file is an error, as in
// pkg/clickhouse.
func (p Postgres) resolvePassword() (string, error) {
	if p.PasswordFile == "" {
		return p.Password, nil
	}
	content, err := os.ReadFile(p.PasswordFile)
	if err != nil {
		return "", fmt.Errorf("failed to read POSTGRES_PASSWORD_FILE %q: %w", p.PasswordFile, err)
	}
	// Strip only trailing line terminators so significant whitespace in passwords is preserved.
	return strings.TrimRight(string(content), "\r\n"), nil
}
