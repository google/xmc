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

package factory

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"xmanager_cloud/internal/storage/dbconfig"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
)

func TestOpen_Postgres(t *testing.T) {
	ctx := context.Background()
	pools := postgrestest.SetupWithDSNs(ctx, t)
	cfg := dbconfig.Config{
		Backend:  dbconfig.BackendPostgres,
		Postgres: postgresConfig(t, pools.ESSDSN, pools.XIDDSN),
	}

	for _, db := range []Database{ESS, XID} {
		t.Run(string(db), func(t *testing.T) {
			store, err := Open(ctx, cfg, db)
			if err != nil {
				t.Fatalf("Open(%q) failed: %v", db, err)
			}
			defer store.Close()

			if store.SpannerClient != nil {
				t.Error("SpannerClient is set for the postgres backend, want nil")
			}
			// The harness stamps each database with the version that this binary expects, so a
			// mixed-up database or expected version fails here.
			if err := store.CheckReady(ctx); err != nil {
				t.Errorf("CheckReady() = %v, want nil", err)
			}
		})
	}
}

// postgresConfig turns the harness URLs of the ESS and XID databases, which share one server and
// one set of credentials, into db-config settings.
func postgresConfig(t *testing.T, essDSN, xidDSN string) dbconfig.Postgres {
	t.Helper()
	ess := parseURL(t, essDSN)
	xid := parseURL(t, xidDSN)
	port, err := strconv.Atoi(ess.Port())
	if err != nil {
		t.Fatalf("invalid port in %q: %v", ess.Redacted(), err)
	}
	password, _ := ess.User.Password()
	sslMode := ess.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "prefer"
	}
	return dbconfig.Postgres{
		Host:        ess.Hostname(),
		Port:        port,
		User:        ess.User.Username(),
		Password:    password,
		EssDatabase: strings.TrimPrefix(ess.Path, "/"),
		XidDatabase: strings.TrimPrefix(xid.Path, "/"),
		SSLMode:     sslMode,
	}
}

func parseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse the test database URL: %v", err)
	}
	return u
}
