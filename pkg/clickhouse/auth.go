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
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ConnectWithFallback attempts a multi-strategy authentication sequence before returning an error.
func ConnectWithFallback(ctx context.Context) (driver.Conn, error) {
	var errs []error
	strategies := []struct {
		name int
		fn   func() (*clickhouse.Options, error)
	}{
		{1, GetOptions},
		{2, getOptionsUserNoPassword},
		{3, getOptionsDefaultUser},
		{4, getOptionsNoCredentials},
	}

	for _, s := range strategies {
		conn, err := tryAuthStrategy(ctx, s.fn)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("strategy %d failed: %w", s.name, err))
	}
	return nil, fmt.Errorf("all clickhouse auth strategies failed: %v", errs)
}

func tryAuthStrategy(ctx context.Context, getOpts func() (*clickhouse.Options, error)) (driver.Conn, error) {
	opts, err := getOpts()
	if err != nil {
		return nil, fmt.Errorf("failed to get options: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping clickhouse: %w", err)
	}

	return conn, nil
}

func getOptionsUserNoPassword() (*clickhouse.Options, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	opts.Auth.Password = ""
	return opts, nil
}

func getOptionsDefaultUser() (*clickhouse.Options, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	opts.Auth.Username = "default"
	opts.Auth.Password = ""
	return opts, nil
}

func getOptionsNoCredentials() (*clickhouse.Options, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	opts.Auth.Username = ""
	opts.Auth.Password = ""
	return opts, nil
}
