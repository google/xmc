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

type strategyFunc func() (*clickhouse.Options, error)

// ConnectWithFallback attempts a 4-strategy authentication fallback before raising an error.
func ConnectWithFallback(ctx context.Context) (driver.Conn, error) {
	strategies := []struct {
		name string
		fn   strategyFunc
	}{
		{"configured_credentials", GetOptions},
		{"user_no_password", getOptionsUserNoPassword},
		{"default_user", getOptionsDefaultUser},
		{"no_credentials", getOptionsNoCredentials},
	}

	var errs []error
	for _, s := range strategies {
		conn, err := tryConnectStrategy(ctx, s.fn)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("strategy %q failed: %w", s.name, err))
	}

	return nil, fmt.Errorf("all clickhouse auth strategies failed: %v", errs)
}

func tryConnectStrategy(ctx context.Context, strategy strategyFunc) (driver.Conn, error) {
	opts, err := strategy()
	if err != nil {
		return nil, fmt.Errorf("failed strategy config: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed strategy open: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed strategy ping: %w", err)
	}

	return conn, nil
}

func getOptionsUserNoPassword() (*clickhouse.Options, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	newOpts := *opts
	newOpts.Auth.Password = ""
	return &newOpts, nil
}

func getOptionsDefaultUser() (*clickhouse.Options, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	newOpts := *opts
	newOpts.Auth.Username = "default"
	newOpts.Auth.Password = ""
	return &newOpts, nil
}

func getOptionsNoCredentials() (*clickhouse.Options, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	newOpts := *opts
	newOpts.Auth.Username = ""
	newOpts.Auth.Password = ""
	return &newOpts, nil
}
