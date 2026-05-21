// Copyright 2025 Google LLC
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
package migrationtest

import (
	"io"
	"strings"
	"testing"

	"golang/cmp/cmp"
	"golang/github_com/golang_migrate/migrate/v/v4/source/source"
)

func TestInjectStoredColumnsReinitialization(t *testing.T) {
	tests := []struct {
		name    string
		content string
		dbName  string
		want    string
	}{
		{
			name:    "no ALTER PROTO BUNDLE",
			content: "CREATE TABLE T1 (A INT64);",
			dbName:  "ess",
			want:    "CREATE TABLE T1 (A INT64);",
		},
		{
			name:    "ess database with ALTER PROTO BUNDLE",
			content: "ALTER PROTO BUNDLE INSERT ( ... );",
			dbName:  "ess",
			want: strings.Join([]string{
				"ALTER TABLE Experiments DROP COLUMN ActiveState;",
				"ALTER TABLE Experiments DROP COLUMN LaunchState;",
				"ALTER TABLE WorkUnits DROP COLUMN ExecutionState;",
				"ALTER TABLE WorkUnits DROP COLUMN ExecutionStatusMessageSeverity;",
				"ALTER PROTO BUNDLE INSERT ( ... );",
				"ALTER TABLE Experiments ADD COLUMN ActiveState cloud.xmanager.v1.ExperimentStatus.ActiveState NOT NULL AS (Status.active_state) STORED;",
				"ALTER TABLE Experiments ADD COLUMN LaunchState cloud.xmanager.v1.ExperimentStatus.LaunchState NOT NULL AS (Status.launch_state) STORED;",
				"ALTER TABLE WorkUnits ADD COLUMN ExecutionState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL AS (WorkUnitStatus.state) STORED;",
				"ALTER TABLE WorkUnits ADD COLUMN ExecutionStatusMessageSeverity cloud.xmanager.v1.StructuredMessage.Severity AS (GREATEST(IFNULL(WorkUnitStatus.system_message.severity, 0), IFNULL(WorkUnitStatus.user_message.severity, 0))) STORED;",
				"",
			}, "\n"),
		},
		{
			name:    "case-insensitive ALTER PROTO BUNDLE",
			content: "alter proto bundle insert ( ... );",
			dbName:  "ess",
			want: strings.Join([]string{
				"ALTER TABLE Experiments DROP COLUMN ActiveState;",
				"ALTER TABLE Experiments DROP COLUMN LaunchState;",
				"ALTER TABLE WorkUnits DROP COLUMN ExecutionState;",
				"ALTER TABLE WorkUnits DROP COLUMN ExecutionStatusMessageSeverity;",
				"alter proto bundle insert ( ... );",
				"ALTER TABLE Experiments ADD COLUMN ActiveState cloud.xmanager.v1.ExperimentStatus.ActiveState NOT NULL AS (Status.active_state) STORED;",
				"ALTER TABLE Experiments ADD COLUMN LaunchState cloud.xmanager.v1.ExperimentStatus.LaunchState NOT NULL AS (Status.launch_state) STORED;",
				"ALTER TABLE WorkUnits ADD COLUMN ExecutionState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL AS (WorkUnitStatus.state) STORED;",
				"ALTER TABLE WorkUnits ADD COLUMN ExecutionStatusMessageSeverity cloud.xmanager.v1.StructuredMessage.Severity AS (GREATEST(IFNULL(WorkUnitStatus.system_message.severity, 0), IFNULL(WorkUnitStatus.user_message.severity, 0))) STORED;",
				"",
			}, "\n"),
		},
		{
			name:    "xid database with ALTER PROTO BUNDLE",
			content: "ALTER PROTO BUNDLE INSERT ( ... );",
			dbName:  "xid",
			want:    "ALTER PROTO BUNDLE INSERT ( ... );",
		},
		{
			name:    "unknown database",
			content: "ALTER PROTO BUNDLE INSERT ( ... );",
			dbName:  "unknown",
			want:    "ALTER PROTO BUNDLE INSERT ( ... );",
		},
		{
			name: "multiple statements",
			content: `CREATE TABLE T1 (A INT64);
ALTER PROTO BUNDLE INSERT ( ... );
CREATE TABLE T2 (B INT64);`,
			dbName: "ess",
			want: strings.Join([]string{
				"CREATE TABLE T1 (A INT64);",
				"ALTER TABLE Experiments DROP COLUMN ActiveState;",
				"ALTER TABLE Experiments DROP COLUMN LaunchState;",
				"ALTER TABLE WorkUnits DROP COLUMN ExecutionState;",
				"ALTER TABLE WorkUnits DROP COLUMN ExecutionStatusMessageSeverity;",
				"ALTER PROTO BUNDLE INSERT ( ... );",
				"ALTER TABLE Experiments ADD COLUMN ActiveState cloud.xmanager.v1.ExperimentStatus.ActiveState NOT NULL AS (Status.active_state) STORED;",
				"ALTER TABLE Experiments ADD COLUMN LaunchState cloud.xmanager.v1.ExperimentStatus.LaunchState NOT NULL AS (Status.launch_state) STORED;",
				"ALTER TABLE WorkUnits ADD COLUMN ExecutionState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL AS (WorkUnitStatus.state) STORED;",
				"ALTER TABLE WorkUnits ADD COLUMN ExecutionStatusMessageSeverity cloud.xmanager.v1.StructuredMessage.Severity AS (GREATEST(IFNULL(WorkUnitStatus.system_message.severity, 0), IFNULL(WorkUnitStatus.user_message.severity, 0))) STORED;",
				"CREATE TABLE T2 (B INT64);",
				"",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectStoredColumnsReinitialization(tt.content, tt.dbName)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("injectStoredColumnsReinitialization() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type mockDriver struct {
	source.Driver
	readUp   func(version uint) (io.ReadCloser, string, error)
	readDown func(version uint) (io.ReadCloser, string, error)
}

func (m *mockDriver) ReadUp(version uint) (io.ReadCloser, string, error) {
	return m.readUp(version)
}

func (m *mockDriver) ReadDown(version uint) (io.ReadCloser, string, error) {
	return m.readDown(version)
}

func TestDriverInterceptor_ReadUp(t *testing.T) {
	content := "ALTER PROTO BUNDLE INSERT ( ... );"
	identifier := "1_test.up.sql"
	mock := &mockDriver{
		readUp: func(version uint) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader(content)), identifier, nil
		},
	}

	interceptor := &DriverInterceptor{
		Driver: mock,
		DBName: "xid",
	}

	r, ident, err := interceptor.ReadUp(1)
	if err != nil {
		t.Fatalf("ReadUp(1) failed: %v", err)
	}
	if ident != identifier {
		t.Errorf("ReadUp(1) identifier = %v, want %v", ident, identifier)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll(r) failed: %v", err)
	}

	got := string(data)
	want := injectStoredColumnsReinitialization(content, "xid")
	if got != want {
		t.Errorf("ReadUp(1) content = %q, want %q", got, want)
	}
}

func TestDriverInterceptor_ReadDown(t *testing.T) {
	content := "ALTER PROTO BUNDLE INSERT ( ... );"
	identifier := "1_test.down.sql"
	mock := &mockDriver{
		readDown: func(version uint) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader(content)), identifier, nil
		},
	}

	interceptor := &DriverInterceptor{
		Driver: mock,
		DBName: "xid",
	}

	r, ident, err := interceptor.ReadDown(1)
	if err != nil {
		t.Fatalf("ReadDown(1) failed: %v", err)
	}
	if ident != identifier {
		t.Errorf("ReadDown(1) identifier = %v, want %v", ident, identifier)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll(r) failed: %v", err)
	}

	got := string(data)
	want := injectStoredColumnsReinitialization(content, "xid")
	if got != want {
		t.Errorf("ReadDown(1) content = %q, want %q", got, want)
	}
}
