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

package clickhouse

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func TestGetOptions(t *testing.T) {
	// Helper to clear env vars after test
	setEnv := func(t *testing.T, key, val string) {
		t.Helper()
		old := os.Getenv(key)
		if err := os.Setenv(key, val); err != nil {
			t.Fatalf("failed to set env %s: %v", key, err)
		}
		t.Cleanup(func() {
			if old == "" {
				if err := os.Unsetenv(key); err != nil {
					t.Errorf("failed to unset env %s: %v", key, err)
				}
			} else {
				if err := os.Setenv(key, old); err != nil {
					t.Errorf("failed to restore env %s: %v", key, err)
				}
			}
		})
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T)
		want    *clickhouse.Options
		wantErr bool
	}{
		{
			name: "default values",
			setup: func(t *testing.T) {
				// Clear relevant env vars to ensure defaults
				for _, env := range []string{"CLICKHOUSE_DSN", "CLICKHOUSE_HOST", "CLICKHOUSE_PORT", "CLICKHOUSE_DATABASE", "CLICKHOUSE_USER", "CLICKHOUSE_PASSWORD"} {
					setEnv(t, env, "")
				}
			},
			want: &clickhouse.Options{
				Addr: []string{"127.0.0.1:9000"},
				Auth: clickhouse.Auth{
					Database: "default",
					Username: "default",
					Password: "",
				},
				DialTimeout: 5 * time.Second,
			},
		},
		{
			name: "custom env vars",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_HOST", "clickhouse.example.com")
				setEnv(t, "CLICKHOUSE_PORT", "9001")
				setEnv(t, "CLICKHOUSE_DATABASE", "mydb")
				setEnv(t, "CLICKHOUSE_USER", "myuser")
				setEnv(t, "CLICKHOUSE_PASSWORD", "mypassword")
			},
			want: &clickhouse.Options{
				Addr: []string{"clickhouse.example.com:9001"},
				Auth: clickhouse.Auth{
					Database: "mydb",
					Username: "myuser",
					Password: "mypassword",
				},
				DialTimeout: 5 * time.Second,
			},
		},
		{
			name: "DSN preferred",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_DSN", "clickhouse://myuser:mypassword@clickhouse.example.com:9001/mydb")
				// These should be ignored
				setEnv(t, "CLICKHOUSE_HOST", "ignored")
			},
			want: &clickhouse.Options{
				Addr: []string{"clickhouse.example.com:9001"},
				Auth: clickhouse.Auth{
					Database: "mydb",
					Username: "myuser",
					Password: "mypassword",
				},
			},
		},
		{
			name: "invalid port",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_PORT", "invalid")
			},
			wantErr: true,
		},
		{
			name: "invalid DSN",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_DSN", "clickhouse://localhost:9000?dial_timeout=invalid")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			got, err := GetOptions()
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			// We might not want to compare all fields of clickhouse.Options as some might be defaults we don't care about
			// or contain functions. We compare Addr and Auth.
			if !reflect.DeepEqual(got.Addr, tt.want.Addr) {
				t.Errorf("GetOptions().Addr = %v, want %v", got.Addr, tt.want.Addr)
			}
			if !reflect.DeepEqual(got.Auth, tt.want.Auth) {
				t.Errorf("GetOptions().Auth = %v, want %v", got.Auth, tt.want.Auth)
			}
		})
	}
}
