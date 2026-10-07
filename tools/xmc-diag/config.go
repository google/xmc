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
	raw := strings.TrimSpace(strings.ToLower(cmp.Or(os.Getenv("CHECK_COMPONENTS"), os.Getenv("ENABLE_COMPONENTS"))))
	enabled := raw == "" || raw == "true" || raw == "1" || raw == "yes"
	return enabled, raw != "" && enabled, cmp.Or(os.Getenv("XMC_NAMESPACE"), os.Getenv("NAMESPACE"), "xmc")
}

// ParseFlags parses command-line arguments into Config.
func ParseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("xmc-diag", flag.ContinueOnError)
	checkComp, checkCompExplicit, defaultNS := parseComponentFlagDefaults()

	var (
		chHost      = fs.String("clickhouse_host", "", "ClickHouse host (overrides CLICKHOUSE_HOST env)")
		chPort      = fs.Int("clickhouse_port", 0, "ClickHouse native protocol port (overrides CLICKHOUSE_PORT env, default 9000)")
		chUser      = fs.String("clickhouse_user", "", "ClickHouse user (overrides CLICKHOUSE_USER env, default \"default\")")
		chPassword  = fs.String("clickhouse_password", "", "ClickHouse password")
		chDatabase  = fs.String("clickhouse_database", "", "ClickHouse database name (overrides CLICKHOUSE_DATABASE env, default \"default\")")
		chDSN       = fs.String("clickhouse_dsn", "", "ClickHouse DSN string")
		brokersStr  = fs.String("redpanda_brokers", "localhost:9092", "Comma-separated Redpanda broker addresses")
		topicName   = fs.String("topic_name", "xmc-metrics", "Redpanda metrics topic name")
		consumerGrp = fs.String("consumer_group", "xmc-clickhouse-ingester", "Kafka consumer group name to inspect lag for")
		saslEnable  = fs.Bool("sasl_enabled", false, "Enable SASL authentication for Redpanda")
		saslMech    = fs.String("sasl_mechanism", "SCRAM-SHA-256", "SASL mechanism")
		saslUser    = fs.String("sasl_user", "", "SASL user")
		saslPass    = fs.String("sasl_password", "", "SASL password")
		tlsEnable   = fs.Bool("tls_enabled", false, "Enable TLS for Redpanda connection")
		tlsInsecure = fs.Bool("tls_insecure_skip_verify", false, "Skip TLS verification for Redpanda")
		seedOnly    = fs.Bool("dial_with_seed_only", false, "Dial only seed brokers (useful for port-forwarding/tunneling)")
		outFormat   = fs.String("output", "text", "Output format: text or json")
		skipSmoke   = fs.Bool("skip_smoke_tests", false, "Skip database and topic smoke queries")
		namespace   = fs.String("namespace", defaultNS, "Kubernetes namespace for Component CR checks")
		kubeconfig  = fs.String("kubeconfig", os.Getenv("KUBECONFIG"), "Path to kubeconfig file for Component CR checks")
	)
	fs.BoolVar(&checkComp, "check_components", checkComp, "Check Component CR health in Kubernetes")
	fs.BoolVar(&checkComp, "check-components", checkComp, "Check Component CR health in Kubernetes")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "check_components" || f.Name == "check-components" {
			checkCompExplicit = checkComp
		}
	})

	if *saslEnable {
		mech := strings.ToUpper(*saslMech)
		if mech != "PLAIN" && mech != "SCRAM-SHA-256" && mech != "SCRAM-SHA-512" {
			return nil, fmt.Errorf("unsupported SASL mechanism %q (must be 'PLAIN', 'SCRAM-SHA-256', or 'SCRAM-SHA-512')", *saslMech)
		}
	}

	out := strings.ToLower(*outFormat)
	if out != "text" && out != "json" {
		return nil, fmt.Errorf("unsupported output format %q (must be 'text' or 'json')", *outFormat)
	}

	var brokers []string
	if *brokersStr != "" {
		for _, b := range strings.Split(*brokersStr, ",") {
			b = strings.TrimSpace(b)
			if b != "" {
				brokers = append(brokers, b)
			}
		}
	}

	cfg := &Config{
		ClickHouseHost:     *chHost,
		ClickHousePort:     *chPort,
		ClickHouseUser:     *chUser,
		ClickHousePassword: *chPassword,
		ClickHouseDatabase: *chDatabase,
		ClickHouseDSN:      *chDSN,
		RedpandaBrokers:    brokers,
		TopicName:          *topicName,
		ConsumerGroup:      *consumerGrp,
		SASLEnabled:        *saslEnable,
		SASLMechanism:      *saslMech,
		SASLUser:           *saslUser,
		SASLPassword:       *saslPass,
		TLSEnabled:         *tlsEnable,
		TLSInsecure:        *tlsInsecure,
		DialWithSeedOnly:   *seedOnly,
		OutputFormat:       out,
		SkipSmokeTests:     *skipSmoke,
	}
	cfg.CheckComponents, cfg.CheckComponentsExplicit = checkComp, checkCompExplicit
	cfg.Namespace, cfg.Kubeconfig = *namespace, *kubeconfig
	return cfg, nil
}
