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

// Package main implements the xmc-diag CLI diagnostic tool entrypoint.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"xmanager_cloud/pkg/clickhouse"
)

func setClickHouseEnvs(cfg *Config) {
	if cfg.ClickHouseHost != "" {
		_ = os.Setenv("CLICKHOUSE_HOST", cfg.ClickHouseHost)
	}
	if cfg.ClickHousePort != 0 {
		_ = os.Setenv("CLICKHOUSE_PORT", fmt.Sprintf("%d", cfg.ClickHousePort))
	}
	if cfg.ClickHouseUser != "" {
		_ = os.Setenv("CLICKHOUSE_USER", cfg.ClickHouseUser)
	}
	if cfg.ClickHousePassword != "" {
		_ = os.Setenv("CLICKHOUSE_PASSWORD", cfg.ClickHousePassword)
	}
	if cfg.ClickHouseDatabase != "" {
		_ = os.Setenv("CLICKHOUSE_DATABASE", cfg.ClickHouseDatabase)
	}
	if cfg.ClickHouseDSN != "" {
		_ = os.Setenv("CLICKHOUSE_DSN", cfg.ClickHouseDSN)
	}
}

func createRedpandaOptions(cfg *Config) []kgo.Opt {
	opts := []kgo.Opt{kgo.SeedBrokers(cfg.RedpandaBrokers...)}
	if cfg.TLSEnabled {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{InsecureSkipVerify: cfg.TLSInsecure}))
	}
	if cfg.SASLEnabled {
		var mechanism sasl.Mechanism
		switch strings.ToUpper(cfg.SASLMechanism) {
		case "PLAIN":
			mechanism = plain.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsMechanism()
		case "SCRAM-SHA-256":
			mechanism = scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha256Mechanism()
		case "SCRAM-SHA-512":
			mechanism = scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha512Mechanism()
		}
		if mechanism != nil {
			opts = append(opts, kgo.SASL(mechanism))
		}
	}
	return opts
}

func main() {
	cfg, err := ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	setClickHouseEnvs(cfg)

	var chQuerier ClickHouseQuerier
	if chConn, err := clickhouse.Connect(ctx); err == nil && chConn != nil {
		chQuerier = &RealClickHouseQuerier{conn: chConn}
		defer chConn.Close()
	}

	var rpQuerier RedpandaQuerier
	if kClient, err := kgo.NewClient(createRedpandaOptions(cfg)...); err == nil && kClient != nil {
		rpQuerier = &RealRedpandaQuerier{client: kadm.NewClient(kClient)}
		defer kClient.Close()
	}

	report := RunDiagnostics(ctx, cfg, chQuerier, rpQuerier)
	out, err := FormatReport(report, cfg.OutputFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting report: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(out)
	if report.OverallStatus == StatusFailed {
		os.Exit(2)
	}
}
