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

// Package bigquery configures and connects to Google Cloud BigQuery for XMC metrics analytics.
package bigquery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	// DefaultDataset is the default BigQuery dataset ID for XMC metrics.
	DefaultDataset = "xmc_metrics"
	// DefaultTable is the default BigQuery table ID for XMC metrics.
	DefaultTable     = "xmc_metrics"
	maxIdentifierLen = 1024
)

var (
	projectIDPattern  = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)
	identifierPattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
)

// Options configures the target BigQuery project, dataset, and table for metrics queries.
type Options struct {
	ProjectID, Dataset, Table string
}

// Validate checks that ProjectID, Dataset, and Table are safe, non-empty BigQuery identifiers.
func (o *Options) Validate() error {
	if o == nil {
		return errors.New("bigquery options cannot be nil")
	}
	for _, f := range []struct {
		name, val string
		re        *regexp.Regexp
	}{
		{"project ID", o.ProjectID, projectIDPattern},
		{"dataset", o.Dataset, identifierPattern},
		{"table", o.Table, identifierPattern},
	} {
		if len(f.val) == 0 || len(f.val) > maxIdentifierLen || !f.re.MatchString(f.val) {
			return fmt.Errorf("invalid bigquery %s %q", f.name, f.val)
		}
	}
	return nil
}

// TableRef returns the validated backtick-quoted Standard SQL table reference (`project.dataset.table`).
func (o *Options) TableRef() (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("`%s.%s.%s`", o.ProjectID, o.Dataset, o.Table), nil
}

// Configured reports whether the environment explicitly points at a BigQuery metrics dataset.
func Configured() bool { return strings.TrimSpace(os.Getenv("BIGQUERY_DATASET")) != "" }

// GetOptions loads and validates BigQuery Options from environment variables.
func GetOptions(fallbackProjectID ...string) (*Options, error) {
	opts := &Options{
		ProjectID: firstNonEmpty(os.Getenv("BIGQUERY_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT"), firstNonEmpty(fallbackProjectID...)),
		Dataset:   firstNonEmpty(os.Getenv("BIGQUERY_DATASET"), DefaultDataset),
		Table:     firstNonEmpty(os.Getenv("BIGQUERY_TABLE"), DefaultTable),
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	return opts, nil
}

// Rows iterates over query result rows returned by BigQuery.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Conn abstracts the BigQuery query and readiness operations required by XMC analytics consumers.
type Conn interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Ping(ctx context.Context) error
	TableRef() string
	Close() error
}

// ConnectWithOptions establishes a BigQuery client and verifies table metadata reachability.
func ConnectWithOptions(ctx context.Context, opts *Options, clientOpts ...option.ClientOption) (Conn, error) {
	tableRef, err := opts.TableRef()
	if err != nil {
		return nil, err
	}
	client, err := bigquery.NewClient(ctx, opts.ProjectID, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create bigquery client: %w", err)
	}
	conn := &clientConn{
		client:   client,
		table:    client.DatasetInProject(opts.ProjectID, opts.Dataset).Table(opts.Table),
		tableRef: tableRef,
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to ping bigquery: %w", err)
	}
	return conn, nil
}

type clientConn struct {
	client    *bigquery.Client
	table     *bigquery.Table
	tableRef  string
	closeOnce sync.Once
	closeErr  error
}

var _ Conn = (*clientConn)(nil)

func (c *clientConn) TableRef() string { return c.tableRef }

func (c *clientConn) Ping(ctx context.Context) error {
	if _, err := c.table.Metadata(ctx); err != nil {
		return fmt.Errorf("bigquery table metadata check failed: %w", err)
	}
	return nil
}

func (c *clientConn) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	q := c.client.Query(sql)
	for _, arg := range args {
		q.Parameters = append(q.Parameters, bigquery.QueryParameter{Value: arg})
	}
	it, err := q.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to execute bigquery query: %w", err)
	}
	return &rowIteratorRows{it: it}, nil
}

func (c *clientConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.client.Close() })
	return c.closeErr
}

type rowIteratorRows struct {
	it      *bigquery.RowIterator
	current []bigquery.Value
	err     error
	closed  bool
}

var _ Rows = (*rowIteratorRows)(nil)

func (r *rowIteratorRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if err := r.it.Next(&r.current); err != nil {
		if !errors.Is(err, iterator.Done) {
			r.err = err
		}
		r.current = nil
		return false
	}
	return true
}

func (r *rowIteratorRows) Scan(dest ...any) error {
	if r.closed || r.current == nil {
		return errors.New("bigquery scan called without an active row")
	}
	if len(dest) != len(r.current) {
		return fmt.Errorf("bigquery column count mismatch: got %d destinations for %d columns", len(dest), len(r.current))
	}
	for i, d := range dest {
		if err := assignColumn(i, d, r.current[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *rowIteratorRows) Err() error { return r.err }

func (r *rowIteratorRows) Close() error {
	r.closed, r.current = true, nil
	return nil
}

func assignColumn(idx int, dest any, src bigquery.Value) error {
	dv := reflect.ValueOf(dest)
	if !dv.IsValid() || dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("column %d destination must be a non-nil pointer, got %T", idx, dest)
	}
	if src == nil {
		dv.Elem().SetZero()
		return nil
	}
	sv := reflect.ValueOf(src)
	if !sv.Type().AssignableTo(dv.Elem().Type()) {
		return fmt.Errorf("column %d cannot scan %T into %T", idx, src, dest)
	}
	dv.Elem().Set(sv)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
