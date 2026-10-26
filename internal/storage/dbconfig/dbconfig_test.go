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

package dbconfig

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var allDBEnvVars = []string{
	"DB_BACKEND",
	"SPANNER_ESS_DATABASE",
	"SPANNER_XID_DATABASE",
	"SPANNER_QUOTA_PROJECT_ID",
	"POSTGRES_HOST",
	"POSTGRES_PORT",
	"POSTGRES_USER",
	"POSTGRES_PASSWORD",
	"POSTGRES_PASSWORD_FILE",
	"POSTGRES_ESS_DATABASE",
	"POSTGRES_XID_DATABASE",
	"POSTGRES_SSLMODE",
	"POSTGRES_MAX_CONNS",
}

func clearDBEnv(t *testing.T) {
	t.Helper()
	for _, key := range allDBEnvVars {
		t.Setenv(key, "")
	}
}

func writePasswordFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "postgres-password")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write password file: %v", err)
	}
	return path
}

func TestParseBackend(t *testing.T) {
	tests := []struct {
		value   string
		want    Backend
		wantErr bool
	}{
		{value: "", want: BackendSpanner},
		{value: "spanner", want: BackendSpanner},
		{value: "postgres", want: BackendPostgres},
		{value: "Postgres", wantErr: true},
		{value: "mysql", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			got, err := ParseBackend(tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseBackend(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("ParseBackend(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	postgresDefaults := Postgres{Port: 5432, SSLMode: "verify-full"}

	tests := []struct {
		name string
		env  map[string]string
		want Config
	}{
		{
			name: "unset backend selects spanner",
			want: Config{Backend: BackendSpanner},
		},
		{
			name: "spanner backend settings from db-config",
			env: map[string]string{
				"DB_BACKEND":               "spanner",
				"SPANNER_ESS_DATABASE":     "projects/p/instances/i/databases/ess-db",
				"SPANNER_XID_DATABASE":     "projects/p/instances/i/databases/xid-db",
				"SPANNER_QUOTA_PROJECT_ID": "quota-proj",
			},
			want: Config{
				Backend: BackendSpanner,
				Spanner: Spanner{
					EssDatabase:    "projects/p/instances/i/databases/ess-db",
					XidDatabase:    "projects/p/instances/i/databases/xid-db",
					QuotaProjectID: "quota-proj",
				},
			},
		},
		{
			name: "spanner backend ignores postgres settings",
			env: map[string]string{
				"DB_BACKEND":    "spanner",
				"POSTGRES_HOST": "db.internal",
			},
			want: Config{Backend: BackendSpanner},
		},
		{
			name: "postgres defaults, ignoring spanner settings",
			env: map[string]string{
				"DB_BACKEND":           "postgres",
				"SPANNER_ESS_DATABASE": "projects/p/instances/i/databases/ess-db",
			},
			want: Config{Backend: BackendPostgres, Postgres: postgresDefaults},
		},
		{
			name: "postgres backend settings from db-config",
			env: map[string]string{
				"DB_BACKEND":            "postgres",
				"POSTGRES_HOST":         "postgres.xmc.svc.cluster.local",
				"POSTGRES_PORT":         "5433",
				"POSTGRES_USER":         "xmc_user",
				"POSTGRES_ESS_DATABASE": "ess_custom",
				"POSTGRES_XID_DATABASE": "xid_custom",
				"POSTGRES_SSLMODE":      "require",
			},
			want: Config{
				Backend: BackendPostgres,
				Postgres: Postgres{
					Host:        "postgres.xmc.svc.cluster.local",
					Port:        5433,
					User:        "xmc_user",
					EssDatabase: "ess_custom",
					XidDatabase: "xid_custom",
					SSLMode:     "require",
				},
			},
		},
		{
			name: "postgres secrets and pool size",
			env: map[string]string{
				"DB_BACKEND":             "postgres",
				"POSTGRES_PASSWORD_FILE": "/etc/gcp-secrets/postgres-password",
				"POSTGRES_MAX_CONNS":     "25",
			},
			want: Config{
				Backend: BackendPostgres,
				Postgres: Postgres{
					Port:         5432,
					SSLMode:      "verify-full",
					PasswordFile: "/etc/gcp-secrets/postgres-password",
					MaxConns:     25,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearDBEnv(t)
			for key, val := range tc.env {
				t.Setenv(key, val)
			}
			got, err := Load()
			if err != nil {
				t.Fatalf("Load() failed: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Load() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoad_InvalidBackend(t *testing.T) {
	for _, value := range []string{"mysql", "Postgres"} {
		t.Run(value, func(t *testing.T) {
			clearDBEnv(t)
			t.Setenv("DB_BACKEND", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "DB_BACKEND") {
				t.Errorf("Load() error = %v, want an error naming DB_BACKEND", err)
			}
		})
	}
}

func TestLoad_InvalidPostgresIntegers(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "non-numeric port", key: "POSTGRES_PORT", value: "54x2"},
		{name: "zero port", key: "POSTGRES_PORT", value: "0"},
		{name: "negative port", key: "POSTGRES_PORT", value: "-1"},
		{name: "port above 65535", key: "POSTGRES_PORT", value: "65536"},
		{name: "non-numeric max conns", key: "POSTGRES_MAX_CONNS", value: "abc"},
		{name: "negative max conns", key: "POSTGRES_MAX_CONNS", value: "-1"},
		{name: "max conns above MaxInt32", key: "POSTGRES_MAX_CONNS", value: "2147483648"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearDBEnv(t)
			t.Setenv("DB_BACKEND", "postgres")
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Errorf("Load() with %s=%q error = %v, want error naming %s", tc.key, tc.value, err, tc.key)
			}
		})
	}
}

func TestLoad_DoesNotIncludePasswordInFormattedConfig(t *testing.T) {
	clearDBEnv(t)
	setRequiredPostgresEnv(t)
	const secret = "s3cr3t-pg-pass"
	t.Setenv("POSTGRES_PASSWORD", secret)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if formatted := fmt.Sprintf("%+v", cfg); strings.Contains(formatted, secret) {
		t.Errorf("fmt.Sprintf(\"%%+v\", cfg) leaked password: %s", formatted)
	}
}

// setRequiredPostgresEnv selects PostgreSQL and sets the variables that the connection URLs require.
func setRequiredPostgresEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_BACKEND", "postgres")
	t.Setenv("POSTGRES_HOST", "db.internal")
	t.Setenv("POSTGRES_USER", "xmc")
	t.Setenv("POSTGRES_ESS_DATABASE", "ess")
	t.Setenv("POSTGRES_XID_DATABASE", "xid")
}

func TestPostgresDSN(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T)
		wantEssDSN string
		wantXidDSN string
		wantPass   string
		wantErr    bool
	}{
		{
			name:       "DSNs leave out an empty password and default to verify-full",
			setup:      func(t *testing.T) {},
			wantEssDSN: "postgres://xmc@db.internal:5432/ess?sslmode=verify-full",
			wantXidDSN: "postgres://xmc@db.internal:5432/xid?sslmode=verify-full",
		},
		{
			name: "special characters in password percent encoded",
			setup: func(t *testing.T) {
				t.Setenv("POSTGRES_PORT", "6432")
				t.Setenv("POSTGRES_USER", "admin")
				t.Setenv("POSTGRES_PASSWORD", "p@ss:w/rd?a=1#b c")
				t.Setenv("POSTGRES_SSLMODE", "require")
			},
			wantEssDSN: "postgres://admin:p%40ss%3Aw%2Frd%3Fa=1%23b%20c@db.internal:6432/ess?sslmode=require",
			wantXidDSN: "postgres://admin:p%40ss%3Aw%2Frd%3Fa=1%23b%20c@db.internal:6432/xid?sslmode=require",
			wantPass:   "p@ss:w/rd?a=1#b c",
		},
		{
			name: "password file preferred over password env and strips only CRLF",
			setup: func(t *testing.T) {
				t.Setenv("POSTGRES_PASSWORD", "ignored-env-password")
				t.Setenv("POSTGRES_PASSWORD_FILE", writePasswordFile(t, " file pass \r\n"))
			},
			wantEssDSN: "postgres://xmc:%20file%20pass%20@db.internal:5432/ess?sslmode=verify-full",
			wantXidDSN: "postgres://xmc:%20file%20pass%20@db.internal:5432/xid?sslmode=verify-full",
			wantPass:   " file pass ",
		},
		{
			name: "unreadable password file returns error",
			setup: func(t *testing.T) {
				t.Setenv("POSTGRES_PASSWORD", "fallback-must-not-be-used")
				t.Setenv("POSTGRES_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing-secret"))
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearDBEnv(t)
			setRequiredPostgresEnv(t)
			tc.setup(t)
			cfg, loadErr := Load()
			if loadErr != nil {
				t.Fatalf("Load() failed: %v", loadErr)
			}
			pg := cfg.Postgres

			essDSN, essErr := pg.EssDSN()
			xidDSN, xidErr := pg.XidDSN()
			if (essErr != nil) != tc.wantErr || (xidErr != nil) != tc.wantErr {
				t.Fatalf("EssDSN()/XidDSN() errors = (%v, %v), wantErr %v", essErr, xidErr, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if essDSN != tc.wantEssDSN {
				t.Errorf("EssDSN() = %q, want %q", essDSN, tc.wantEssDSN)
			}
			if xidDSN != tc.wantXidDSN {
				t.Errorf("XidDSN() = %q, want %q", xidDSN, tc.wantXidDSN)
			}
			if tc.wantPass != "" {
				parsed, err := url.Parse(essDSN)
				if err != nil {
					t.Fatalf("url.Parse(%q) failed: %v", essDSN, err)
				}
				if gotPass, _ := parsed.User.Password(); gotPass != tc.wantPass {
					t.Errorf("parsed password = %q, want %q", gotPass, tc.wantPass)
				}
			}
		})
	}
}

func TestPostgresDSN_MissingSettings(t *testing.T) {
	complete := Postgres{Host: "db", Port: 5432, User: "xmc", EssDatabase: "ess", XidDatabase: "xid", SSLMode: "verify-full"}
	tests := []struct {
		name    string
		clear   func(*Postgres)
		dsn     func(Postgres) (string, error)
		wantKey string
	}{
		{name: "host", clear: func(p *Postgres) { p.Host = "" }, dsn: Postgres.EssDSN, wantKey: "POSTGRES_HOST"},
		{name: "user", clear: func(p *Postgres) { p.User = "" }, dsn: Postgres.XidDSN, wantKey: "POSTGRES_USER"},
		{name: "ess database", clear: func(p *Postgres) { p.EssDatabase = "" }, dsn: Postgres.EssDSN, wantKey: "POSTGRES_ESS_DATABASE"},
		{name: "xid database", clear: func(p *Postgres) { p.XidDatabase = "" }, dsn: Postgres.XidDSN, wantKey: "POSTGRES_XID_DATABASE"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := complete
			tc.clear(&p)
			if _, err := tc.dsn(p); err == nil || !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("DSN without %s: error = %v, want an error naming %s", tc.name, err, tc.wantKey)
			}
		})
	}
}

func TestPostgresDSN_NeedsOnlyItsOwnDatabase(t *testing.T) {
	essOnly := Postgres{Host: "db", Port: 5432, User: "xmc", EssDatabase: "ess", SSLMode: "verify-full"}
	if _, err := essOnly.EssDSN(); err != nil {
		t.Errorf("EssDSN() without an XID database failed: %v", err)
	}
}
