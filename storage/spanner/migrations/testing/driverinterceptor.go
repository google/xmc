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

	"golang/github_com/golang_migrate/migrate/v/v4/source/source"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
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
	ir, err := i.intercept(r)
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
	ir, err := i.intercept(r)
	if err != nil {
		return nil, "", err
	}
	return ir, identifier, nil
}

func (i *DriverInterceptor) intercept(r io.ReadCloser) (io.ReadCloser, error) {
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	content := string(data)
	modified := injectStoredColumnsReinitialization(content, i.DBName)
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

// injectStoredColumnsReinitialization injects DDL statements to reinitialize stored columns.
// This is needed to get around Spanner emulator issues with stored columns when running tests.
func injectStoredColumnsReinitialization(content string, dbName string) string {
	if !strings.Contains(strings.ToUpper(content), "ALTER PROTO BUNDLE") {
		return content
	}

	drops, adds := getReinitializationStatements(dbName)
	if drops == "" && adds == "" {
		return content
	}

	var result strings.Builder
	statements := spannerutils.ParseDDL(content)
	for _, statement := range statements {
		if strings.Contains(strings.ToUpper(statement), "ALTER PROTO BUNDLE") {
			result.WriteString(drops)
			result.WriteString(statement)
			result.WriteString(";\n")
			result.WriteString(adds)
		} else {
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
