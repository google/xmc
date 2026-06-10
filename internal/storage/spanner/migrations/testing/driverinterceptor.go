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
// Package migrationtest provides a source driver that intercepts SQL migration files and
// applies workarounds to fix issues with unit testing migrations.
//
// Specifically, it handles Spanner emulator limitations with stored columns ((internal issue))
// by dropping and re-adding them when proto bundles are altered. Once the emulator is fixed,
// this driver should no longer be necessary and can be removed.
package migrationtest

import (
	"fmt"
	"io"
	"strings"

	"xmanager_cloud/storage/spanner/utils/spannerutils"

	"github.com/golang-migrate/migrate/v4/source"
)

// DriverInterceptor intercepts SQL migration files for purpose of testing.
type DriverInterceptor struct {
	source.Driver
	DBName string
}

// ReadUp intercepts the ReadUp call and applies workarounds to the SQL content.
func (i *DriverInterceptor) ReadUp(version uint) (io.ReadCloser, string, error) {
	r, identifier, err := i.Driver.ReadUp(version)
	if err != nil {
		return nil, "", err
	}
	ir, err := i.intercept(r, UP, version)
	if err != nil {
		return nil, "", err
	}
	return ir, identifier, nil
}

// ReadDown intercepts the ReadDown call and applies workarounds to the SQL content.
func (i *DriverInterceptor) ReadDown(version uint) (io.ReadCloser, string, error) {
	r, identifier, err := i.Driver.ReadDown(version)
	if err != nil {
		return nil, "", err
	}
	ir, err := i.intercept(r, DOWN, version)
	if err != nil {
		return nil, "", err
	}
	return ir, identifier, nil
}

func (i *DriverInterceptor) intercept(r io.ReadCloser, direction Direction, version uint) (io.ReadCloser, error) {
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	content := string(data)
	modified := injectStoredColumnsReinitialization(content, direction, version, i.DBName)
	return io.NopCloser(strings.NewReader(modified)), nil
}

type columnSpec struct {
	table      string
	name       string
	definition string
}

// storedColumns defines a set of columns that must be dropped and re-added whenever a PROTO BUNDLE
// is altered. This is workaround for (internal issue) and is required for columns that are STORED and
// use base type from PROTO. STORED columns that refer to proto but use primitive types (e.g. QueueName)
// do not require this workaround.
var storedColumns = map[string][]columnSpec{
	"ess": {
		{"Experiments", "ActiveState", "cloud.xmanager.v1.ExperimentStatus.ActiveState NOT NULL AS (Status.active_state) STORED"},
		{"Experiments", "LaunchState", "cloud.xmanager.v1.ExperimentStatus.LaunchState NOT NULL AS (Status.launch_state) STORED"},
		{"WorkUnits", "ExecutionState", "cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL AS (WorkUnitStatus.state) STORED"},
		{"WorkUnits", "ExecutionStatusMessageSeverity", "cloud.xmanager.v1.StructuredMessage.Severity AS (GREATEST(IFNULL(WorkUnitStatus.system_message.severity, 0), IFNULL(WorkUnitStatus.user_message.severity, 0))) STORED"},
	},
	"xid": {},
}

type Direction int

const (
	DONT_CARE Direction = iota
	UP
	DOWN
)

type interceptedStatement struct {
	version   uint // Version 0 matches all .versions
	direction Direction
	trigger   string
	action    func(drops, adds, statement string, result *strings.Builder)
}

var fixes = []interceptedStatement{
	{version: 00,
		trigger:   "ALTER PROTO BUNDLE",
		direction: DONT_CARE,
		action: func(drops, adds, statement string, result *strings.Builder) {
			result.WriteString(drops)
			result.WriteString(statement)
			result.WriteString(";\n")
			result.WriteString(adds)
		},
	},
	{version: 04,
		trigger:   "ALTER SEARCH INDEX",
		direction: UP,
		action: func(drops, adds, statement string, result *strings.Builder) {
			result.WriteString("DROP SEARCH INDEX ArtifactsSearchIndex;\n")
			result.WriteString("CREATE SEARCH INDEX ArtifactsSearchIndex ON Artifacts (Url_Tokens, DisplayName_Tokens, Tags_Tokens, Notes_Tokens, MimeType_Tokens, SearchableContent_Tokens);\n")
		},
	},
	{version: 04,
		trigger:   "ALTER SEARCH INDEX",
		direction: DOWN,
		action: func(drops, adds, statement string, result *strings.Builder) {
			result.WriteString("DROP SEARCH INDEX ArtifactsSearchIndex;\n")
			result.WriteString("CREATE SEARCH INDEX ArtifactsSearchIndex ON Artifacts (Url_Tokens, DisplayName_Tokens, Tags_Tokens, Notes_Tokens, MimeType_Tokens);\n")
		},
	},
}

// injectStoredColumnsReinitialization injects DDL statements to reinitialize stored columns.
// This is needed to get around Spanner emulator issues with stored columns when running tests.
func injectStoredColumnsReinitialization(content string, direction Direction, version uint, dbName string) string {
	drops, adds := getReinitializationStatements(dbName)
	if drops == "" && adds == "" {
		return content
	}

	var result strings.Builder
	statements := spannerutils.ParseDDL(content)
	for _, statement := range statements {
		matched := false
		for _, fix := range fixes {
			// Version 0 matches all versions.
			if (fix.version == 0 || fix.version == version) && fix.direction == direction && strings.Contains(strings.ToUpper(statement), fix.trigger) {
				fix.action(drops, adds, statement, &result)
				matched = true
				break
			}
		}
		if !matched {
			result.WriteString(statement)
			result.WriteString(";\n")
		}
	}
	return result.String()
}

func generateDropDDL(cols []columnSpec) string {
	var sb strings.Builder
	for _, col := range cols {
		fmt.Fprintf(&sb, "ALTER TABLE %s DROP COLUMN %s;\n", col.table, col.name)
	}
	return sb.String()
}

func generateAddDDL(cols []columnSpec) string {
	var sb strings.Builder
	for _, col := range cols {
		fmt.Fprintf(&sb, "ALTER TABLE %s ADD COLUMN %s %s;\n", col.table, col.name, col.definition)
	}
	return sb.String()
}

func getReinitializationStatements(dbName string) (drops, adds string) {
	cols, ok := storedColumns[dbName]
	if !ok {
		return "", ""
	}

	return generateDropDDL(cols), generateAddDDL(cols)
}
