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

package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Config holds CLI flags and runtime parameters for xmc-diag.
type Config struct {
	ClickHouseHost     string
	ClickHousePort     int
	ClickHouseUser     string
	ClickHousePassword string
	ClickHouseDatabase string
	ClickHouseDSN      string

	RedpandaBrokers  []string
	TopicName        string
	ConsumerGroup    string
	SASLEnabled      bool
	SASLMechanism    string
	SASLUser         string
	SASLPassword     string
	TLSEnabled       bool
	TLSInsecure      bool
	DialWithSeedOnly bool

	CheckComponents, CheckComponentsExplicit bool
	Namespace, Kubeconfig                    string

	OutputFormat   string
	SkipSmokeTests bool
}

func parseComponentFlagDefaults() (bool, bool, string) {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("CHECK_COMPONENTS")))
	enabled := raw == "" || raw == "true" || raw == "1" || raw == "yes"
	return enabled, raw != "" && enabled, cmp.Or(os.Getenv("XMC_NAMESPACE"), os.Getenv("NAMESPACE"), "xmc")
}

func finalizeDiagConfig(cfg *Config, brokersStr string) (*Config, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}
	if cfg.SASLEnabled {
		mech := strings.ToUpper(cfg.SASLMechanism)
		if mech != "PLAIN" && mech != "SCRAM-SHA-256" && mech != "SCRAM-SHA-512" {
			return nil, fmt.Errorf("unsupported SASL mechanism %q (must be 'PLAIN', 'SCRAM-SHA-256', or 'SCRAM-SHA-512')", cfg.SASLMechanism)
		}
	}
	out := strings.ToLower(cfg.OutputFormat)
	if out != "text" && out != "json" {
		return nil, fmt.Errorf("unsupported output format %q (must be 'text' or 'json')", cfg.OutputFormat)
	}
	cfg.OutputFormat = out
	for _, b := range strings.Split(brokersStr, ",") {
		if trimmed := strings.TrimSpace(b); trimmed != "" {
			cfg.RedpandaBrokers = append(cfg.RedpandaBrokers, trimmed)
		}
	}
	return cfg, nil
}

// ParseFlags parses command-line arguments into Config.
func ParseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("xmc-diag", flag.ContinueOnError)
	checkComp, checkCompExplicit, defaultNS := parseComponentFlagDefaults()
	cfg := &Config{
		CheckComponents:         checkComp,
		CheckComponentsExplicit: checkCompExplicit,
	}
	var brokersStr string

	fs.StringVar(&cfg.ClickHouseHost, "clickhouse_host", "", "ClickHouse host (overrides CLICKHOUSE_HOST env)")
	fs.IntVar(&cfg.ClickHousePort, "clickhouse_port", 0, "ClickHouse native protocol port (overrides CLICKHOUSE_PORT env, default 9000)")
	fs.StringVar(&cfg.ClickHouseUser, "clickhouse_user", "", "ClickHouse user (overrides CLICKHOUSE_USER env, default \"default\")")
	fs.StringVar(&cfg.ClickHousePassword, "clickhouse_password", "", "ClickHouse password")
	fs.StringVar(&cfg.ClickHouseDatabase, "clickhouse_database", "", "ClickHouse database name (overrides CLICKHOUSE_DATABASE env, default \"default\")")
	fs.StringVar(&cfg.ClickHouseDSN, "clickhouse_dsn", "", "ClickHouse DSN string")
	fs.StringVar(&brokersStr, "redpanda_brokers", "localhost:9092", "Comma-separated Redpanda broker addresses")
	fs.StringVar(&cfg.TopicName, "topic_name", "xmc-metrics", "Redpanda metrics topic name")
	fs.StringVar(&cfg.ConsumerGroup, "consumer_group", "xmc-clickhouse-ingester", "Kafka consumer group name to inspect lag for")
	fs.BoolVar(&cfg.SASLEnabled, "sasl_enabled", false, "Enable SASL authentication for Redpanda")
	fs.StringVar(&cfg.SASLMechanism, "sasl_mechanism", "SCRAM-SHA-256", "SASL mechanism")
	fs.StringVar(&cfg.SASLUser, "sasl_user", "", "SASL user")
	fs.StringVar(&cfg.SASLPassword, "sasl_password", "", "SASL password")
	fs.BoolVar(&cfg.TLSEnabled, "tls_enabled", false, "Enable TLS for Redpanda connection")
	fs.BoolVar(&cfg.TLSInsecure, "tls_insecure_skip_verify", false, "Skip TLS verification for Redpanda")
	fs.BoolVar(&cfg.DialWithSeedOnly, "dial_with_seed_only", false, "Dial only seed brokers (useful for port-forwarding/tunneling)")
	fs.StringVar(&cfg.OutputFormat, "output", "text", "Output format: text or json")
	fs.BoolVar(&cfg.SkipSmokeTests, "skip_smoke_tests", false, "Skip database and topic smoke queries")
	fs.StringVar(&cfg.Namespace, "namespace", defaultNS, "Kubernetes namespace for Component CR checks")
	fs.StringVar(&cfg.Kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Path to kubeconfig file for Component CR checks")
	fs.BoolVar(&cfg.CheckComponents, "check_components", cfg.CheckComponents, "Check Component CR health in Kubernetes")
	fs.BoolVar(&cfg.CheckComponents, "check-components", cfg.CheckComponents, "Check Component CR health in Kubernetes")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "check_components" || f.Name == "check-components" {
			cfg.CheckComponentsExplicit = cfg.CheckComponents
		}
	})
	return finalizeDiagConfig(cfg, brokersStr)
}
