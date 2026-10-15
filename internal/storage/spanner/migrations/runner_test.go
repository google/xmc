// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"xmanager_cloud/internal/storage/spanner/emulator" // definitions for emulator options
)

func TestRun(t *testing.T) {
	ctx := t.Context()

	tests := []testRunCase{
		{
			name:          "ess",
			migrationsDir: "ess",
			databaseID:    "test-ess-db-" + emulator.RandomSuffix(),
			getDB: func(emu *emulator.Emulator) string {
				return emu.ESSDatabase
			},
		},
		{
			name:          "xid",
			migrationsDir: "xid",
			databaseID:    "test-xid-db-" + emulator.RandomSuffix(),
			getDB: func(emu *emulator.Emulator) string {
				return emu.XIDDatabase
			},
		},
	}

	t.Run("Unsupported", func(t *testing.T) {
		emu := emulator.SetupWithEmulator(ctx, t).Emulator
		unsupportedCmds := []string{"unknown"}
		for _, cmd := range unsupportedCmds {
			t.Run(cmd, func(t *testing.T) {
				for _, tc := range tests {
					t.Run(tc.name, func(t *testing.T) {
						tc = withEmuDB(tc, emu)
						cfg := mustCreateConfig(t, tc, cmd, nil)
						err := run(cfg, emu.ClientOptions()...)
						if err == nil {
							t.Errorf("run(%q) succeeded using %s, want error", cmd, tc.name)
						}
					})
				}
			})
		}
	})

	t.Run("Version", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				emu := emulator.SetupWithEmulator(ctx, t).Emulator
				tc = withEmuDB(tc, emu)
				cfg := mustCreateConfig(t, tc, "version", nil)

				if err := run(cfg, emu.ClientOptions()...); err != nil {
					t.Fatalf("run(version) failed: %v", err)
				}
			})
		}
	})

	t.Run("Force", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				forceTests := []struct {
					name        string
					arg         *int
					wantErr     bool
					wantVersion int
					errContains string
				}{
					{
						name:        "valid version",
						arg:         ptr(5),
						wantVersion: 5,
					},
					{
						name:        "reset version",
						arg:         ptr(-1),
						wantVersion: -1,
					},
					{
						name:        "nil arg",
						arg:         nil,
						wantErr:     true,
						errContains: "missing argument for force command",
					},
				}

				emu := emulator.SetupWithEmulator(ctx, t).Emulator
				for _, ft := range forceTests {
					t.Run(ft.name, func(t *testing.T) {
						tc = withEmuDB(tc, emu)
						cfg := mustCreateConfig(t, tc, "force", ft.arg)

						err := run(cfg, emu.ClientOptions()...)
						if (err != nil) != ft.wantErr {
							t.Fatalf("run(force) error = %v, wantErr %v", err, ft.wantErr)
						}

						if ft.wantErr {
							if !strings.Contains(err.Error(), ft.errContains) {
								t.Errorf("run(force) error = %v, want error containing %q", err, ft.errContains)
							}
							return
						}

						r, err := newRunner(ctx, cfg, emu.ClientOptions()...)
						if err != nil {
							t.Fatalf("newRunner() failed: %v", err)
						}
						defer r.Close()

						v, dirty, err := r.Migrator.Version()
						if ft.wantVersion == -1 {
							if err != migrate.ErrNilVersion {
								t.Errorf("Version() error = %v, want %v", err, migrate.ErrNilVersion)
							}
						} else if err != nil {
							t.Errorf("Version() failed: %v", err)
						} else if int(v) != ft.wantVersion {
							t.Errorf("Version(): got %v, want %d", v, ft.wantVersion)
						}
						if dirty {
							t.Errorf("Version(): got dirty=true, want false")
						}
					})
				}
			})
		}
	})

	t.Run("Up_MigrationsNotInitialized", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				// SetupWithEmulator applies the schema but does not initialize the migration table.
				emu := emulator.SetupWithEmulator(ctx, t).Emulator
				tc = withEmuDB(tc, emu)
				cfg := mustCreateConfig(t, tc, "up", nil)

				r, err := newRunner(ctx, cfg, emu.ClientOptions()...)
				if err != nil {
					t.Fatalf("newRunner() failed: %v", err)
				}
				defer r.Close()

				// Verify initial state: Schema migration table should not be yet initialized.
				v, dirty, err := r.Migrator.Version()
				if err != migrate.ErrNilVersion {
					t.Errorf("Initial Version(): got (%v, %v, %v), want (nil, _, ErrNilVersion)", v, dirty, err)
				}

				err = run(cfg, emu.ClientOptions()...)
				if err == nil {
					t.Fatalf("run(up) succeeded, want error")
				}
				if !strings.Contains(err.Error(), "schema migrations table is not yet initialized") {
					t.Errorf("run(up) unexpected error: %v, want error containing 'schema migrations table is not yet initialized'", err)
				}

				// Verify that baseline migration was not applied, and version is still nil.
				v, dirty, err = r.Migrator.Version()
				if err != migrate.ErrNilVersion {
					t.Errorf("Final Version(): got (%v, %v, %v), want (nil, false, ErrNilVersion)", v, dirty, err)
				}
			})
		}
	})
	t.Run("Up", func(t *testing.T) {
		for _, tc := range tests {
			emu := emulator.SetupEmpty(ctx, t).Emulator
			t.Run(tc.name, func(t *testing.T) {
				tc = withEmuDB(tc, emu)
				cfg := mustCreateConfig(t, tc, "up", nil)

				if err := run(cfg, emu.ClientOptions()...); err != nil {
					t.Fatalf("run(up) failed: %v", err)
				}

				// Run again to verify that the migration is idempotent.
				if err := run(cfg, emu.ClientOptions()...); err != nil {
					t.Errorf("run(up) second time failed: %v", err)
				}
			})
		}
	})
	t.Run("Down", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				emu := emulator.SetupEmpty(ctx, t).Emulator
				tc = withEmuDB(tc, emu)
				cfg := mustCreateConfig(t, tc, "up", nil)

				// Run up first to have something to revert.
				if err := run(cfg, emu.ClientOptions()...); err != nil {
					t.Fatalf("run(up) failed: %v", err)
				}

				r, err := newRunner(ctx, cfg, emu.ClientOptions()...)
				if err != nil {
					t.Fatalf("newRunner() failed: %v", err)
				}
				defer r.Close()

				vBefore, _, err := r.Migrator.Version()
				if err != nil {
					t.Fatalf("Version() failed before down: %v", err)
				}

				// Provide "y" as confirmation via buffer.
				r.Stdin = strings.NewReader("y\n")
				var stdout bytes.Buffer
				r.Stdout = &stdout

				cfg.command = "down"
				cfg.arg = ptr(1)
				if err := r.ExecCommand(ctx, cfg.command, cfg.arg); err != nil {
					t.Fatalf("ExecCommand(down) failed: %v", err)
				}

				vAfter, _, err := r.Migrator.Version()
				if err != nil && err != migrate.ErrNilVersion {
					t.Fatalf("Version() failed after down: %v", err)
				}

				if err != migrate.ErrNilVersion && vAfter >= vBefore {
					t.Errorf("%s: Version(): got %d, want < %d", tc.name, vAfter, vBefore)
				}
			})
		}
	})
	t.Run("Init", func(t *testing.T) {
		for _, tc := range tests {
			emu := emulator.SetupEmpty(ctx, t).Emulator
			t.Run(tc.name, func(t *testing.T) {
				tc = withEmuDB(tc, emu)
				cfg := mustCreateConfig(t, tc, "init", nil)

				if err := run(cfg, emu.ClientOptions()...); err != nil {
					t.Fatalf("run(init) = %v, want nil", err)
				}

				r, err := newRunner(ctx, cfg, emu.ClientOptions()...)
				if err != nil {
					t.Fatalf("newRunner() = %v, want nil", err)
				}
				defer r.Close()

				versionAfterInit, dirty, err := r.Migrator.Version()
				if err != nil {
					t.Fatalf("Version() = %v, want nil (after init)", err)
				}
				if dirty {
					t.Errorf("Version() dirty = true, want false (after init)")
				}

				// Init command sets the version to latest so doing up command should be a no-op.
				cfg.command = "up"
				if err := run(cfg, emu.ClientOptions()...); err != nil {
					t.Fatalf("run(up) = %v, want nil (after init)", err)
				}

				versionAfterUp, dirtyAfterUp, err := r.Migrator.Version()
				if err != nil {
					t.Fatalf("Version() = %v, want nil (after up)", err)
				}
				if versionAfterUp != versionAfterInit {
					t.Errorf("Version() = %d, want %d (after up)", versionAfterUp, versionAfterInit)
				}
				if dirtyAfterUp {
					t.Errorf("Version() dirty = true, want false (after up)")
				}
			})
		}
	})
}

func TestSelectDescriptor(t *testing.T) {
	tests := []struct {
		name           string
		migrationsDir  string
		wantDescriptor []byte
		wantErr        bool
	}{
		{
			name:           "ess",
			migrationsDir:  "ess",
			wantDescriptor: essMessagesDescriptor,
			wantErr:        false,
		},
		{
			name:           "xid",
			migrationsDir:  "xid",
			wantDescriptor: xidMessagesDescriptor,
			wantErr:        false,
		},
		{
			name:          "unknown",
			migrationsDir: "unknown",
			wantErr:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectDescriptor(tc.migrationsDir)
			if (err != nil) != tc.wantErr {
				t.Errorf("selectDescriptor() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if !tc.wantErr && !bytes.Equal(got, tc.wantDescriptor) {
				t.Errorf("selectDescriptor() returned incorrect descriptor")
			}
		})
	}
}

func TestRun_Validation(t *testing.T) {
	baseCfg := config{
		projectID:     "project",
		instanceID:    "instance",
		databaseID:    "database",
		migrationsDir: "dir",
		command:       "up",
	}

	tests := []struct {
		name    string
		mutator func(*config)
	}{
		{"missing project", func(c *config) { c.projectID = "" }},
		{"missing instance", func(c *config) { c.instanceID = "" }},
		{"missing database", func(c *config) { c.databaseID = "" }},
		{"missing migrations_dir", func(c *config) { c.migrationsDir = "" }},
		{"missing command", func(c *config) { c.command = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseCfg // make a copy
			tc.mutator(&cfg)
			if err := run(&cfg); err == nil {
				t.Errorf("run() with %s: expected error, got nil", tc.name)
			}
		})
	}
}
