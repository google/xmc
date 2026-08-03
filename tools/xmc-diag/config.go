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
	"flag"
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

	RedpandaBrokers []string
	TopicName       string
	ConsumerGroup   string
	SASLEnabled     bool
	SASLMechanism   string
	SASLUser        string
	SASLPassword    string
	TLSEnabled      bool
	TLSInsecure     bool

	OutputFormat   string
	SkipSmokeTests bool
}

// ParseFlags parses command-line arguments into Config.
func ParseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("xmc-diag", flag.ContinueOnError)

	var (
		chHost      = fs.String("clickhouse_host", "", "ClickHouse host (overrides CLICKHOUSE_HOST env)")
		chPort      = fs.Int("clickhouse_port", 9000, "ClickHouse native protocol port")
		chUser      = fs.String("clickhouse_user", "default", "ClickHouse user")
		chPassword  = fs.String("clickhouse_password", "", "ClickHouse password")
		chDatabase  = fs.String("clickhouse_database", "default", "ClickHouse database name")
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
		outFormat   = fs.String("output", "text", "Output format: text or json")
		skipSmoke   = fs.Bool("skip_smoke_tests", false, "Skip latency performance smoke tests")
	)

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	brokers := strings.Split(*brokersStr, ",")
	for i, b := range brokers {
		brokers[i] = strings.TrimSpace(b)
	}

	return &Config{
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
		OutputFormat:       strings.ToLower(*outFormat),
		SkipSmokeTests:     *skipSmoke,
	}, nil
}
