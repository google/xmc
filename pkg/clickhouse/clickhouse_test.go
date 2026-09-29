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
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// writePasswordFile creates a password file of the shape the CSI driver mounts and returns its path.
func writePasswordFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clickhouse-password")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write password file: %v", err)
	}
	return path
}

func TestGetOptions(t *testing.T) {
	// Helper to set env vars with automatic cleanup
	setEnv := func(t *testing.T, key, val string) {
		t.Helper()
		t.Setenv(key, val)
	}

	baseOptions := &clickhouse.Options{
		Addr: []string{"127.0.0.1:9000"},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "",
		},
		DialTimeout: 5 * time.Second,
	}

	modifyOptions := func(base *clickhouse.Options, mutator func(opt *clickhouse.Options)) *clickhouse.Options {
		opt := *base
		mutator(&opt)
		return &opt
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
				for _, env := range []string{"CLICKHOUSE_DSN", "CLICKHOUSE_HOST", "CLICKHOUSE_PORT", "CLICKHOUSE_DATABASE", "CLICKHOUSE_USER", "CLICKHOUSE_PASSWORD", "CLICKHOUSE_PASSWORD_FILE"} {
					setEnv(t, env, "")
				}
			},
			want: baseOptions,
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
			want: modifyOptions(baseOptions, func(opt *clickhouse.Options) {
				opt.Addr = []string{"clickhouse.example.com:9001"}
				opt.Auth = clickhouse.Auth{
					Database: "mydb",
					Username: "myuser",
					Password: "mypassword",
				}
			}),
		},
		{
			name: "DSN preferred",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_DSN", "clickhouse://myuser:mypassword@clickhouse.example.com:9001/mydb")
				// These should be ignored
				setEnv(t, "CLICKHOUSE_HOST", "ignored")
			},
			want: modifyOptions(baseOptions, func(opt *clickhouse.Options) {
				opt.Addr = []string{"clickhouse.example.com:9001"}
				opt.Auth = clickhouse.Auth{
					Database: "mydb",
					Username: "myuser",
					Password: "mypassword",
				}
			}),
		},
		{
			name: "password file preferred over password env var",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_PASSWORD", "env-password")
				// The trailing newline is what Secret Manager payloads carry in practice.
				setEnv(t, "CLICKHOUSE_PASSWORD_FILE", writePasswordFile(t, "file-password\n"))
			},
			want: modifyOptions(baseOptions, func(opt *clickhouse.Options) {
				opt.Auth.Password = "file-password"
			}),
		},
		{
			name: "password file keeps significant whitespace",
			setup: func(t *testing.T) {
				// A space is a legal password character, so only the line terminator may be stripped.
				setEnv(t, "CLICKHOUSE_PASSWORD_FILE", writePasswordFile(t, " pa ss \r\n"))
			},
			want: modifyOptions(baseOptions, func(opt *clickhouse.Options) {
				opt.Auth.Password = " pa ss "
			}),
		},
		{
			name: "unreadable password file",
			setup: func(t *testing.T) {
				setEnv(t, "CLICKHOUSE_PASSWORD_FILE", filepath.Join(t.TempDir(), "absent"))
			},
			wantErr: true,
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

func TestConfigured(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{
			name: "no clickhouse variables",
			env:  map[string]string{"CLICKHOUSE_DSN": "", "CLICKHOUSE_HOST": ""},
			want: false,
		},
		{
			// The host is what the helm chart renders, and only when metrics are enabled.
			name: "host configured",
			env:  map[string]string{"CLICKHOUSE_DSN": "", "CLICKHOUSE_HOST": "ch.example.internal"},
			want: true,
		},
		{
			name: "dsn configured",
			env:  map[string]string{"CLICKHOUSE_DSN": "clickhouse://ch:9000", "CLICKHOUSE_HOST": ""},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			if got := Configured(); got != tt.want {
				t.Errorf("Configured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPasswordFromFileEnv(t *testing.T) {
	t.Run("unset returns false", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_PASSWORD_FILE", "")
		pw, ok, err := PasswordFromFileEnv()
		if err != nil || ok || pw != "" {
			t.Errorf("PasswordFromFileEnv() = (%q, %v, %v), want (\"\", false, nil)", pw, ok, err)
		}
	})

	t.Run("set and readable returns trimmed password", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_PASSWORD_FILE", writePasswordFile(t, " s3cret \r\n"))
		pw, ok, err := PasswordFromFileEnv()
		if err != nil || !ok || pw != " s3cret " {
			t.Errorf("PasswordFromFileEnv() = (%q, %v, %v), want (\" s3cret \", true, nil)", pw, ok, err)
		}
	})

	t.Run("set and unreadable returns error", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing"))
		_, ok, err := PasswordFromFileEnv()
		if err == nil || !ok {
			t.Errorf("PasswordFromFileEnv() = (_, %v, %v), want (true, non-nil error)", ok, err)
		}
	})
}

func TestConnectWithOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if _, err := ConnectWithOptions(ctx, nil); err == nil {
		t.Errorf("ConnectWithOptions(nil) succeeded, want error")
	}

	opts := &clickhouse.Options{
		Addr:        []string{"127.0.0.1:1"},
		DialTimeout: 100 * time.Millisecond,
	}
	if _, err := ConnectWithOptions(ctx, opts); err == nil {
		t.Errorf("ConnectWithOptions(unreachable) succeeded, want error")
	}

	t.Setenv("CLICKHOUSE_DSN", "clickhouse://localhost:9000?dial_timeout=invalid")
	if _, err := Connect(ctx); err == nil {
		t.Errorf("Connect(invalid DSN) succeeded, want error")
	}
}
